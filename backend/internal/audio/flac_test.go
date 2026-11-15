package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
	"testing"
)

func makeTestWAV(pcmLE []byte, sampleRate, channels int) []byte {
	dataSize := len(pcmLE)
	buf := make([]byte, 44+dataSize)
	copy(buf[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], []byte("WAVE"))
	copy(buf[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*channels*2))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(channels*2))
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))
	copy(buf[44:], pcmLE)
	return buf
}

func TestFLACRoundTripAndCorruptionDetection(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	cases := []struct {
		name       string
		numSamples int
		genSample  func(i int) int16
	}{
		{
			name:       "1 sample",
			numSamples: 1,
			genSample:  func(i int) int16 { return 1234 },
		},
		{
			name:       "silence (constant subframe)",
			numSamples: 10000,
			genSample:  func(i int) int16 { return 0 },
		},
		{
			name:       "sine wave 440Hz (non-4096 multiple)",
			numSamples: 9999,
			genSample: func(i int) int16 {
				return int16(math.Sin(2*math.Pi*440*float64(i)/44100.0) * 28000)
			},
		},
		{
			name:       "full scale extremes (-32768 / +32767)",
			numSamples: 5000,
			genSample: func(i int) int16 {
				if i%2 == 0 {
					return -32768
				}
				return 32767
			},
		},
		{
			name:       "pseudo-random speech-like signal",
			numSamples: 44100,
			genSample: func(i int) int16 {
				base := math.Sin(2*math.Pi*220*float64(i)/44100.0) * 12000
				noise := float64(rng.Intn(400) - 200)
				return int16(base + noise)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pcm := make([]byte, tc.numSamples*2)
			for i := 0; i < tc.numSamples; i++ {
				binary.LittleEndian.PutUint16(pcm[i*2:i*2+2], uint16(tc.genSample(i)))
			}
			wav := makeTestWAV(pcm, 44100, 1)

			flacBytes, err := ConvertWAVToFLACVerified(wav)
			if err != nil {
				t.Fatalf("ConvertWAVToFLACVerified failed: %v", err)
			}

			decodedPCM, info, err := DecodeFLACToPCM16(flacBytes)
			if err != nil {
				t.Fatalf("DecodeFLACToPCM16 failed: %v", err)
			}
			if !bytes.Equal(pcm, decodedPCM) {
				t.Fatalf("decoded PCM does not match original PCM")
			}
			if int(info.NSamples) != tc.numSamples {
				t.Fatalf("expected %d samples, got %d", tc.numSamples, info.NSamples)
			}

			// Corrupting MD5 in STREAMINFO (offset 26..42) must fail VerifyFLAC
			corruptedMD5 := append([]byte(nil), flacBytes...)
			corruptedMD5[30] ^= 0xFF
			if _, err := VerifyFLAC(corruptedMD5); err == nil {
				t.Fatalf("expected VerifyFLAC to reject corrupted MD5")
			}

			// Corrupting frame payload near the end must fail VerifyFLAC
			corruptedFrame := append([]byte(nil), flacBytes...)
			corruptedFrame[len(corruptedFrame)-2] ^= 0xFF
			if _, err := VerifyFLAC(corruptedFrame); err == nil {
				t.Fatalf("expected VerifyFLAC to reject corrupted frame CRC/data")
			}
		})
	}
}
