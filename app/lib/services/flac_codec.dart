import 'dart:math' as math;
import 'dart:typed_data';
import 'package:crypto/crypto.dart' as crypto;
import 'wav_encoder.dart';

/// Audio (FLAC or WAV) バイナリから 16-bit PCM データとフォーマット情報を抽出する。
/// 再トリミング (`_loadRawAudioForTrimming`) などで `.flac` とレガシー `.wav` の両方を透過的に扱う。
WavPcmInfo? parseAudioPcm16(Uint8List audioBytes) {
  if (audioBytes.length < 4) return null;
  // "fLaC" (0x66, 0x4C, 0x61, 0x43)
  if (audioBytes[0] == 0x66 &&
      audioBytes[1] == 0x4C &&
      audioBytes[2] == 0x61 &&
      audioBytes[3] == 0x43) {
    return parseFlacPcm16(audioBytes);
  }
  // "RIFF" (0x52, 0x49, 0x46, 0x46)
  if (audioBytes[0] == 0x52 &&
      audioBytes[1] == 0x49 &&
      audioBytes[2] == 0x46 &&
      audioBytes[3] == 0x46) {
    return parseWavPcm16(audioBytes);
  }
  return null;
}

/// 16-bit Signed Little-Endian PCM データを RFC 9639 準拠の FLAC バイナリにエンコードする。
/// Android ネイティブと Flutter Web (dart2js) の両方で 100% 同一動作する Pure Dart 実装。
Uint8List pcm16ToFlac(
  Uint8List pcmBytes, {
  int sampleRate = 44100,
  int numChannels = 1,
}) {
  if (numChannels < 1 || numChannels > 2) {
    throw ArgumentError('numChannels must be 1 or 2 (got $numChannels)');
  }
  if (sampleRate < 1 || sampleRate > 655350) {
    throw ArgumentError('invalid sampleRate: $sampleRate');
  }

  const int maxBlockSize = 4096;
  final int blockAlign = numChannels * 2;
  final int dataSize = pcmBytes.length - (pcmBytes.length % blockAlign);
  final int totalSamples = dataSize ~/ blockAlign;
  final pcmSlice = Uint8List.sublistView(pcmBytes, 0, dataSize);

  // STREAMINFO MD5 signature (16 bytes) over raw interleaved 16-bit LE PCM bytes
  final List<int> md5Bytes = dataSize > 0
      ? crypto.md5.convert(pcmSlice).bytes
      : List<int>.filled(16, 0);

  final out = BytesBuilder(copy: false);

  // 1. Magic "fLaC"
  out.add(const [0x66, 0x4C, 0x61, 0x43]);

  // 2. Metadata block header: isLast=1 (0x80), type=0 (STREAMINFO), length=34 (0x000022)
  out.add(const [0x80, 0x00, 0x00, 0x22]);

  // 3. STREAMINFO payload (34 bytes)
  // RFC 9639: minimum and maximum block size must be >= 16.
  final streamInfo = Uint8List(34);
  final siBd = ByteData.sublistView(streamInfo);
  siBd.setUint16(0, maxBlockSize, Endian.big); // min block size (4096)
  siBd.setUint16(2, maxBlockSize, Endian.big); // max block size (4096)
  // bytes 4..9: min/max frame size = 0 (unknown)
  // bytes 10..17: sampleRate (20b) | (channels-1) (3b) | (bps-1) (5b) | totalSamples (36b)
  // Written with 32-bit safe arithmetic for dart2js compatibility:
  const int bpsMinus1 = 15; // 16-bit PCM
  streamInfo[10] = (sampleRate >> 12) & 0xFF;
  streamInfo[11] = (sampleRate >> 4) & 0xFF;
  streamInfo[12] = ((sampleRate & 0x0F) << 4) |
      (((numChannels - 1) & 0x07) << 1) |
      ((bpsMinus1 >> 4) & 0x01);
  final int totalSamplesHigh4 = (totalSamples ~/ 0x100000000) & 0x0F;
  final int totalSamplesLow32 = totalSamples % 0x100000000;
  streamInfo[13] = ((bpsMinus1 & 0x0F) << 4) | totalSamplesHigh4;
  siBd.setUint32(14, totalSamplesLow32, Endian.big);
  for (int i = 0; i < 16; i++) {
    streamInfo[18 + i] = md5Bytes[i];
  }
  out.add(streamInfo);

  if (totalSamples == 0) {
    return out.takeBytes();
  }

  // 4. Encode audio frames
  final pcmBd = ByteData.sublistView(pcmSlice);
  final chanSamples = List<Int32List>.generate(
    numChannels,
    (_) => Int32List(maxBlockSize),
  );
  final frameWriter = _FlacBitWriter();

  int frameIndex = 0;
  int sampleOffset = 0;
  while (sampleOffset < totalSamples) {
    final int blockLen = math.min(maxBlockSize, totalSamples - sampleOffset);

    for (int ch = 0; ch < numChannels; ch++) {
      final buf = chanSamples[ch];
      for (int i = 0; i < blockLen; i++) {
        final int byteIdx = ((sampleOffset + i) * numChannels + ch) * 2;
        buf[i] = pcmBd.getInt16(byteIdx, Endian.little);
      }
    }

    frameWriter.reset();
    _encodeFlacFrame(
      frameWriter,
      chanSamples: chanSamples,
      numChannels: numChannels,
      blockLen: blockLen,
      frameIndex: frameIndex,
    );
    out.add(frameWriter.toBytes());

    sampleOffset += blockLen;
    frameIndex++;
  }

  return out.takeBytes();
}

