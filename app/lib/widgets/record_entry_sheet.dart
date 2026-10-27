import 'dart:async';
import 'dart:math' as math;
import 'dart:typed_data';
import 'package:audioplayers/audioplayers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import 'package:record/record.dart';
import '../models/word_entry.dart';
import '../providers/app_state.dart';
import '../services/wav_encoder.dart';

class RecordEntrySheet extends ConsumerStatefulWidget {
  final WordEntry? existingEntry;

  const RecordEntrySheet({
    super.key,
    this.existingEntry,
  });

  @override
  ConsumerState<RecordEntrySheet> createState() => _RecordEntrySheetState();
}

class _RecordEntrySheetState extends ConsumerState<RecordEntrySheet> {
  static const int _sampleRate = 44100;
  static const int _numChannels = 1;

  late final TextEditingController _wordCtrl;
  late final TextEditingController _meaningCtrl;
  late final FocusNode _wordFocusNode;
  late final FocusNode _meaningFocusNode;
  late DateTime _spokenAt;

  final AudioRecorder _recorder = AudioRecorder();
  AudioPlayer? _previewPlayer;
  StreamSubscription<Uint8List>? _streamSub;
  Timer? _timer;

  bool _isRecording = false;
  bool _isStartingRecorder = false;
  bool _isPlayingPreview = false;
  bool _isSaving = false;

  int _elapsedMs = 0;
  double _liveLevel = 0.1;
  final List<int> _pcmBuffer = [];

  // 録音完了後のPCMデータと切り出し範囲（ミリ秒）
  Uint8List? _recordedPcm;
  List<double> _waveformPeaks = const [];
  int _totalDurationMs = 0;
  int _trimStartMs = 0;
  int _trimEndMs = 0;

  // 既存エントリ編集時の既存音声状態
  bool _keepExistingAudio = false;

  bool get _isEditing => widget.existingEntry != null;

