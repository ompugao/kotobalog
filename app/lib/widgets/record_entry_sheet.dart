import 'dart:async';
import 'dart:typed_data';
import 'package:audioplayers/audioplayers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import 'package:record/record.dart';
import '../providers/app_state.dart';
import '../services/wav_encoder.dart';

class RecordEntrySheet extends ConsumerStatefulWidget {
  const RecordEntrySheet({super.key});

  @override
  ConsumerState<RecordEntrySheet> createState() => _RecordEntrySheetState();
}

class _RecordEntrySheetState extends ConsumerState<RecordEntrySheet> {
  final _wordCtrl = TextEditingController();
  final _meaningCtrl = TextEditingController();
  DateTime _spokenAt = DateTime.now();

  final AudioRecorder _recorder = AudioRecorder();
  AudioPlayer? _previewPlayer;
  StreamSubscription<Uint8List>? _streamSub;
  Timer? _timer;

  bool _isRecording = false;
  bool _isPlayingPreview = false;
  bool _isSaving = false;
  int _recordSeconds = 0;
  final List<int> _pcmBuffer = [];
  Uint8List? _wavBytes;

  @override
  void dispose() {
    _timer?.cancel();
    _streamSub?.cancel();
    _recorder.dispose();
    _previewPlayer?.dispose();
    _wordCtrl.dispose();
    _meaningCtrl.dispose();
    super.dispose();
  }