void _encodeFlacFrame(
  _FlacBitWriter bw, {
  required List<Int32List> chanSamples,
  required int numChannels,
  required int blockLen,
  required int frameIndex,
}) {
  // Frame Header
  // Sync code: 0xFFF8 (14-bit sync 0x3FFE, 1-bit reserved 0, 1-bit fixed blocksize strategy 0)
  bw.writeByte(0xFF);
  bw.writeByte(0xF8);

  // Block size bits: 1100 (12) for 4096, 0110 (6) for 8-bit (blockLen-1), 0111 (7) for 16-bit (blockLen-1)
  final int bsBits;
  if (blockLen == 4096) {
    bsBits = 0x0C;
  } else if (blockLen <= 256) {
    bsBits = 0x06;
  } else {
    bsBits = 0x07;
  }
  // Sample rate bits: 0000 (from STREAMINFO)
  bw.writeByte((bsBits << 4) | 0x00);

  // Channel assignment & sample size:
  // If stereo, check if Mid-Side or Left-Side/Right-Side is beneficial, or use independent.
  // For mono (numChannels == 1), channel assignment is 0000 (0).
  int chanAssign = numChannels - 1;
  int bps0 = 16;
  int bps1 = 16;
  Int32List ch0 = chanSamples[0];
  Int32List? ch1 = numChannels == 2 ? chanSamples[1] : null;

  if (numChannels == 2 && ch1 != null) {
    // Check if mid/side decorrelation saves bits
    final mid = Int32List(blockLen);
    final side = Int32List(blockLen);
    int indepDiffSum = 0;
    int sideDiffSum = 0;
    for (int i = 0; i < blockLen; i++) {
      final l = ch0[i];
      final r = ch1[i];
      final s = l - r;
      side[i] = s;
      mid[i] = (l + r) >> 1;
      if (i > 0) {
        indepDiffSum += (l - ch0[i - 1]).abs() + (r - ch1[i - 1]).abs();
        sideDiffSum += (mid[i] - mid[i - 1]).abs() + (s - side[i - 1]).abs();
      }
    }
    if (sideDiffSum < indepDiffSum) {
      chanAssign = 10; // Mid/Side stereo
      ch0 = mid;
      ch1 = side;
      bps1 = 17; // Side channel uses 17 bits
    }
  }

  // Sample size bits: 100 (16 bits per sample), reserved: 0 -> 0x08
  bw.writeByte((chanAssign << 4) | 0x08);

  // UTF-8 coded frame number
  _writeUtf8Int(bw, frameIndex);

  // Uncommon block size
  if (bsBits == 0x06) {
    bw.writeByte(blockLen - 1);
  } else if (bsBits == 0x07) {
    final v = blockLen - 1;
    bw.writeByte((v >> 8) & 0xFF);
    bw.writeByte(v & 0xFF);
  }

  // Frame Header CRC-8
  final int crc8 = _computeCrc8(bw.buffer, bw.byteLength);
  bw.writeByte(crc8);

  // Subframes
  _encodeSubframe(bw, ch0, blockLen, bps0);
  if (numChannels == 2 && ch1 != null) {
    _encodeSubframe(bw, ch1, blockLen, bps1);
  }

  // Align to byte boundary with zero bits
  bw.alignToByte();

  // Frame Footer CRC-16
  final int crc16 = _computeCrc16(bw.buffer, bw.byteLength);
  bw.writeByte((crc16 >> 8) & 0xFF);
  bw.writeByte(crc16 & 0xFF);
}

