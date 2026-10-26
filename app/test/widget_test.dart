import 'dart:typed_data';
import 'package:flutter_test/flutter_test.dart';
import 'package:kotobalog/models/notebook.dart';
import 'package:kotobalog/services/wav_encoder.dart';

void main() {
  group('Notebook age calculation and title tests', () {
    const notebook = Notebook(
      id: 'nb-1',
      childName: 'はるくん',
      birthDate: '2025-05-10',
      themeColor: '#FF8A65',
      iconEmoji: '🦖',
      members: [],
    );

    test('title uses ことば帳 nomenclature', () {
      expect(notebook.title, 'はるくんのことば帳');
    });

    test('calculates days when under 1 month old', () {
      expect(notebook.ageLabelAt(DateTime(2025, 5, 25)), '生後15日');
    });

    test('calculates months when under 1 year old', () {
      expect(notebook.ageLabelAt(DateTime(2025, 11, 10)), '生後6ヶ月');
      expect(notebook.ageLabelAt(DateTime(2025, 11, 9)), '生後5ヶ月');
    });

    test('calculates years and months when 1 year or older', () {
      expect(notebook.ageLabelAt(DateTime(2026, 5, 10)), '1歳0ヶ月');
      expect(notebook.ageLabelAt(DateTime(2026, 10, 26)), '1歳5ヶ月');
    });
  });

  group('WAV Encoder and PCM Trimming tests', () {
    test('prepends valid 44-byte RIFF WAVE header to PCM data', () {
      final pcm = Uint8List.fromList([0, 1, 2, 3, 4, 5, 6, 7]);
      final wav = pcm16ToWav(pcm, sampleRate: 44100, numChannels: 1);

      expect(wav.length, 44 + 8);
      expect(String.fromCharCodes(wav.sublist(0, 4)), 'RIFF');
      expect(String.fromCharCodes(wav.sublist(8, 12)), 'WAVE');
      expect(String.fromCharCodes(wav.sublist(36, 40)), 'data');
    });

    test('calculates duration and slices 16-bit PCM accurately on sample boundaries', () {
      // 1 second of 16-bit mono at 1000 Hz = 2000 bytes
      final pcm = Uint8List(2000);
      for (int i = 0; i < 1000; i++) {
        // Store sample index in little-endian
        pcm[i * 2] = i & 0xFF;
        pcm[i * 2 + 1] = (i >> 8) & 0xFF;
      }

      expect(pcmDurationMs(pcm, sampleRate: 1000, numChannels: 1), 1000);

      // Slice 200ms to 700ms -> 500ms = 500 samples = 1000 bytes
      final sliced = slicePcm16(
        pcm,
        startMs: 200,
        endMs: 700,
        sampleRate: 1000,
        numChannels: 1,
      );
      expect(sliced.length, 1000);
      expect(pcmDurationMs(sliced, sampleRate: 1000, numChannels: 1), 500);
      // First sample in slice should be sample #200 (0x00C8)
      expect(sliced[0], 200);
      expect(sliced[1], 0);

      final peaks = extractWaveformPeaks(pcm, barCount: 10);
      expect(peaks.length, 10);
      expect(peaks.every((p) => p >= 0.08 && p <= 1.0), isTrue);
    });
  });
}
