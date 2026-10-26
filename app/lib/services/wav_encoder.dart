import 'dart:math' as math;
import 'dart:typed_data';

/// 16-bitモノラルまたはステレオPCMデータにRIFF WAVヘッダを付与するユーティリティ
Uint8List pcm16ToWav(
  Uint8List pcmBytes, {
  int sampleRate = 44100,
  int numChannels = 1,
}) {
  final int byteRate = sampleRate * numChannels * 2;
  final int blockAlign = numChannels * 2;
  final int dataSize = pcmBytes.length - (pcmBytes.length % blockAlign);
  final int fileSize = 36 + dataSize;

  final header = ByteData(44);
  // "RIFF"
  header.setUint8(0, 0x52);
  header.setUint8(1, 0x49);
  header.setUint8(2, 0x46);
  header.setUint8(3, 0x46);
  header.setUint32(4, fileSize, Endian.little);
  // "WAVE"
  header.setUint8(8, 0x57);
  header.setUint8(9, 0x41);
  header.setUint8(10, 0x56);
  header.setUint8(11, 0x45);
  // "fmt "
  header.setUint8(12, 0x66);
  header.setUint8(13, 0x6d);
  header.setUint8(14, 0x74);
  header.setUint8(15, 0x20);
  header.setUint32(16, 16, Endian.little); // Subchunk1Size (16 for PCM)
  header.setUint16(20, 1, Endian.little); // AudioFormat (1 = PCM)
  header.setUint16(22, numChannels, Endian.little);
  header.setUint32(24, sampleRate, Endian.little);
  header.setUint32(28, byteRate, Endian.little);
  header.setUint16(32, blockAlign, Endian.little);
  header.setUint16(34, 16, Endian.little); // BitsPerSample (16)
  // "data"
  header.setUint8(36, 0x64);
  header.setUint8(37, 0x61);
  header.setUint8(38, 0x74);
  header.setUint8(39, 0x61);
  header.setUint32(40, dataSize, Endian.little);

  final out = Uint8List(44 + dataSize);
  out.setRange(0, 44, header.buffer.asUint8List());
  out.setRange(44, 44 + dataSize, pcmBytes.sublist(0, dataSize));
  return out;
}

/// 16-bit PCMデータの長さからミリ秒単位の再生時間を計算する
int pcmDurationMs(
  Uint8List pcmBytes, {
  int sampleRate = 44100,
  int numChannels = 1,
}) {
  final int byteRate = sampleRate * numChannels * 2;
  if (byteRate <= 0 || pcmBytes.isEmpty) return 0;
  return (pcmBytes.length * 1000) ~/ byteRate;
}

/// 16-bit PCMデータを指定した開始〜終了ミリ秒で切り出す（サンプル境界アライン済み）
Uint8List slicePcm16(
  Uint8List pcmBytes, {
  required int startMs,
  required int endMs,
  int sampleRate = 44100,
  int numChannels = 1,
}) {
  final int blockAlign = numChannels * 2;
  final int byteRate = sampleRate * blockAlign;
  final int alignedTotal = pcmBytes.length - (pcmBytes.length % blockAlign);
  if (alignedTotal <= 0 || endMs <= startMs) {
    return Uint8List(0);
  }

  int startByte = ((startMs * byteRate) ~/ 1000);
  startByte -= startByte % blockAlign;
  startByte = startByte.clamp(0, alignedTotal);

  int endByte = ((endMs * byteRate) ~/ 1000);
  endByte -= endByte % blockAlign;
  endByte = endByte.clamp(startByte, alignedTotal);

  return Uint8List.sublistView(pcmBytes, startByte, endByte);
}

/// 16-bit PCMデータから波形表示用の正規化ピーク配列 (0.08〜1.0) を抽出する
List<double> extractWaveformPeaks(
  Uint8List pcmBytes, {
  int barCount = 48,
}) {
  if (barCount <= 0) return const [];
  final int totalSamples = pcmBytes.length ~/ 2;
  if (totalSamples == 0) {
    return List<double>.filled(barCount, 0.1);
  }

  final bd = ByteData.sublistView(pcmBytes, 0, totalSamples * 2);
  final peaks = List<double>.filled(barCount, 0.0);
  double maxPeak = 1.0;

  for (int i = 0; i < barCount; i++) {
    final int startSample = (i * totalSamples) ~/ barCount;
    final int endSample = math.max(
      startSample + 1,
      ((i + 1) * totalSamples) ~/ barCount,
    );
    // 間引きサンプリングで高速にピークを計算
    final int step = math.max(1, (endSample - startSample) ~/ 64);
    int localMax = 0;
    for (int s = startSample; s < endSample && s < totalSamples; s += step) {
      final sample = bd.getInt16(s * 2, Endian.little).abs();
      if (sample > localMax) localMax = sample;
    }
    peaks[i] = localMax.toDouble();
    if (peaks[i] > maxPeak) {
      maxPeak = peaks[i];
    }
  }

  return peaks
      .map((p) => (0.08 + 0.92 * (p / maxPeak)).clamp(0.08, 1.0))
      .toList();
}