void _encodeSubframe(
  _FlacBitWriter bw,
  Int32List samples,
  int blockLen,
  int bps,
) {
  // 1. Check if CONSTANT subframe
  final int first = samples[0];
  bool isConstant = true;
  for (int i = 1; i < blockLen; i++) {
    if (samples[i] != first) {
      isConstant = false;
      break;
    }
  }
  if (isConstant) {
    // Subframe header: 0 (pad) | 000000 (CONSTANT) | 0 (no wasted bits) = 0x00
    bw.writeBits(0x00, 8);
    bw.writeTwosComplement(first, bps);
    return;
  }

  // 2. Evaluate FIXED predictor orders 0..4 (partition order 0, Rice1 param k in 0..14)
  final int maxOrder = math.min(4, blockLen - 1);
  int bestOrder = -1;
  int bestK = 0;
  int bestBits = 8 + blockLen * bps; // VERBATIM bit cost as baseline
  Int32List? bestZigzag;

  final sumAbsOrder = Int32List(5);
  final zigzagPerOrder = <Int32List>[];

  for (int order = 0; order <= maxOrder; order++) {
    final int n = blockLen - order;
    final zz = Int32List(n);
    int sumU = 0;
    for (int i = 0; i < n; i++) {
      final int idx = i + order;
      final int r;
      switch (order) {
        case 0:
          r = samples[idx];
          break;
        case 1:
          r = samples[idx] - samples[idx - 1];
          break;
        case 2:
          r = samples[idx] - 2 * samples[idx - 1] + samples[idx - 2];
          break;
        case 3:
          r = samples[idx] -
              3 * samples[idx - 1] +
              3 * samples[idx - 2] -
              samples[idx - 3];
          break;
        default:
          r = samples[idx] -
              4 * samples[idx - 1] +
              6 * samples[idx - 2] -
              4 * samples[idx - 3] +
              samples[idx - 4];
          break;
      }
      final int u = r >= 0 ? (r * 2) : ((-r * 2) - 1);
      zz[i] = u;
      sumU += u;
    }
    zigzagPerOrder.add(zz);
    sumAbsOrder[order] = sumU;

    // Estimate candidate k around mean(u) and test k in 0..14
    for (int k = 0; k <= 14; k++) {
      int qSum = 0;
      bool overflow = false;
      for (int i = 0; i < n; i++) {
        final int q = zz[i] >> k;
        qSum += q;
        if (q > 4096) {
          overflow = true;
          break;
        }
      }
      if (overflow) continue;
      final int bits = 8 + (order * bps) + 10 + n * (k + 1) + qSum;
      if (bits < bestBits) {
        bestBits = bits;
        bestOrder = order;
        bestK = k;
        bestZigzag = zz;
      }
    }
  }

  // 3. Fallback to VERBATIM if FIXED is not smaller
  if (bestOrder < 0 || bestZigzag == null) {
    // Subframe header: 0 (pad) | 000001 (VERBATIM) | 0 (no wasted bits) = 0x02
    bw.writeBits(0x02, 8);
    for (int i = 0; i < blockLen; i++) {
      bw.writeTwosComplement(samples[i], bps);
    }
    return;
  }

  // 4. Write FIXED subframe
  // Subframe header: 0 (pad) | (001000 | bestOrder) | 0 (no wasted bits)
  final int subframeHeader = ((0x08 | bestOrder) << 1);
  bw.writeBits(subframeHeader, 8);

  // Unencoded warm-up samples
  for (int i = 0; i < bestOrder; i++) {
    bw.writeTwosComplement(samples[i], bps);
  }

  // Residual header: 2-bit method (00 = Rice1) | 4-bit partition order (0000) | 4-bit Rice param (bestK)
  bw.writeBits(bestK, 10);

  // Rice-encoded residuals
  final int mask = (1 << bestK) - 1;
  final int n = bestZigzag.length;
  for (int i = 0; i < n; i++) {
    final int u = bestZigzag[i];
    final int q = u >> bestK;
    final int rem = u & mask;
    bw.writeZeroBits(q);
    bw.writeBits((1 << bestK) | rem, bestK + 1);
  }
}

