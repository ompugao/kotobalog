package audio

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

const defaultBlockSize = 4096

// ParseWAVPCM16 parses a 16-bit linear PCM RIFF/WAVE byte slice and returns the
// raw interleaved little-endian PCM bytes along with sampleRate and channels.
func ParseWAVPCM16(wavBytes []byte) (pcmLE []byte, sampleRate int, channels int, err error) {
	if len(wavBytes) < 44 {
		return nil, 0, 0, errors.New("wav too short")
	}
	if !bytes.Equal(wavBytes[0:4], []byte("RIFF")) || !bytes.Equal(wavBytes[8:12], []byte("WAVE")) {
		return nil, 0, 0, errors.New("invalid RIFF/WAVE header")
	}

	offset := 12
	var (
		foundFmt      bool
		audioFormat   uint16
		numChannels   uint16
		sRate         uint32
		bitsPerSample uint16
		pcmData       []byte
	)

	for offset+8 <= len(wavBytes) {
		chunkID := string(wavBytes[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(wavBytes[offset+4 : offset+8]))
		offset += 8
		if chunkSize < 0 {
			return nil, 0, 0, errors.New("negative wav chunk size")
		}

		switch chunkID {
		case "fmt ":
			if offset+16 > len(wavBytes) || chunkSize < 16 {
				return nil, 0, 0, errors.New("truncated fmt chunk")
			}
			audioFormat = binary.LittleEndian.Uint16(wavBytes[offset : offset+2])
			numChannels = binary.LittleEndian.Uint16(wavBytes[offset+2 : offset+4])
			sRate = binary.LittleEndian.Uint32(wavBytes[offset+4 : offset+8])
			bitsPerSample = binary.LittleEndian.Uint16(wavBytes[offset+14 : offset+16])
			foundFmt = true
		case "data":
			end := offset + chunkSize
			if end > len(wavBytes) {
				end = len(wavBytes)
			}
			pcmData = wavBytes[offset:end]
		}

		if pcmData != nil && foundFmt {
			break
		}
		offset += chunkSize
		if chunkSize%2 != 0 {
			offset++
		}
	}

	if !foundFmt || pcmData == nil {
		return nil, 0, 0, errors.New("missing fmt or data chunk in wav")
	}
	if audioFormat != 1 {
		return nil, 0, 0, fmt.Errorf("unsupported wav audio format: %d (want PCM=1)", audioFormat)
	}
	if bitsPerSample != 16 {
		return nil, 0, 0, fmt.Errorf("unsupported wav bits per sample: %d (want 16)", bitsPerSample)
	}
	if numChannels < 1 || numChannels > 2 {
		return nil, 0, 0, fmt.Errorf("unsupported wav channel count: %d", numChannels)
	}
	if sRate < 8000 || sRate > 96000 {
		return nil, 0, 0, fmt.Errorf("unsupported wav sample rate: %d", sRate)
	}

	blockAlign := int(numChannels) * 2
	alignedLen := len(pcmData) - (len(pcmData) % blockAlign)
	if alignedLen <= 0 {
		return nil, 0, 0, errors.New("wav contains no complete PCM samples")
	}

	return pcmData[:alignedLen], int(sRate), int(numChannels), nil
}

// EncodePCM16ToFLAC encodes interleaved little-endian 16-bit signed PCM bytes into a FLAC stream.
func EncodePCM16ToFLAC(pcmLE []byte, sampleRate int, channels int) ([]byte, error) {
	if channels < 1 || channels > 2 {
		return nil, fmt.Errorf("unsupported channel count: %d", channels)
	}
	if sampleRate < 8000 || sampleRate > 96000 {
		return nil, fmt.Errorf("unsupported sample rate: %d", sampleRate)
	}
	blockAlign := channels * 2
	if len(pcmLE) < blockAlign || len(pcmLE)%blockAlign != 0 {
		return nil, fmt.Errorf("invalid pcm16 byte length: %d (blockAlign=%d)", len(pcmLE), blockAlign)
	}

	totalInterChannelSamples := len(pcmLE) / blockAlign
	md5sum := md5.Sum(pcmLE)

	info := &meta.StreamInfo{
		BlockSizeMin:  defaultBlockSize,
		BlockSizeMax:  defaultBlockSize,
		SampleRate:    uint32(sampleRate),
		NChannels:     uint8(channels),
		BitsPerSample: 16,
		NSamples:      uint64(totalInterChannelSamples),
		MD5sum:        md5sum,
	}

	var buf bytes.Buffer
	enc, err := flac.NewEncoder(&buf, info)
	if err != nil {
		return nil, fmt.Errorf("create flac encoder: %w", err)
	}

	chAssign := frame.ChannelsMono
	if channels == 2 {
		chAssign = frame.ChannelsLR
	}

	for offset := 0; offset < totalInterChannelSamples; offset += defaultBlockSize {
		end := offset + defaultBlockSize
		if end > totalInterChannelSamples {
			end = totalInterChannelSamples
		}
		blockLen := end - offset

		subframes := make([]*frame.Subframe, channels)
		for ch := 0; ch < channels; ch++ {
			samples := make([]int32, blockLen)
			for i := 0; i < blockLen; i++ {
				byteIdx := ((offset + i) * channels + ch) * 2
				sample := int16(binary.LittleEndian.Uint16(pcmLE[byteIdx : byteIdx+2]))
				samples[i] = int32(sample)
			}
			subframes[ch] = &frame.Subframe{
				SubHeader: frame.SubHeader{
					Pred: frame.PredVerbatim,
				},
				Samples:  samples,
				NSamples: blockLen,
			}
		}

		fr := &frame.Frame{
			Header: frame.Header{
				HasFixedBlockSize: true,
				BlockSize:         uint16(blockLen),
				SampleRate:        uint32(sampleRate),
				Channels:          chAssign,
				BitsPerSample:     16,
			},
			Subframes: subframes,
		}
		if err := enc.WriteFrame(fr); err != nil {
			return nil, fmt.Errorf("write flac frame at sample %d: %w", offset, err)
		}
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close flac encoder: %w", err)
	}

	return buf.Bytes(), nil
}

// DecodeFLACToPCM16 decodes a FLAC stream into interleaved little-endian 16-bit signed PCM bytes,
// verifying frame CRCs, sample counts, and the STREAMINFO MD5 checksum (when present).
func DecodeFLACToPCM16(flacBytes []byte) (pcmLE []byte, info *meta.StreamInfo, err error) {
	if len(flacBytes) < 42 || !bytes.Equal(flacBytes[0:4], []byte("fLaC")) {
		return nil, nil, errors.New("invalid flac magic bytes")
	}

	stream, err := flac.New(bytes.NewReader(flacBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("open flac stream: %w", err)
	}
	defer stream.Close()

	si := stream.Info
	if si == nil {
		return nil, nil, errors.New("missing flac STREAMINFO")
	}
	if si.BitsPerSample != 16 {
		return nil, nil, fmt.Errorf("unsupported flac bits per sample: %d (want 16)", si.BitsPerSample)
	}
	if si.NChannels < 1 || si.NChannels > 2 {
		return nil, nil, fmt.Errorf("unsupported flac channel count: %d", si.NChannels)
	}
	if si.SampleRate < 8000 || si.SampleRate > 96000 {
		return nil, nil, fmt.Errorf("unsupported flac sample rate: %d", si.SampleRate)
	}

	channels := int(si.NChannels)
	var out bytes.Buffer
	if si.NSamples > 0 && si.NSamples < 100_000_000 {
		out.Grow(int(si.NSamples) * channels * 2)
	}

	var decodedSamples uint64
	for {
		fr, err := stream.ParseNext()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("decode flac frame: %w", err)
		}
		if len(fr.Subframes) != channels {
			return nil, nil, fmt.Errorf("flac frame channel mismatch: got %d, want %d", len(fr.Subframes), channels)
		}
		blockLen := int(fr.BlockSize)
		for ch := 0; ch < channels; ch++ {
			if len(fr.Subframes[ch].Samples) != blockLen {
				return nil, nil, fmt.Errorf("subframe sample count mismatch: got %d, want %d", len(fr.Subframes[ch].Samples), blockLen)
			}
		}

		frameBytes := make([]byte, blockLen*channels*2)
		for i := 0; i < blockLen; i++ {
			for ch := 0; ch < channels; ch++ {
				s := fr.Subframes[ch].Samples[i]
				if s < -32768 || s > 32767 {
					return nil, nil, fmt.Errorf("sample out of 16-bit range: %d", s)
				}
				idx := (i*channels + ch) * 2
				binary.LittleEndian.PutUint16(frameBytes[idx:idx+2], uint16(int16(s)))
			}
		}
		out.Write(frameBytes)
		decodedSamples += uint64(blockLen)
	}

	if decodedSamples == 0 {
		return nil, nil, errors.New("flac stream contains zero samples")
	}
	if si.NSamples > 0 && decodedSamples != si.NSamples {
		return nil, nil, fmt.Errorf("flac sample count mismatch: decoded %d, streaminfo %d", decodedSamples, si.NSamples)
	}

	pcm := out.Bytes()
	var zeroMD5 [16]byte
	if si.MD5sum != zeroMD5 {
		actualMD5 := md5.Sum(pcm)
		if actualMD5 != si.MD5sum {
			return nil, nil, fmt.Errorf("flac MD5 mismatch: got %x, want %x", actualMD5, si.MD5sum)
		}
	}

	return pcm, si, nil
}

// VerifyFLAC validates that data is a well-formed, decodable 16-bit FLAC file whose
// frames and MD5 checksum (if present) are intact.
func VerifyFLAC(data []byte) (*meta.StreamInfo, error) {
	_, info, err := DecodeFLACToPCM16(data)
	return info, err
}

// ConvertWAVToFLACVerified converts a 16-bit PCM WAV byte slice to FLAC and verifies
// that decoding the resulting FLAC yields bit-identical PCM bytes.
func ConvertWAVToFLACVerified(wavBytes []byte) ([]byte, error) {
	origPCM, sampleRate, channels, err := ParseWAVPCM16(wavBytes)
	if err != nil {
		return nil, fmt.Errorf("parse wav: %w", err)
	}

	flacBytes, err := EncodePCM16ToFLAC(origPCM, sampleRate, channels)
	if err != nil {
		return nil, fmt.Errorf("encode flac: %w", err)
	}

	decodedPCM, info, err := DecodeFLACToPCM16(flacBytes)
	if err != nil {
		return nil, fmt.Errorf("verify decoded flac: %w", err)
	}
	if int(info.SampleRate) != sampleRate || int(info.NChannels) != channels {
		return nil, fmt.Errorf("flac metadata mismatch: sr=%d ch=%d", info.SampleRate, info.NChannels)
	}
	if !bytes.Equal(origPCM, decodedPCM) {
		return nil, errors.New("bit-exact verification failed: decoded PCM differs from source WAV PCM")
	}

	return flacBytes, nil
}
