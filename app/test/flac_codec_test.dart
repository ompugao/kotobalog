import 'dart:io';
import 'dart:math' as math;
import 'dart:typed_data';
import 'package:flutter_test/flutter_test.dart';
import 'package:kotobalog/services/flac_codec.dart';
import 'package:kotobalog/services/wav_encoder.dart';

void main() {
  group('Pure Dart FLAC Encoder & Decoder (RFC 9639)', () {
    test('1 sample round-trip and flac -t verification', () async {
      final pcm = Uint8List(2);
      ByteData.sublistView(pcm).setInt16(0, -12345, Endian.little);

      final flacBytes = pcm16ToFlac(pcm, sampleRate: 44100, numChannels: 1);
      final decoded = parseAudioPcm16(flacBytes);
      expect(decoded, isNotNull);
      expect(decoded!.sampleRate, 44100);
      expect(decoded.numChannels, 1);
      expect(decoded.pcmBytes, equals(pcm));

      await _verifyWithSystemFlac(flacBytes, pcm, sampleRate: 44100, channels: 1);
    });

    test('Silence (CONSTANT subframe) round-trip and compression', () async {
      final pcm = Uint8List(44100 * 2 * 2); // 2 seconds of silence
      final flacBytes = pcm16ToFlac(pcm, sampleRate: 44100, numChannels: 1);
      expect(flacBytes.length, lessThan(1000));

      final decoded = parseFlacPcm16(flacBytes);
      expect(decoded, isNotNull);
      expect(decoded!.pcmBytes, equals(pcm));

      await _verifyWithSystemFlac(flacBytes, pcm, sampleRate: 44100, channels: 1);
    });

    test('440Hz sine wave (non-4096 multiple) round-trip', () async {
      const nSamples = 50000;
      final pcm = Uint8List(nSamples * 2);
      final bd = ByteData.sublistView(pcm);
      for (int i = 0; i < nSamples; i++) {
        final v = (math.sin(2 * math.pi * 440.0 * i / 44100.0) * 28000).round();
        bd.setInt16(i * 2, v, Endian.little);
      }

      final flacBytes = pcm16ToFlac(pcm, sampleRate: 44100, numChannels: 1);
      expect(flacBytes.length, lessThan(pcm.length ~/ 2));

      final decoded = parseFlacPcm16(flacBytes);
      expect(decoded, isNotNull);
      expect(decoded!.pcmBytes, equals(pcm));

      await _verifyWithSystemFlac(flacBytes, pcm, sampleRate: 44100, channels: 1);
    });

    test('Full-scale extremes (-32768 / +32767) round-trip', () async {
      const nSamples = 10000;
      final pcm = Uint8List(nSamples * 2);
      final bd = ByteData.sublistView(pcm);
      for (int i = 0; i < nSamples; i++) {
        final v = (i % 2 == 0) ? -32768 : 32767;
        bd.setInt16(i * 2, v, Endian.little);
      }

      final flacBytes = pcm16ToFlac(pcm, sampleRate: 44100, numChannels: 1);
      final decoded = parseFlacPcm16(flacBytes);
      expect(decoded, isNotNull);
      expect(decoded!.pcmBytes, equals(pcm));

      await _verifyWithSystemFlac(flacBytes, pcm, sampleRate: 44100, channels: 1);
    });

    test('Pseudo-random speech-like signal (mono & stereo) round-trip', () async {
      const nSamples = 60000;
      final pcmMono = Uint8List(nSamples * 2);
      final bdMono = ByteData.sublistView(pcmMono);
      int rng = 0x12345678;
      int cur = 0;
      for (int i = 0; i < nSamples; i++) {
        rng = (rng * 1103515245 + 12345) & 0x7fffffff;
        final noise = ((rng >> 16) % 401) - 200;
        final env = (math.sin(2 * math.pi * 3.0 * i / 44100.0) + 1.0) * 0.5;
        final tone = (math.sin(2 * math.pi * 220.0 * i / 44100.0) * 12000 * env).round();
        cur = ((cur * 7) ~/ 8 + noise).clamp(-10000, 10000);
        final sample = (tone + cur).clamp(-32768, 32767);
        bdMono.setInt16(i * 2, sample, Endian.little);
      }

      final flacMono = pcm16ToFlac(pcmMono, sampleRate: 44100, numChannels: 1);
      final decMono = parseFlacPcm16(flacMono);
      expect(decMono, isNotNull);
      expect(decMono!.pcmBytes, equals(pcmMono));
      await _verifyWithSystemFlac(flacMono, pcmMono, sampleRate: 44100, channels: 1);

      // Stereo test (correlated channels -> triggers Mid-Side decorrelation)
      final pcmStereo = Uint8List(nSamples * 4);
      final bdStereo = ByteData.sublistView(pcmStereo);
      for (int i = 0; i < nSamples; i++) {
        final l = bdMono.getInt16(i * 2, Endian.little);
        final r = (l * 3) ~/ 4;
        bdStereo.setInt16(i * 4, l, Endian.little);
        bdStereo.setInt16(i * 4 + 2, r, Endian.little);
      }
      final flacStereo = pcm16ToFlac(pcmStereo, sampleRate: 44100, numChannels: 2);
      final decStereo = parseFlacPcm16(flacStereo);
      expect(decStereo, isNotNull);
      expect(decStereo!.numChannels, 2);
      expect(decStereo.pcmBytes, equals(pcmStereo));
      await _verifyWithSystemFlac(flacStereo, pcmStereo, sampleRate: 44100, channels: 2);
    });

    test('Corrupt FLAC frame bytes are rejected by CRC-8 / CRC-16', () {
      const nSamples = 8192;
      final pcm = Uint8List(nSamples * 2);
      final bd = ByteData.sublistView(pcm);
      for (int i = 0; i < nSamples; i++) {
        bd.setInt16(i * 2, (i * 17) & 0x7FFF, Endian.little);
      }

      final flacBytes = pcm16ToFlac(pcm);
      expect(parseFlacPcm16(flacBytes), isNotNull);

      final corrupted = Uint8List.fromList(flacBytes);
      corrupted[corrupted.length ~/ 2] ^= 0xFF;
      expect(parseFlacPcm16(corrupted), isNull);
    });

    test('Real WAV files round-trip and LPC decoding compatibility', () async {
      final dir = Directory('/tmp/kl_flac');
      if (!dir.existsSync()) return;
      final wavFiles = dir
          .listSync()
          .whereType<File>()
          .where((f) => f.path.endsWith('.wav'))
          .toList();
      for (final wf in wavFiles) {
        final wavBytes = await wf.readAsBytes();
        final wavInfo = parseAudioPcm16(wavBytes);
        expect(wavInfo, isNotNull, reason: 'Failed to parse WAV ${wf.path}');

        final flacBytes = pcm16ToFlac(
          wavInfo!.pcmBytes,
          sampleRate: wavInfo.sampleRate,
          numChannels: wavInfo.numChannels,
        );
        final flacInfo = parseAudioPcm16(flacBytes);
        expect(flacInfo, isNotNull, reason: 'Failed to decode Dart FLAC for ${wf.path}');
        expect(flacInfo!.pcmBytes, equals(wavInfo.pcmBytes));

        await _verifyWithSystemFlac(
          flacBytes,
          wavInfo.pcmBytes,
          sampleRate: wavInfo.sampleRate,
          channels: wavInfo.numChannels,
        );
      }
    });
  });
}