void _writeUtf8Int(_FlacBitWriter bw, int v) {
  if (v < 0x80) {
    bw.writeByte(v);
  } else if (v < 0x800) {
    bw.writeByte(0xC0 | (v >> 6));
    bw.writeByte(0x80 | (v & 0x3F));
  } else if (v < 0x10000) {
    bw.writeByte(0xE0 | (v >> 12));
    bw.writeByte(0x80 | ((v >> 6) & 0x3F));
    bw.writeByte(0x80 | (v & 0x3F));
  } else if (v < 0x200000) {
    bw.writeByte(0xF0 | (v >> 18));
    bw.writeByte(0x80 | ((v >> 12) & 0x3F));
    bw.writeByte(0x80 | ((v >> 6) & 0x3F));
    bw.writeByte(0x80 | (v & 0x3F));
  } else {
    bw.writeByte(0xF8 | (v >> 24));
    bw.writeByte(0x80 | ((v >> 18) & 0x3F));
    bw.writeByte(0x80 | ((v >> 12) & 0x3F));
    bw.writeByte(0x80 | ((v >> 6) & 0x3F));
    bw.writeByte(0x80 | (v & 0x3F));
  }
}

/// FLAC バイナリから 16-bit Signed Little-Endian PCM データとフォーマット情報をデコードする。
/// CONSTANT / VERBATIM / FIXED (0..4) / LPC (1..32), Rice1 / Rice2, ステレオ相関解除、
/// およびフレーム CRC-8 / CRC-16 検証に対応。破損データの場合は null を返す。
WavPcmInfo? parseFlacPcm16(Uint8List flacBytes) {
  if (flacBytes.length < 42) return null;
  if (flacBytes[0] != 0x66 ||
      flacBytes[1] != 0x4C ||
      flacBytes[2] != 0x61 ||
      flacBytes[3] != 0x43) {
    return null;
  }

  int offset = 4;
  int sampleRate = 0;
  int numChannels = 0;
  int bitsPerSample = 0;
  int totalSamples = 0;
  bool hasStreamInfo = false;
  bool isLastMeta = false;

  while (!isLastMeta) {
    if (offset + 4 > flacBytes.length) return null;
    final int headerByte = flacBytes[offset];
    isLastMeta = (headerByte & 0x80) != 0;
    final int blockType = headerByte & 0x7F;
    final int blockLen = (flacBytes[offset + 1] << 16) |
        (flacBytes[offset + 2] << 8) |
        flacBytes[offset + 3];
    offset += 4;
    if (blockType == 127 || offset + blockLen > flacBytes.length) {
      return null;
    }

    if (blockType == 0) {
      // STREAMINFO
      if (blockLen != 34) return null;
      final int b10 = flacBytes[offset + 10];
      final int b11 = flacBytes[offset + 11];
      final int b12 = flacBytes[offset + 12];
      final int b13 = flacBytes[offset + 13];
      sampleRate = (b10 << 12) | (b11 << 4) | (b12 >> 4);
      numChannels = ((b12 >> 1) & 0x07) + 1;
      bitsPerSample = (((b12 & 0x01) << 4) | (b13 >> 4)) + 1;
      final int tsHigh4 = b13 & 0x0F;
      final int tsLow32 = ByteData.sublistView(flacBytes, offset + 14, offset + 18)
          .getUint32(0, Endian.big);
      totalSamples = tsHigh4 * 0x100000000 + tsLow32;
      hasStreamInfo = true;
    }
    offset += blockLen;
  }

  if (!hasStreamInfo ||
      sampleRate <= 0 ||
      numChannels < 1 ||
      numChannels > 8 ||
      bitsPerSample != 16) {
    return null;
  }

  final pcmOut = BytesBuilder(copy: false);
  final br = _FlacBitReader(flacBytes, offset);

  while (!br.isAtEnd) {
    final int frameStartByte = br.bytePosition;
    if (frameStartByte + 6 > flacBytes.length) {
      return null;
    }

    final int sync = br.readBits(14);
    if (sync != 0x3FFE) return null;
    final int reserved0 = br.readBits(1);
    if (reserved0 != 0) return null;
    br.readBits(1); // blocking strategy (0 or 1)

    final int bsBits = br.readBits(4);
    final int srBits = br.readBits(4);
    final int chanBits = br.readBits(4);
    final int bpsBits = br.readBits(3);
    final int reserved1 = br.readBits(1);
    if (reserved1 != 0 || srBits == 15 || bpsBits == 3) {
      return null;
    }

    // Read UTF-8 frame/sample number
    if (!_skipUtf8Number(br)) return null;

    // Decode block size
    int blockLen;
    if (bsBits == 0) {
      return null;
    } else if (bsBits == 1) {
      blockLen = 192;
    } else if (bsBits >= 2 && bsBits <= 5) {
      blockLen = 576 * (1 << (bsBits - 2));
    } else if (bsBits == 6) {
      blockLen = br.readBits(8) + 1;
    } else if (bsBits == 7) {
      blockLen = br.readBits(16) + 1;
    } else {
      blockLen = 256 * (1 << (bsBits - 8));
    }

    // Uncommon sample rate in frame header
    if (srBits == 12) {
      br.readBits(8);
    } else if (srBits == 13 || srBits == 14) {
      br.readBits(16);
    }

    // Verify frame header CRC-8
    final int headerEndByte = br.bytePosition;
    final int expectedCrc8 = br.readBits(8);
    final int actualCrc8 = _computeCrc8Range(
      flacBytes,
      frameStartByte,
      headerEndByte,
    );
    if (expectedCrc8 != actualCrc8) {
      return null;
    }

    final int frameChannels;
    if (chanBits < 8) {
      frameChannels = chanBits + 1;
    } else if (chanBits <= 10) {
      frameChannels = 2;
    } else {
      return null;
    }
    if (frameChannels != numChannels) {
      return null;
    }

    final chanData = List<Int32List>.generate(
      numChannels,
      (_) => Int32List(blockLen),
    );

    for (int ch = 0; ch < numChannels; ch++) {
      int subBps = bitsPerSample;
      if ((chanBits == 8 && ch == 1) ||
          (chanBits == 9 && ch == 0) ||
          (chanBits == 10 && ch == 1)) {
        subBps += 1;
      }
      if (!_decodeSubframe(br, chanData[ch], blockLen, subBps)) {
        return null;
      }
    }

    // Stereo decorrelation
    if (chanBits == 8) {
      // Left-Side
      final ch0 = chanData[0];
      final ch1 = chanData[1];
      for (int i = 0; i < blockLen; i++) {
        ch1[i] = ch0[i] - ch1[i];
      }
    } else if (chanBits == 9) {
      // Right-Side
      final ch0 = chanData[0];
      final ch1 = chanData[1];
      for (int i = 0; i < blockLen; i++) {
        ch0[i] = ch0[i] + ch1[i];
      }
    } else if (chanBits == 10) {
      // Mid-Side
      final ch0 = chanData[0];
      final ch1 = chanData[1];
      for (int i = 0; i < blockLen; i++) {
        final int side = ch1[i];
        final int mid = (ch0[i] * 2) + (side & 1);
        ch0[i] = (mid + side) >> 1;
        ch1[i] = (mid - side) >> 1;
      }
    }

    // Align to byte boundary and verify CRC-16
    if (!br.alignToByteZeroChecked()) return null;
    final int bodyEndByte = br.bytePosition;
    if (bodyEndByte + 2 > flacBytes.length) return null;
    final int expectedCrc16 = br.readBits(16);
    final int actualCrc16 = _computeCrc16Range(
      flacBytes,
      frameStartByte,
      bodyEndByte,
    );
    if (expectedCrc16 != actualCrc16) {
      return null;
    }

    // Append interleaved 16-bit LE PCM bytes
    final framePcm = Uint8List(blockLen * numChannels * 2);
    final frameBd = ByteData.sublistView(framePcm);
    if (numChannels == 1) {
      final ch0 = chanData[0];
      for (int i = 0; i < blockLen; i++) {
        frameBd.setInt16(i * 2, ch0[i], Endian.little);
      }
    } else {
      for (int i = 0; i < blockLen; i++) {
        for (int ch = 0; ch < numChannels; ch++) {
          frameBd.setInt16(
            (i * numChannels + ch) * 2,
            chanData[ch][i],
            Endian.little,
          );
        }
      }
    }
    pcmOut.add(framePcm);
  }

  final pcmBytes = pcmOut.takeBytes();
  if (totalSamples > 0 &&
      pcmBytes.length != totalSamples * numChannels * 2) {
    return null;
  }

  return WavPcmInfo(
    pcmBytes: pcmBytes,
    sampleRate: sampleRate,
    numChannels: numChannels,
    durationMs: pcmDurationMs(
      pcmBytes,
      sampleRate: sampleRate,
      numChannels: numChannels,
    ),
  );
}

