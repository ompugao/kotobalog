import 'dart:typed_data';

/// 16-bitモノラルまたはステレオPCMデータにRIFF WAVヘッダを付与するユーティリティ
Uint8List pcm16ToWav(
  Uint8List pcmBytes, {
  int sampleRate = 44100,
  int numChannels = 1,
}) {
  final int byteRate = sampleRate * numChannels * 2;
  final int blockAlign = numChannels * 2;
  final int dataSize = pcmBytes.length;
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
  out.setRange(44, 44 + dataSize, pcmBytes);
  return out;
}