  @override
  void initState() {
    super.initState();
    final existing = widget.existingEntry;
    _wordCtrl = TextEditingController(text: existing?.word ?? '');
    _meaningCtrl = TextEditingController(text: existing?.meaning ?? '');
    _wordFocusNode = FocusNode();
    _meaningFocusNode = FocusNode();
    _spokenAt = existing?.spokenAt.toLocal() ?? DateTime.now();
    _keepExistingAudio = existing?.hasAudio ?? false;

    if (!_isEditing) {
      // 新規記録時はシートが開いた瞬間に録音を開始する
      _isStartingRecorder = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        _startRecording(autoStart: true);
      });
    }
  }

  @override
  void dispose() {
    _timer?.cancel();
    _streamSub?.cancel();
    _recorder.dispose();
    _previewPlayer?.dispose();
    _wordCtrl.dispose();
    _meaningCtrl.dispose();
    _wordFocusNode.dispose();
    _meaningFocusNode.dispose();
    super.dispose();
  }

  Future<void> _startRecording({bool autoStart = false}) async {
    await _stopPreview();
    setState(() {
      _isStartingRecorder = true;
    });

    try {
      final hasPerm = await _recorder.hasPermission();
      if (!hasPerm) {
        if (mounted) {
          setState(() {
            _isStartingRecorder = false;
            _isRecording = false;
          });
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(
              content: Text('マイク権限がないため、文字での記録画面へ進みました'),
            ),
          );
          _wordFocusNode.requestFocus();
        }
        return;
      }

      _pcmBuffer.clear();
      final stream = await _recorder.startStream(
        const RecordConfig(
          encoder: AudioEncoder.pcm16bits,
          sampleRate: _sampleRate,
          numChannels: _numChannels,
        ),
      );

      _streamSub = stream.listen((chunk) {
        _pcmBuffer.addAll(chunk);
        if (chunk.length >= 2 && mounted) {
          final bd = ByteData.sublistView(chunk, 0, chunk.length - (chunk.length % 2));
          int peak = 0;
          final step = math.max(1, (chunk.length ~/ 2) ~/ 16);
          for (int i = 0; i < chunk.length ~/ 2; i += step) {
            final v = bd.getInt16(i * 2, Endian.little).abs();
            if (v > peak) peak = v;
          }
          final norm = (0.1 + 0.9 * (peak / 16384.0)).clamp(0.1, 1.0);
          setState(() {
            _liveLevel = norm;
          });
        }
      });

      if (!mounted) return;
      setState(() {
        _isStartingRecorder = false;
        _isRecording = true;
        _elapsedMs = 0;
        _recordedPcm = null;
        _waveformPeaks = const [];
        _totalDurationMs = 0;
        _trimStartMs = 0;
        _trimEndMs = 0;
        _keepExistingAudio = false;
      });

      _timer?.cancel();
      _timer = Timer.periodic(const Duration(milliseconds: 100), (_) {
        if (mounted) {
          setState(() => _elapsedMs += 100);
        }
      });
    } catch (e) {
      if (mounted) {
        setState(() {
          _isStartingRecorder = false;
          _isRecording = false;
        });
        if (!autoStart) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text('録音を開始できませんでした: $e')),
          );
        }
        _wordFocusNode.requestFocus();
      }
    }
  }

  Future<void> _finishRecording({bool discard = false}) async {
    _timer?.cancel();
    await _streamSub?.cancel();
    _streamSub = null;
    try {
      await _recorder.stop();
    } catch (_) {}

    if (discard || _pcmBuffer.isEmpty) {
      _pcmBuffer.clear();
      if (mounted) {
        setState(() {
          _isRecording = false;
          _isStartingRecorder = false;
          _recordedPcm = null;
          _waveformPeaks = const [];
          _totalDurationMs = 0;
          _trimStartMs = 0;
          _trimEndMs = 0;
        });
        Future.delayed(const Duration(milliseconds: 100), () {
          if (mounted) _wordFocusNode.requestFocus();
        });
      }
      return;
    }

    final rawPcm = Uint8List.fromList(_pcmBuffer);
    final durationMs = math.max(
      100,
      pcmDurationMs(rawPcm, sampleRate: _sampleRate, numChannels: _numChannels),
    );
    final peaks = extractWaveformPeaks(rawPcm, barCount: 48);

    if (mounted) {
      setState(() {
        _isRecording = false;
        _isStartingRecorder = false;
        _recordedPcm = rawPcm;
        _waveformPeaks = peaks;
        _totalDurationMs = durationMs;
        _trimStartMs = 0;
        _trimEndMs = durationMs;
        _keepExistingAudio = false;
      });
      Future.delayed(const Duration(milliseconds: 100), () {
        if (mounted && _wordCtrl.text.isEmpty) {
          _wordFocusNode.requestFocus();
        }
      });
    }
  }

  Uint8List? _buildTrimmedWavBytes() {
    final pcm = _recordedPcm;
    if (pcm == null || pcm.isEmpty || _trimEndMs <= _trimStartMs) {
      return null;
    }
    final sliced = slicePcm16(
      pcm,
      startMs: _trimStartMs,
      endMs: _trimEndMs,
      sampleRate: _sampleRate,
      numChannels: _numChannels,
    );
    if (sliced.isEmpty) return null;
    return pcm16ToWav(
      sliced,
      sampleRate: _sampleRate,
      numChannels: _numChannels,
    );
  }

  int get _trimmedDurationMs => math.max(0, _trimEndMs - _trimStartMs);

  Future<void> _stopPreview() async {
    if (_previewPlayer != null && _isPlayingPreview) {
      await _previewPlayer!.stop();
    }
    if (mounted && _isPlayingPreview) {
      setState(() => _isPlayingPreview = false);
    }
  }

  Future<void> _togglePreview() async {
    if (_previewPlayer == null) {
      _previewPlayer = AudioPlayer();
      _previewPlayer!.onPlayerComplete.listen((_) {
        if (mounted) setState(() => _isPlayingPreview = false);
      });
    }

    if (_isPlayingPreview) {
      await _stopPreview();
      return;
    }

    final wavBytes = _buildTrimmedWavBytes();
    if (wavBytes != null) {
      setState(() => _isPlayingPreview = true);
      await _previewPlayer!.play(BytesSource(wavBytes, mimeType: 'audio/wav'));
      return;
    }

    if (_keepExistingAudio && widget.existingEntry != null) {
      final nb = ref.read(appProvider).currentNotebook;
      if (nb == null) return;
      final url = ref
          .read(appProvider.notifier)
          .api
          .audioUrl(nb.id, widget.existingEntry!.id);
      setState(() => _isPlayingPreview = true);
      await _previewPlayer!.play(UrlSource(url));
    }
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

  String _formatMs(int ms) {
    final sec = ms / 1000.0;
    return '${sec.toStringAsFixed(1)}秒';
  }

  Future<void> _save() async {
    final word = _wordCtrl.text.trim();
    if (word.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('お子さまが話した「ことば」を入力してください')),
      );
      _wordFocusNode.requestFocus();
      return;
    }

    if (_isRecording) {
      await _finishRecording();
    }
    await _stopPreview();

    final nb = ref.read(appProvider).currentNotebook;
    if (nb == null) return;

    final trimmedWav = _buildTrimmedWavBytes();
    final trimmedMs = trimmedWav != null ? _trimmedDurationMs : 0;

    setState(() => _isSaving = true);
    try {
      final notifier = ref.read(appProvider.notifier);
      if (_isEditing) {
        final existing = widget.existingEntry!;
        final removeAudio =
            existing.hasAudio && !_keepExistingAudio && trimmedWav == null;
        await notifier.updateEntry(
          entryId: existing.id,
          word: word,
          meaning: _meaningCtrl.text.trim(),
          spokenAt: _spokenAt,
          audioBytes: trimmedWav,
          audioMime: 'audio/wav',
          audioDurationMs: trimmedMs,
          removeAudio: removeAudio,
        );
      } else {
        await notifier.api.createEntry(
          notebookId: nb.id,
          word: word,
          meaning: _meaningCtrl.text.trim(),
          spokenAt: _spokenAt,
          audioBytes: trimmedWav,
          audioMime: 'audio/wav',
          audioDurationMs: trimmedMs,
        );
        await notifier.refreshEntries();
      }
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

  Widget _buildRecordingPhase(BuildContext context, Color themeColor, String childName) {
    final secStr = (_elapsedMs / 1000.0).toStringAsFixed(1);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            Row(
              children: [
                Container(
                  width: 10,
                  height: 10,
                  decoration: const BoxDecoration(
                    color: Colors.red,
                    shape: BoxShape.circle,
                  ),
                ),
                const SizedBox(width: 8),
                Text(
                  '$childNameの声を録音中...',
                  style: const TextStyle(
                    fontSize: 18,
                    fontWeight: FontWeight.bold,
                  ),
                ),
              ],
            ),
            IconButton(
              icon: const Icon(Icons.close),
              onPressed: () => Navigator.of(context).pop(),
            ),
          ],
        ),
        const SizedBox(height: 8),
        Text(
          '話し終わったら「録音を終了して次へ」を押してください。\nあとから必要な部分だけを切り出したり、ことばやメモを入力できます。',
          style: TextStyle(
            fontSize: 13,
            color: Colors.brown.shade600,
          ),
        ),
        const SizedBox(height: 24),
        Center(
          child: AnimatedContainer(
            duration: const Duration(milliseconds: 90),
            width: 96 + (_liveLevel * 28),
            height: 96 + (_liveLevel * 28),
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              color: Colors.red.withValues(alpha: 0.14),
              border: Border.all(
                color: Colors.red.withValues(alpha: 0.45),
                width: 3,
              ),
            ),
            child: Center(
              child: Container(
                width: 72,
                height: 72,
                decoration: const BoxDecoration(
                  shape: BoxShape.circle,
                  color: Colors.red,
                ),
                child: const Icon(
                  Icons.mic,
                  color: Colors.white,
                  size: 36,
                ),
              ),
            ),
          ),
        ),
        const SizedBox(height: 16),
        Center(
          child: Text(
            '$secStr 秒',
            style: TextStyle(
              fontSize: 28,
              fontWeight: FontWeight.bold,
              color: Colors.red.shade700,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
        ),
        const SizedBox(height: 24),
        FilledButton.icon(
          style: FilledButton.styleFrom(
            backgroundColor: Colors.red.shade600,
            foregroundColor: Colors.white,
            padding: const EdgeInsets.symmetric(vertical: 16),
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(16),
            ),
          ),
          onPressed: () => _finishRecording(discard: false),
          icon: const Icon(Icons.stop_circle_outlined, size: 24),
          label: const Text(
            '録音を終了して次へ（切り出し・文字入力）',
            style: TextStyle(fontSize: 16, fontWeight: FontWeight.bold),
          ),
        ),
        const SizedBox(height: 8),
        TextButton.icon(
          onPressed: () => _finishRecording(discard: true),
          icon: const Icon(Icons.edit_note),
          label: const Text('音声なしで文字だけ記録する'),
        ),
      ],
    );
  }

  Widget _buildAudioTrimmerSection(BuildContext context, Color themeColor) {
    if (_recordedPcm != null && _totalDurationMs > 0) {
      final double maxMs = math.max(100, _totalDurationMs).toDouble();
      final double startVal = _trimStartMs.clamp(0, _totalDurationMs).toDouble();
      final double endVal =
          _trimEndMs.clamp(_trimStartMs, _totalDurationMs).toDouble();

      return Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: themeColor.withValues(alpha: 0.1),
          borderRadius: BorderRadius.circular(16),
          border: Border.all(color: themeColor.withValues(alpha: 0.35)),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Row(
                  children: [
                    Icon(Icons.content_cut, size: 18, color: Colors.brown.shade800),
                    const SizedBox(width: 6),
                    Text(
                      '音声の切り出し (${_formatMs(_trimmedDurationMs)})',
                      style: TextStyle(
                        fontWeight: FontWeight.bold,
                        color: Colors.brown.shade900,
                      ),
                    ),
                  ],
                ),
                Text(
                  '${_formatMs(_trimStartMs)} 〜 ${_formatMs(_trimEndMs)}',
                  style: TextStyle(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    color: Colors.brown.shade700,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            // 波形バー表示（選択範囲内をハイライト）
            SizedBox(
              height: 44,
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.center,
                children: List.generate(_waveformPeaks.length, (index) {
                  final barPosMs =
                      ((index + 0.5) / _waveformPeaks.length) * _totalDurationMs;
                  final inRange =
                      barPosMs >= _trimStartMs && barPosMs <= _trimEndMs;
                  final amp = _waveformPeaks[index];
                  return Expanded(
                    child: Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 1),
                      child: Align(
                        alignment: Alignment.center,
                        child: Container(
                          height: math.max(4.0, 42.0 * amp),
                          decoration: BoxDecoration(
                            color: inRange
                                ? themeColor
                                : Colors.brown.withValues(alpha: 0.2),
                            borderRadius: BorderRadius.circular(3),
                          ),
                        ),
                      ),
                    ),
                  );
                }),
              ),
            ),
            RangeSlider(
              values: RangeValues(startVal, endVal),
              min: 0,
              max: maxMs,
              activeColor: themeColor,
              inactiveColor: Colors.brown.withValues(alpha: 0.2),
              labels: RangeLabels(
                _formatMs(_trimStartMs),
                _formatMs(_trimEndMs),
              ),
              onChanged: (values) {
                final newStart = values.start.round();
                final newEnd = math.max(newStart + 100, values.end.round());
                if (_isPlayingPreview) {
                  _stopPreview();
                }
                setState(() {
                  _trimStartMs = newStart.clamp(0, math.max(0, _totalDurationMs - 100));
                  _trimEndMs = newEnd.clamp(_trimStartMs, _totalDurationMs);
                });
              },
            ),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              alignment: WrapAlignment.center,
              children: [
                FilledButton.tonalIcon(
                  onPressed: _togglePreview,
                  icon: Icon(
                    _isPlayingPreview ? Icons.stop_rounded : Icons.play_arrow_rounded,
                  ),
                  label: Text(_isPlayingPreview ? '停止' : '切り出し範囲を試聴'),
                ),
                OutlinedButton.icon(
                  onPressed: () => _startRecording(autoStart: false),
                  icon: const Icon(Icons.fiber_manual_record, color: Colors.red, size: 16),
                  label: const Text('録り直す'),
                ),
                IconButton(
                  tooltip: '音声を添付しない',
                  onPressed: () {
                    _stopPreview();
                    setState(() {
                      _recordedPcm = null;
                      _waveformPeaks = const [];
                      _totalDurationMs = 0;
                      _trimStartMs = 0;
                      _trimEndMs = 0;
                    });
                  },
                  icon: const Icon(Icons.delete_outline),
                ),
              ],
            ),
          ],
        ),
      );
    }

    if (_keepExistingAudio && widget.existingEntry != null) {
      return Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: themeColor.withValues(alpha: 0.1),
          borderRadius: BorderRadius.circular(16),
          border: Border.all(color: themeColor.withValues(alpha: 0.3)),
        ),
        child: Wrap(
          spacing: 8,
          runSpacing: 8,
          alignment: WrapAlignment.spaceBetween,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(Icons.audiotrack, size: 18, color: Colors.brown.shade800),
                const SizedBox(width: 6),
                Text(
                  '録音済みの音声 (${_formatMs(widget.existingEntry!.audioDurationMs)})',
                  style: const TextStyle(fontWeight: FontWeight.bold),
                ),
              ],
            ),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                OutlinedButton.icon(
                  onPressed: _togglePreview,
                  icon: Icon(_isPlayingPreview ? Icons.stop : Icons.play_arrow),
                  label: Text(_isPlayingPreview ? '停止' : '再生'),
                ),
                const SizedBox(width: 6),
                OutlinedButton.icon(
                  onPressed: () async {
                    final ok = await showDialog<bool>(
                      context: context,
                      builder: (ctx) => AlertDialog(
                        title: const Text('音声を録り直しますか？'),
                        content: const Text(
                          '新しい録音を開始します。（保存するまでは元の録音済み音声は消えません）',
                        ),
                        actions: [
                          TextButton(
                            onPressed: () => Navigator.of(ctx).pop(false),
                            child: const Text('キャンセル'),
                          ),
                          FilledButton(
                            onPressed: () => Navigator.of(ctx).pop(true),
                            child: const Text('録り直す'),
                          ),
                        ],
                      ),
                    );
                    if (ok == true) {
                      await _startRecording(autoStart: false);
                    }
                  },
                  icon: const Icon(Icons.mic, color: Colors.red, size: 16),
                  label: const Text('録り直す'),
                ),
                IconButton(
                  tooltip: '音声を削除',
                  onPressed: () async {
                    bool confirmedCheck = false;
                    final ok = await showDialog<bool>(
                      context: context,
                      builder: (ctx) => StatefulBuilder(
                        builder: (ctx, setDialogState) => AlertDialog(
                          title: const Row(
                            children: [
                              Icon(Icons.warning_amber_rounded,
                                  color: Colors.red),
                              SizedBox(width: 8),
                              Flexible(child: Text('録音済みの音声を外しますか？')),
                            ],
                          ),
                          content: Column(
                            mainAxisSize: MainAxisSize.min,
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              const Text(
                                'この記録から音声を外します。このまま「変更を保存する」を押すと、録音された音声データが削除されます。',
                              ),
                              const SizedBox(height: 12),
                              CheckboxListTile(
                                contentPadding: EdgeInsets.zero,
                                controlAffinity:
                                    ListTileControlAffinity.leading,
                                value: confirmedCheck,
                                onChanged: (v) => setDialogState(
                                  () => confirmedCheck = v ?? false,
                                ),
                                title: const Text(
                                  '録音済みの音声を削除することに同意する',
                                  style: TextStyle(
                                    fontSize: 13,
                                    fontWeight: FontWeight.bold,
                                  ),
                                ),
                              ),
                            ],
                          ),
                          actions: [
                            TextButton(
                              onPressed: () => Navigator.of(ctx).pop(false),
                              child: const Text('キャンセル'),
                            ),
                            FilledButton(
                              style: FilledButton.styleFrom(
                                backgroundColor: Colors.red.shade600,
                                foregroundColor: Colors.white,
                              ),
                              onPressed: confirmedCheck
                                  ? () => Navigator.of(ctx).pop(true)
                                  : null,
                              child: const Text('音声を外す'),
                            ),
                          ],
                        ),
                      ),
                    );
                    if (ok == true) {
                      await _stopPreview();
                      if (mounted) {
                        setState(() => _keepExistingAudio = false);
                      }
                    }
                  },
                  icon: const Icon(Icons.delete_outline),
                ),
              ],
            ),
          ],
        ),
      );
    }

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      decoration: BoxDecoration(
        color: Colors.orange.shade50.withValues(alpha: 0.5),
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: Colors.orange.shade200),
      ),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [
          Row(
            children: [
              Icon(Icons.mic_none, color: Colors.brown.shade600, size: 20),
              const SizedBox(width: 8),
              Text(
                '音声なし',
                style: TextStyle(color: Colors.brown.shade700),
              ),
            ],
          ),
          FilledButton.tonalIcon(
            onPressed: () => _startRecording(autoStart: false),
            icon: const Icon(Icons.fiber_manual_record, color: Colors.red, size: 16),
            label: const Text('声を録音する'),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final nb = ref.watch(appProvider).currentNotebook;
    final ageLabel = nb?.ageLabelAt(_spokenAt) ?? '';
    final themeColor = nb?.color ?? const Color(0xFFFF8A65);
    final childName = nb?.childName ?? 'お子さま';

    return SafeArea(
      child: Padding(
        padding: EdgeInsets.only(
          left: 20,
          right: 20,
          top: 16,
          bottom: MediaQuery.of(context).viewInsets.bottom + 20,
        ),
        child: SingleChildScrollView(
          child: (_isRecording || _isStartingRecorder)
              ? _buildRecordingPhase(context, themeColor, childName)
              : Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Row(
                      mainAxisAlignment: MainAxisAlignment.spaceBetween,
                      children: [
                        Text(
                          _isEditing
                              ? '✏️ ことば・記録メモを編集'
                              : '✨ $childNameのことばを記録',
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
                    _buildAudioTrimmerSection(context, themeColor),
                    const SizedBox(height: 14),
                    TextField(
                      controller: _wordCtrl,
                      focusNode: _wordFocusNode,
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
                      focusNode: _meaningFocusNode,
                      maxLines: 3,
                      decoration: const InputDecoration(
                        labelText: '意味・そのときのエピソード（記録メモ）',
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
                    const SizedBox(height: 20),
                    FilledButton.icon(
                      onPressed: _isSaving ? null : _save,
                      style: FilledButton.styleFrom(
                        backgroundColor: themeColor,
                        foregroundColor: Colors.white,
                        padding: const EdgeInsets.symmetric(vertical: 15),
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(14),
                        ),
                      ),
                      icon: const Icon(Icons.check),
                      label: Text(
                        _isSaving
                            ? '保存しています...'
                            : (_isEditing ? '変更を保存する' : 'ことば帳に記録する'),
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