bool _skipUtf8Number(_FlacBitReader br) {
  if (br.isAtEnd) return false;
  final int b0 = br.readBits(8);
  int extraBytes = 0;
  if ((b0 & 0x80) == 0) {
    extraBytes = 0;
  } else if ((b0 & 0xE0) == 0xC0) {
    extraBytes = 1;
  } else if ((b0 & 0xF0) == 0xE0) {
    extraBytes = 2;
  } else if ((b0 & 0xF8) == 0xF0) {
    extraBytes = 3;
  } else if ((b0 & 0xFC) == 0xF8) {
    extraBytes = 4;
  } else if ((b0 & 0xFE) == 0xFC) {
    extraBytes = 5;
  } else if (b0 == 0xFE) {
    extraBytes = 6;
  } else {
    return false;
  }
  for (int i = 0; i < extraBytes; i++) {
    final int bx = br.readBits(8);
    if ((bx & 0xC0) != 0x80) return false;
  }
  return !br.hasOverflowed;
}

bool _decodeSubframe(
  _FlacBitReader br,
  Int32List out,
  int blockLen,
  int bps,
) {
  final int zeroPad = br.readBits(1);
  if (zeroPad != 0) return false;
  final int typeBits = br.readBits(6);
  final int hasWasted = br.readBits(1);
  int wasted = 0;
  if (hasWasted != 0) {
    final int u = br.readUnaryZeros();
    if (u < 0) return false;
    wasted = u + 1;
  }
  final int codingBps = bps - wasted;
  if (codingBps <= 0 || codingBps > 32) return false;

  if (typeBits == 0) {
    // CONSTANT
    final int val = br.readSignedBits(codingBps);
    if (br.hasOverflowed) return false;
    for (int i = 0; i < blockLen; i++) {
      out[i] = val;
    }
  } else if (typeBits == 1) {
    // VERBATIM
    for (int i = 0; i < blockLen; i++) {
      out[i] = br.readSignedBits(codingBps);
    }
    if (br.hasOverflowed) return false;
  } else if (typeBits >= 8 && typeBits <= 12) {
    // FIXED predictor (order 0..4)
    final int order = typeBits - 8;
    if (blockLen < order) return false;
    for (int i = 0; i < order; i++) {
      out[i] = br.readSignedBits(codingBps);
    }
    if (!_decodeResiduals(br, out, blockLen, order)) return false;
    for (int i = order; i < blockLen; i++) {
      switch (order) {
        case 0:
          break;
        case 1:
          out[i] += out[i - 1];
          break;
        case 2:
          out[i] += 2 * out[i - 1] - out[i - 2];
          break;
        case 3:
          out[i] += 3 * out[i - 1] - 3 * out[i - 2] + out[i - 3];
          break;
        case 4:
          out[i] += 4 * out[i - 1] -
              6 * out[i - 2] +
              4 * out[i - 3] -
              out[i - 4];
          break;
      }
    }
  } else if (typeBits >= 32 && typeBits <= 63) {
    // LPC predictor (order 1..32)
    final int order = typeBits - 31;
    if (blockLen < order) return false;
    for (int i = 0; i < order; i++) {
      out[i] = br.readSignedBits(codingBps);
    }
    final int qlpPrecBits = br.readBits(4);
    if (qlpPrecBits == 15) return false;
    final int qlpPrec = qlpPrecBits + 1;
    final int qlpShift = br.readSignedBits(5);
    if (qlpShift < 0) return false;
    final coeffs = Int32List(order);
    for (int i = 0; i < order; i++) {
      coeffs[i] = br.readSignedBits(qlpPrec);
    }
    if (!_decodeResiduals(br, out, blockLen, order)) return false;

    // 53-bit safe arithmetic right shift for dart2js compatibility:
    final int shiftDiv = 1 << qlpShift;
    for (int i = order; i < blockLen; i++) {
      int sum = 0;
      for (int j = 0; j < order; j++) {
        sum += coeffs[j] * out[i - j - 1];
      }
      final int pred = sum >= 0
          ? (sum ~/ shiftDiv)
          : ((sum - (shiftDiv - 1)) ~/ shiftDiv);
      out[i] += pred;
    }
  } else {
    return false;
  }

  if (wasted > 0) {
    final int mul = 1 << wasted;
    for (int i = 0; i < blockLen; i++) {
      out[i] *= mul;
    }
  }
  return !br.hasOverflowed;
}

