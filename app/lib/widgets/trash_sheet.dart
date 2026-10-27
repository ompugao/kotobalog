import 'package:audioplayers/audioplayers.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../models/notebook.dart';
import '../models/word_entry.dart';
import '../providers/app_state.dart';

class TrashSheet extends ConsumerStatefulWidget {
  final Notebook notebook;

  const TrashSheet({
    super.key,
    required this.notebook,
  });

  @override
  ConsumerState<TrashSheet> createState() => _TrashSheetState();
}

class _TrashSheetState extends ConsumerState<TrashSheet> {
  List<WordEntry> _trashedEntries = const [];
  bool _isLoading = true;
  String? _playingEntryId;
  AudioPlayer? _player;

  @override
  void initState() {
    super.initState();
    _loadTrash();
  }

  @override
  void dispose() {
    _player?.dispose();
    super.dispose();
  }

  Future<void> _loadTrash() async {
    setState(() => _isLoading = true);
    try {
      final items = await ref.read(appProvider.notifier).listTrashedEntries();
      if (mounted) {
        setState(() {
          _trashedEntries = items;
          _isLoading = false;
        });
      }
    } catch (_) {
      if (mounted) {
        setState(() => _isLoading = false);
      }
    }
  }

  Future<void> _toggleAudio(WordEntry entry) async {
    if (_player == null) {
      _player = AudioPlayer();
      _player!.onPlayerComplete.listen((_) {
        if (mounted) {
          setState(() => _playingEntryId = null);
        }
      });
    }

    if (_playingEntryId == entry.id) {
      await _player!.stop();
      setState(() => _playingEntryId = null);
      return;
    }

    final url = ref
        .read(appProvider.notifier)
        .api
        .audioUrl(widget.notebook.id, entry.id);
    setState(() => _playingEntryId = entry.id);
    try {
      await _player!.play(UrlSource(url));
    } catch (e) {
      if (mounted) {
        setState(() => _playingEntryId = null);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('音声の再生に失敗しました: $e')),
        );
      }
    }
  }

  Future<void> _restore(WordEntry entry) async {
    try {
      await ref.read(appProvider.notifier).restoreEntry(entry.id);
      if (mounted) {
        setState(() {
          _trashedEntries =
              _trashedEntries.where((e) => e.id != entry.id).toList();
        });
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('「${entry.word}」をことば帳に復元しました')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('復元に失敗しました: $e')),
        );
      }
    }
  }

  Future<void> _confirmPermanentDelete(WordEntry entry) async {
    bool confirmedCheck = false;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setDialogState) => AlertDialog(
          title: const Row(
            children: [
              Icon(Icons.warning_amber_rounded, color: Colors.red),
              SizedBox(width: 8),
              Flexible(child: Text('完全に削除しますか？')),
            ],
          ),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                '「${entry.word}」の記録と音声を完全に削除します。\nこの操作は取り消せず、二度と復元できません。',
              ),
              const SizedBox(height: 12),
              CheckboxListTile(
                contentPadding: EdgeInsets.zero,
                controlAffinity: ListTileControlAffinity.leading,
                value: confirmedCheck,
                onChanged: (v) =>
                    setDialogState(() => confirmedCheck = v ?? false),
                title: const Text(
                  '二度と復元できないことを理解しました',
                  style: TextStyle(fontSize: 13, fontWeight: FontWeight.bold),
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
                backgroundColor: Colors.red.shade700,
                foregroundColor: Colors.white,
              ),
              onPressed: confirmedCheck
                  ? () => Navigator.of(ctx).pop(true)
                  : null,
              child: const Text('完全に削除する'),
            ),
          ],
        ),
      ),
    );

    if (ok == true) {
      try {
        await ref
            .read(appProvider.notifier)
            .deleteEntry(entry.id, permanent: true);
        if (mounted) {
          setState(() {
            _trashedEntries =
                _trashedEntries.where((e) => e.id != entry.id).toList();
          });
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text('「${entry.word}」を完全に削除しました')),
          );
        }
      } catch (e) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text('削除に失敗しました: $e')),
          );
        }
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final nb = widget.notebook;
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 16, 20, 20),
        child: ConstrainedBox(
          constraints: BoxConstraints(
            maxHeight: MediaQuery.of(context).size.height * 0.78,
          ),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  Row(
                    children: [
                      Icon(Icons.restore_from_trash,
                          color: Colors.brown.shade800),
                      const SizedBox(width: 8),
                      Text(
                        '${nb.title} のゴミ箱',
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
              Text(
                '削除したことばはここに保管されます。いつでも元のタイムラインに復元できます。',
                style: TextStyle(fontSize: 12, color: Colors.brown.shade600),
              ),
              const SizedBox(height: 12),
              if (_isLoading)
                const Padding(
                  padding: EdgeInsets.all(32),
                  child: Center(child: CircularProgressIndicator()),
                )
              else if (_trashedEntries.isEmpty)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 40),
                  child: Column(
                    children: [
                      Icon(
                        Icons.delete_outline,
                        size: 48,
                        color: Colors.brown.shade300,
                      ),
                      const SizedBox(height: 8),
                      Text(
                        'ゴミ箱は空です',
                        style: TextStyle(
                          color: Colors.brown.shade500,
                          fontWeight: FontWeight.bold,
                        ),
                      ),
                    ],
                  ),
                )
              else
                Flexible(
                  child: ListView.separated(
                    shrinkWrap: true,
                    itemCount: _trashedEntries.length,
                    separatorBuilder: (_, _) => const SizedBox(height: 8),
                    itemBuilder: (context, idx) {
                      final entry = _trashedEntries[idx];
                      final spokenStr = DateFormat('yyyy/MM/dd HH:mm')
                          .format(entry.spokenAt.toLocal());
                      final isPlaying = _playingEntryId == entry.id;
                      return Card(
                        elevation: 0,
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(14),
                          side: BorderSide(color: Colors.brown.shade200),
                        ),
                        child: Padding(
                          padding: const EdgeInsets.all(12),
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Row(
                                children: [
                                  Expanded(
                                    child: Text(
                                      '「${entry.word}」',
                                      style: const TextStyle(
                                        fontSize: 17,
                                        fontWeight: FontWeight.bold,
                                      ),
                                    ),
                                  ),
                                  if (entry.hasAudio)
                                    IconButton(
                                      tooltip: isPlaying ? '停止' : '音声を再生',
                                      onPressed: () => _toggleAudio(entry),
                                      icon: Icon(
                                        isPlaying
                                            ? Icons.stop_circle
                                            : Icons.volume_up_rounded,
                                        color: Colors.brown.shade700,
                                      ),
                                    ),
                                ],
                              ),
                              if (entry.meaning.isNotEmpty) ...[
                                const SizedBox(height: 4),
                                Text(
                                  entry.meaning,
                                  style: TextStyle(
                                    fontSize: 13,
                                    color: Colors.brown.shade700,
                                  ),
                                ),
                              ],
                              const SizedBox(height: 8),
                              Wrap(
                                spacing: 8,
                                runSpacing: 6,
                                alignment: WrapAlignment.spaceBetween,
                                crossAxisAlignment: WrapCrossAlignment.center,
                                children: [
                                  Text(
                                    '話した日時: $spokenStr',
                                    style: TextStyle(
                                      fontSize: 11,
                                      color: Colors.brown.shade500,
                                    ),
                                  ),
                                  Row(
                                    mainAxisSize: MainAxisSize.min,
                                    children: [
                                      FilledButton.tonalIcon(
                                        onPressed: () => _restore(entry),
                                        icon: const Icon(Icons.restore, size: 18),
                                        label: const Text('復元する'),
                                      ),
                                      const SizedBox(width: 8),
                                      TextButton.icon(
                                        style: TextButton.styleFrom(
                                          foregroundColor: Colors.red.shade700,
                                        ),
                                        onPressed: () =>
                                            _confirmPermanentDelete(entry),
                                        icon: const Icon(Icons.delete_forever,
                                            size: 18),
                                        label: const Text('完全削除'),
                                      ),
                                    ],
                                  ),
                                ],
                              ),
                            ],
                          ),
                        ),
                      );
                    },
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}
