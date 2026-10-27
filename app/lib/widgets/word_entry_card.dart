import 'package:audioplayers/audioplayers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../models/notebook.dart';
import '../models/word_entry.dart';
import '../providers/app_state.dart';
import 'record_entry_sheet.dart';

class WordEntryCard extends ConsumerStatefulWidget {
  final WordEntry entry;
  final Notebook notebook;

  const WordEntryCard({
    super.key,
    required this.entry,
    required this.notebook,
  });

  @override
  ConsumerState<WordEntryCard> createState() => _WordEntryCardState();
}

class _WordEntryCardState extends ConsumerState<WordEntryCard> {
  AudioPlayer? _player;
  bool _isPlaying = false;

  @override
  void dispose() {
    _player?.dispose();
    super.dispose();
  }

  void _openEditSheet() {
    if (_isPlaying) {
      _player?.stop();
      setState(() => _isPlaying = false);
    }
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
      ),
      builder: (_) => RecordEntrySheet(existingEntry: widget.entry),
    );
  }

  Future<void> _toggleAudio() async {
    if (_player == null) {
      _player = AudioPlayer();
      _player!.onPlayerComplete.listen((_) {
        if (mounted) {
          setState(() => _isPlaying = false);
        }
      });
    }

    if (_isPlaying) {
      await _player!.stop();
      setState(() => _isPlaying = false);
      return;
    }

    final url = ref
        .read(appProvider.notifier)
        .api
        .audioUrl(widget.notebook.id, widget.entry.id);
    setState(() => _isPlaying = true);
    try {
      await _player!.play(UrlSource(url));
    } catch (e) {
      if (mounted) {
        setState(() => _isPlaying = false);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('音声の再生に失敗しました: $e')),
        );
      }
    }
  }

  String _formatDuration(int ms) {
    if (ms <= 0) return '音声あり';
    final sec = (ms / 1000).ceil();
    return '$sec秒';
  }

  @override
  Widget build(BuildContext context) {
    final entry = widget.entry;
    final nb = widget.notebook;
    final ageLabel = nb.ageLabelAt(entry.spokenAt);
    final dateStr = DateFormat('yyyy/MM/dd HH:mm').format(entry.spokenAt.toLocal());

    return Card(
      margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
      elevation: 1,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(18),
        side: BorderSide(color: nb.color.withValues(alpha: 0.25)),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(
                  child: InkWell(
                    borderRadius: BorderRadius.circular(8),
                    onTap: _openEditSheet,
                    child: Padding(
                      padding: const EdgeInsets.symmetric(vertical: 2),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            '「${entry.word}」',
                            style: const TextStyle(
                              fontSize: 21,
                              fontWeight: FontWeight.bold,
                              color: Color(0xFF3E2723),
                            ),
                          ),
                          if (entry.meaning.isNotEmpty) ...[
                            const SizedBox(height: 6),
                            Text(
                              entry.meaning,
                              style: TextStyle(
                                fontSize: 14,
                                color: Colors.brown.shade700,
                                height: 1.4,
                              ),
                            ),
                          ],
                        ],
                      ),
                    ),
                  ),
                ),
                if (entry.hasAudio)
                  FilledButton.tonalIcon(
                    onPressed: _toggleAudio,
                    style: FilledButton.styleFrom(
                      backgroundColor: _isPlaying
                          ? nb.color
                          : nb.color.withValues(alpha: 0.18),
                      foregroundColor:
                          _isPlaying ? Colors.white : Colors.brown.shade900,
                    ),
                    icon: Icon(
                      _isPlaying ? Icons.stop_rounded : Icons.volume_up_rounded,
                    ),
                    label: Text(
                      _isPlaying
                          ? '停止'
                          : _formatDuration(entry.audioDurationMs),
                    ),
                  ),
                IconButton(
                  tooltip: '記録メモ・ことばを編集',
                  icon: Icon(
                    Icons.edit_outlined,
                    size: 20,
                    color: Colors.brown.shade600,
                  ),
                  onPressed: _openEditSheet,
                ),
                PopupMenuButton<String>(
                  icon: const Icon(Icons.more_vert, size: 20),
                  onSelected: (val) async {
                    if (val == 'edit') {
                      _openEditSheet();
                    } else if (val == 'delete') {
                      if (_isPlaying) {
                        await _player?.stop();
                        if (mounted) setState(() => _isPlaying = false);
                      }
                      if (!context.mounted) return;
                      bool confirmedCheck = false;
                      final ok = await showDialog<bool>(
                        context: context,
                        builder: (ctx) => StatefulBuilder(
                          builder: (ctx, setDialogState) => AlertDialog(
                            title: const Row(
                              children: [
                                Icon(Icons.delete_outline, color: Colors.red),
                                SizedBox(width: 8),
                                Flexible(child: Text('記録をゴミ箱へ移動しますか？')),
                              ],
                            ),
                            content: Column(
                              mainAxisSize: MainAxisSize.min,
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(
                                  '「${entry.word}」の記録をゴミ箱へ移動します。\n万が一のときは、右上のメニューの「ゴミ箱」からいつでも復元できます。',
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
                                  title: Text(
                                    '「${entry.word}」を削除（ゴミ箱へ移動）する',
                                    style: const TextStyle(
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
                                child: const Text('ゴミ箱へ移動'),
                              ),
                            ],
                          ),
                        ),
                      );
                      if (ok == true) {
                        if (!context.mounted) return;
                        final messenger = ScaffoldMessenger.maybeOf(context);
                        final notifier = ref.read(appProvider.notifier);
                        await notifier.deleteEntry(entry.id);
                        messenger?.showSnackBar(
                          SnackBar(
                            content: Text('「${entry.word}」をゴミ箱に移動しました'),
                            action: SnackBarAction(
                              label: '元に戻す',
                              onPressed: () {
                                notifier.restoreEntry(entry.id);
                              },
                            ),
                          ),
                        );
                      }
                    }
                  },
                  itemBuilder: (_) => const [
                    PopupMenuItem(
                      value: 'edit',
                      child: Row(
                        children: [
                          Icon(Icons.edit_outlined, size: 18),
                          SizedBox(width: 8),
                          Text('記録・メモを編集'),
                        ],
                      ),
                    ),
                    PopupMenuItem(
                      value: 'delete',
                      child: Row(
                        children: [
                          Icon(Icons.delete_outline, color: Colors.red, size: 18),
                          SizedBox(width: 8),
                          Text('ゴミ箱へ移動...'),
                        ],
                      ),
                    ),
                  ],
                ),
              ],
            ),
            const SizedBox(height: 12),
            Wrap(
              spacing: 8,
              runSpacing: 6,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                if (ageLabel.isNotEmpty)
                  Container(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 10,
                      vertical: 3,
                    ),
                    decoration: BoxDecoration(
                      color: nb.color.withValues(alpha: 0.2),
                      borderRadius: BorderRadius.circular(20),
                    ),
                    child: Text(
                      '🌱 $ageLabel',
                      style: TextStyle(
                        fontSize: 12,
                        fontWeight: FontWeight.bold,
                        color: Colors.brown.shade900,
                      ),
                    ),
                  ),
                Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(
                      Icons.schedule,
                      size: 14,
                      color: Colors.brown.shade400,
                    ),
                    const SizedBox(width: 4),
                    Text(
                      dateStr,
                      style: TextStyle(
                        fontSize: 12,
                        color: Colors.brown.shade500,
                      ),
                    ),
                  ],
                ),
                Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(
                      Icons.person_outline,
                      size: 14,
                      color: Colors.brown.shade400,
                    ),
                    const SizedBox(width: 2),
                    Text(
                      entry.creatorName,
                      style: TextStyle(
                        fontSize: 12,
                        color: Colors.brown.shade500,
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