bool _decodeResiduals(
  _FlacBitReader br,
  Int32List out,
  int blockLen,
  int predOrder,
) {
  final int method = br.readBits(2);
  if (method > 1) return false;
  final int paramBits = method == 0 ? 4 : 5;
  final int escapeParam = method == 0 ? 15 : 31;
  final int partOrder = br.readBits(4);
  final int numPartitions = 1 << partOrder;
  if ((blockLen % numPartitions) != 0) return false;
  final int partSamples = blockLen >> partOrder;
  if (partSamples < predOrder) return false;

  int sampleIdx = predOrder;
  for (int p = 0; p < numPartitions; p++) {
    final int n = p == 0 ? (partSamples - predOrder) : partSamples;
    final int riceParam = br.readBits(paramBits);
    if (riceParam == escapeParam) {
      final int rawBps = br.readBits(5);
      if (rawBps == 0) {
        for (int i = 0; i < n; i++) {
          out[sampleIdx++] = 0;
        }
      } else {
        for (int i = 0; i < n; i++) {
          out[sampleIdx++] = br.readSignedBits(rawBps);
        }
      }
    } else {
      final int scale = 1 << riceParam;
      for (int i = 0; i < n; i++) {
        final int q = br.readUnaryZeros();
        if (q < 0) return false;
        final int rem = riceParam > 0 ? br.readBits(riceParam) : 0;
        final int u = q * scale + rem;
        // ZigZag decode without 32-bit sign shift overflow:
        out[sampleIdx++] = (u & 1) == 0 ? (u ~/ 2) : -((u + 1) ~/ 2);
      }
    }
    if (br.hasOverflowed) return false;
  }
  return sampleIdx == blockLen;
}

