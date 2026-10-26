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

  group('WAV Encoder tests', () {
    test('prepends valid 44-byte RIFF WAVE header to PCM data', () {
      final pcm = Uint8List.fromList([0, 1, 2, 3, 4, 5, 6, 7]);
      final wav = pcm16ToWav(pcm, sampleRate: 44100, numChannels: 1);

      expect(wav.length, 44 + 8);
      expect(String.fromCharCodes(wav.sublist(0, 4)), 'RIFF');
      expect(String.fromCharCodes(wav.sublist(8, 12)), 'WAVE');
      expect(String.fromCharCodes(wav.sublist(36, 40)), 'data');
    });
  });
}