Future<void> _verifyWithSystemFlac(
  Uint8List flacBytes,
  Uint8List originalPcm, {
  required int sampleRate,
  required int channels,
}) async {
  final flacBin = File('/usr/bin/flac');
  if (!flacBin.existsSync()) return;

  final tmpDir = await Directory.systemTemp.createTemp('dart_flac_test_');
  try {
    final flacFile = File('${tmpDir.path}/out.flac');
    await flacFile.writeAsBytes(flacBytes);

    // 1. Verify Dart-encoded FLAC with official `flac -t`
    final testRes = await Process.run('/usr/bin/flac', ['-t', '-s', flacFile.path]);
    expect(
      testRes.exitCode,
      0,
      reason: 'flac -t failed: ${testRes.stderr}',
    );

    // 2. Encode WAV with `flac -8` (LPC predictor) and verify Dart decoder decodes it bit-exactly
    final wavFile = File('${tmpDir.path}/in.wav');
    final lpcFlacFile = File('${tmpDir.path}/lpc.flac');
    await wavFile.writeAsBytes(
      pcm16ToWav(originalPcm, sampleRate: sampleRate, numChannels: channels),
    );
    final encRes = await Process.run(
      '/usr/bin/flac',
      ['-8', '-s', '-f', wavFile.path, '-o', lpcFlacFile.path],
    );
    expect(encRes.exitCode, 0, reason: 'flac -8 failed: ${encRes.stderr}');

    final lpcFlacBytes = await lpcFlacFile.readAsBytes();
    final lpcDecoded = parseFlacPcm16(lpcFlacBytes);
    expect(lpcDecoded, isNotNull, reason: 'Failed to decode flac -8 LPC output');
    expect(lpcDecoded!.pcmBytes, equals(originalPcm));
  } finally {
    await tmpDir.delete(recursive: true);
  }
}