class _FlacBitWriter {
  Uint8List _buf = Uint8List(16384);
  int _bytePos = 0;
  int _bitBuf = 0;
  int _bitCount = 0;

  Uint8List get buffer => _buf;
  int get byteLength => _bytePos;

  void reset() {
    _bytePos = 0;
    _bitBuf = 0;
    _bitCount = 0;
  }

  void _ensureCapacity(int extraBytes) {
    if (_bytePos + extraBytes <= _buf.length) return;
    int newCap = _buf.length * 2;
    while (newCap < _bytePos + extraBytes) {
      newCap *= 2;
    }
    final next = Uint8List(newCap);
    next.setRange(0, _bytePos, _buf);
    _buf = next;
  }

  void writeByte(int b) {
    if (_bitCount == 0) {
      _ensureCapacity(1);
      _buf[_bytePos++] = b & 0xFF;
    } else {
      writeBits(b & 0xFF, 8);
    }
  }

  void writeBits(int val, int bits) {
    if (bits <= 0) return;
    // Keep _bitCount + bits <= 24 for dart2js 32-bit bitwise safety
    if (bits > 16) {
      final int highBits = bits - 16;
      writeBits((val ~/ 65536) & ((1 << highBits) - 1), highBits);
      writeBits(val & 0xFFFF, 16);
      return;
    }
    final int mask = (1 << bits) - 1;
    _bitBuf = ((_bitBuf << bits) | (val & mask));
    _bitCount += bits;
    _ensureCapacity(4);
    while (_bitCount >= 8) {
      _bitCount -= 8;
      _buf[_bytePos++] = (_bitBuf >> _bitCount) & 0xFF;
    }
    _bitBuf &= (1 << _bitCount) - 1;
  }

  void writeZeroBits(int count) {
    int rem = count;
    while (rem >= 16) {
      writeBits(0, 16);
      rem -= 16;
    }
    if (rem > 0) {
      writeBits(0, rem);
    }
  }

  void writeTwosComplement(int val, int bps) {
    if (val >= 0) {
      writeBits(val, bps);
    } else {
      final int mod = 1 << bps;
      writeBits(mod + val, bps);
    }
  }