  Future<void> _startRecording() async {
    try {
      final hasPerm = await _recorder.hasPermission();
      if (!hasPerm) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('マイクの使用権限が許可されていません')),
          );
        }
        return;
      }

      _pcmBuffer.clear();
      final stream = await _recorder.startStream(
        const RecordConfig(
          encoder: AudioEncoder.pcm16bits,
          sampleRate: 44100,
          numChannels: 1,
        ),
      );

      _streamSub = stream.listen((chunk) {
        _pcmBuffer.addAll(chunk);
      });

      setState(() {
        _isRecording = true;
        _recordSeconds = 0;
        _wavBytes = null;
      });

      _timer = Timer.periodic(const Duration(seconds: 1), (_) {
        if (mounted) {
          setState(() => _recordSeconds++);
        }
      });
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('録音を開始できませんでした: $e')),
        );
      }
    }
  }

  Future<void> _stopRecording() async {
    _timer?.cancel();
    await _streamSub?.cancel();
    _streamSub = null;
    try {
      await _recorder.stop();
    } catch (_) {}

    Uint8List? wav;
    if (_pcmBuffer.isNotEmpty) {
      wav = pcm16ToWav(
        Uint8List.fromList(_pcmBuffer),
        sampleRate: 44100,
        numChannels: 1,
      );
    }

    if (mounted) {
      setState(() {
        _isRecording = false;
        _wavBytes = wav;
      });
    }
  }

  Future<void> _togglePreview() async {
    if (_wavBytes == null) return;
    if (_previewPlayer == null) {
      _previewPlayer = AudioPlayer();
      _previewPlayer!.onPlayerComplete.listen((_) {
        if (mounted) setState(() => _isPlayingPreview = false);
      });
    }

    if (_isPlayingPreview) {
      await _previewPlayer!.stop();
      setState(() => _isPlayingPreview = false);
      return;
    }

    setState(() => _isPlayingPreview = true);
    await _previewPlayer!.play(BytesSource(_wavBytes!, mimeType: 'audio/wav'));
  }

  Future<void> _pickDateTime() async {
    final pickedDate = await showDatePicker(
      context: context,
      initialDate: _spokenAt,
      firstDate: DateTime(2010),
      lastDate: DateTime.now().add(const Duration(days: 1)),
    );
    if (pickedDate == null || !mounted) return;

    final pickedTime = await showTimePicker(
      context: context,
      initialTime: TimeOfDay.fromDateTime(_spokenAt),
    );
    if (pickedTime == null) return;

    setState(() {
      _spokenAt = DateTime(
        pickedDate.year,
        pickedDate.month,
        pickedDate.day,
        pickedTime.hour,
        pickedTime.minute,
      );
    });
  }

  Future<void> _save() async {
    final word = _wordCtrl.text.trim();
    if (word.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('お子さまが話した「ことば」を入力してください')),
      );
      return;
    }

    if (_isRecording) {
      await _stopRecording();
    }

    final nb = ref.read(appProvider).currentNotebook;
    if (nb == null) return;

    setState(() => _isSaving = true);
    try {
      final notifier = ref.read(appProvider.notifier);
      await notifier.api.createEntry(
        notebookId: nb.id,
        word: word,
        meaning: _meaningCtrl.text.trim(),
        spokenAt: _spokenAt,
        audioBytes: _wavBytes,
        audioMime: 'audio/wav',
        audioDurationMs: _wavBytes != null ? _recordSeconds * 1000 : 0,
      );
      await notifier.refreshEntries();
      if (mounted) Navigator.of(context).pop();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(e.toString())),
        );
      }
    } finally {
      if (mounted) setState(() => _isSaving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final nb = ref.watch(appProvider).currentNotebook;
    final ageLabel = nb?.ageLabelAt(_spokenAt) ?? '';

    return SafeArea(
      child: Padding(
        padding: EdgeInsets.only(
          left: 20,
          right: 20,
          top: 16,
          bottom: MediaQuery.of(context).viewInsets.bottom + 20,
        ),
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  Text(
                    '✨ ${nb?.childName ?? 'お子さま'}のことばを記録',
                    style: const TextStyle(
                      fontSize: 18,
                      fontWeight: FontWeight.bold,
                    ),
                  ),
                  IconButton(
                    icon: const Icon(Icons.close),
                    onPressed: () => Navigator.of(context).pop(),
                  ),
                ],
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _wordCtrl,
                autofocus: true,
                decoration: const InputDecoration(
                  labelText: '話したことば *',
                  hintText: '例: ぶーぶー、まんま、とっと',
                  prefixIcon: Icon(Icons.chat_bubble_outline),
                  border: OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _meaningCtrl,
                maxLines: 2,
                decoration: const InputDecoration(
                  labelText: '意味・そのときのエピソード',
                  hintText: '例: 救急車を見て指差しながら嬉しそうに言った！',
                  prefixIcon: Icon(Icons.notes),
                  border: OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 12),
              InkWell(
                borderRadius: BorderRadius.circular(12),
                onTap: _pickDateTime,
                child: InputDecorator(
                  decoration: const InputDecoration(
                    labelText: '話した日時',
                    prefixIcon: Icon(Icons.event),
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                  child: Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      Text(DateFormat('yyyy年M月d日 HH:mm').format(_spokenAt)),
                      if (ageLabel.isNotEmpty)
                        Chip(
                          label: Text(
                            ageLabel,
                            style: const TextStyle(fontSize: 12),
                          ),
                          visualDensity: VisualDensity.compact,
                        ),
                    ],
                  ),
                ),
              ),
              const SizedBox(height: 16),
              Container(
                padding: const EdgeInsets.all(14),
                decoration: BoxDecoration(
                  color: _isRecording
                      ? Colors.red.shade50
                      : Colors.orange.shade50.withValues(alpha: 0.6),
                  borderRadius: BorderRadius.circular(16),
                  border: Border.all(
                    color: _isRecording
                        ? Colors.red.shade300
                        : Colors.orange.shade200,
                  ),
                ),
                child: Column(
                  children: [
                    Row(
                      mainAxisAlignment: MainAxisAlignment.center,
                      children: [
                        Icon(
                          _isRecording ? Icons.mic : Icons.mic_none,
                          color: _isRecording ? Colors.red : Colors.brown,
                        ),
                        const SizedBox(width: 8),
                        Text(
                          _isRecording
                              ? '録音中... $_recordSeconds 秒'
                              : (_wavBytes != null
                                  ? '録音済み ($_recordSeconds 秒)'
                                  : '声の録音（任意）'),
                          style: TextStyle(
                            fontWeight: FontWeight.bold,
                            color: _isRecording
                                ? Colors.red.shade700
                                : Colors.brown.shade800,
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 10),
                    Wrap(
                      spacing: 10,
                      alignment: WrapAlignment.center,
                      children: [
                        if (!_isRecording)
                          FilledButton.tonalIcon(
                            onPressed: _startRecording,
                            icon: const Icon(Icons.fiber_manual_record,
                                color: Colors.red),
                            label: Text(_wavBytes == null ? '録音スタート' : '録り直す'),
                          )
                        else
                          FilledButton.icon(
                            style: FilledButton.styleFrom(
                              backgroundColor: Colors.red,
                            ),
                            onPressed: _stopRecording,
                            icon: const Icon(Icons.stop),
                            label: const Text('録音を止める'),
                          ),
                        if (_wavBytes != null && !_isRecording) ...[
                          OutlinedButton.icon(
                            onPressed: _togglePreview,
                            icon: Icon(_isPlayingPreview
                                ? Icons.stop
                                : Icons.play_arrow),
                            label: Text(_isPlayingPreview ? '停止' : '聞いてみる'),
                          ),
                          IconButton(
                            tooltip: '録音を削除',
                            onPressed: () {
                              setState(() {
                                _wavBytes = null;
                                _recordSeconds = 0;
                              });
                            },
                            icon: const Icon(Icons.delete_outline),
                          ),
                        ],
                      ],
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 20),
              FilledButton.icon(
                onPressed: _isSaving ? null : _save,
                style: FilledButton.styleFrom(
                  padding: const EdgeInsets.symmetric(vertical: 15),
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(14),
                  ),
                ),
                icon: const Icon(Icons.check),
                label: Text(
                  _isSaving ? '保存しています...' : 'ことば帳に記録する',
                  style: const TextStyle(
                    fontSize: 16,
                    fontWeight: FontWeight.bold,
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