  void alignToByte() {
    if (_bitCount > 0) {
      writeBits(0, 8 - _bitCount);
    }
  }

  Uint8List toBytes() {
    alignToByte();
    return Uint8List.fromList(Uint8List.sublistView(_buf, 0, _bytePos));
  }
}

class _FlacBitReader {
  final Uint8List _data;
  int _bytePos;
  int _bitBuf = 0;
  int _bitCount = 0;
  bool hasOverflowed = false;

  _FlacBitReader(this._data, this._bytePos);

  bool get isAtEnd => _bytePos >= _data.length && _bitCount == 0;

  int get bytePosition => _bytePos - (_bitCount ~/ 8);

  int readBits(int bits) {
    if (bits <= 0) return 0;
    if (bits > 16) {
      final int highBits = bits - 16;
      final int high = readBits(highBits);
      final int low = readBits(16);
      return high * 65536 + low;
    }
    while (_bitCount < bits) {
      if (_bytePos >= _data.length) {
        hasOverflowed = true;
        return 0;
      }
      _bitBuf = (_bitBuf << 8) | _data[_bytePos++];
      _bitCount += 8;
    }
    _bitCount -= bits;
    final int val = (_bitBuf >> _bitCount) & ((1 << bits) - 1);
    _bitBuf &= (1 << _bitCount) - 1;
    return val;
  }

  int readSignedBits(int bits) {
    final int u = readBits(bits);
    final int signBit = 1 << (bits - 1);
    if (u >= signBit) {
      return u - (signBit * 2);
    }
    return u;
  }

  int readUnaryZeros() {
    int zeros = 0;
    while (true) {
      if (_bitCount == 0) {
        while (_bytePos < _data.length && _data[_bytePos] == 0) {
          zeros += 8;
          _bytePos++;
          if (zeros > 65536) {
            hasOverflowed = true;
            return -1;
          }
        }
        if (_bytePos >= _data.length) {
          hasOverflowed = true;
          return -1;
        }
        _bitBuf = _data[_bytePos++];
        _bitCount = 8;
      }
      final int bit = (_bitBuf >> (_bitCount - 1)) & 1;
      _bitCount--;
      _bitBuf &= (1 << _bitCount) - 1;
      if (bit == 1) {
        return zeros;
      }
      zeros++;
      if (zeros > 65536) {
        hasOverflowed = true;
        return -1;
      }
    }
  }

  bool alignToByteZeroChecked() {
    if (_bitCount > 0) {
      final int pad = readBits(_bitCount);
      if (pad != 0) return false;
    }
    return !hasOverflowed;
  }
}

final Uint8List _crc8Table = _buildCrc8Table();
final Uint16List _crc16Table = _buildCrc16Table();

Uint8List _buildCrc8Table() {
  final table = Uint8List(256);
  for (int i = 0; i < 256; i++) {
    int crc = i;
    for (int b = 0; b < 8; b++) {
      if ((crc & 0x80) != 0) {
        crc = ((crc << 1) ^ 0x07) & 0xFF;
      } else {
        crc = (crc << 1) & 0xFF;
      }
    }
    table[i] = crc;
  }
  return table;
}

Uint16List _buildCrc16Table() {
  final table = Uint16List(256);
  for (int i = 0; i < 256; i++) {
    int crc = i << 8;
    for (int b = 0; b < 8; b++) {
      if ((crc & 0x8000) != 0) {
        crc = ((crc << 1) ^ 0x8005) & 0xFFFF;
      } else {
        crc = (crc << 1) & 0xFFFF;
      }
    }
    table[i] = crc;
  }
  return table;
}

int _computeCrc8(Uint8List data, int length) {
  return _computeCrc8Range(data, 0, length);
}

int _computeCrc8Range(Uint8List data, int start, int end) {
  int crc = 0;
  for (int i = start; i < end; i++) {
    crc = _crc8Table[(crc ^ data[i]) & 0xFF];
  }
  return crc;
}

int _computeCrc16(Uint8List data, int length) {
  return _computeCrc16Range(data, 0, length);
}

int _computeCrc16Range(Uint8List data, int start, int end) {
  int crc = 0;
  for (int i = start; i < end; i++) {
    crc = ((crc << 8) & 0xFFFF) ^ _crc16Table[((crc >> 8) ^ data[i]) & 0xFF];
  }
  return crc;
}
