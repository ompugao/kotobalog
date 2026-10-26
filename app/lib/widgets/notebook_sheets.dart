import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../models/notebook.dart';
import '../providers/app_state.dart';

class NotebookSwitcherSheet extends ConsumerStatefulWidget {
  const NotebookSwitcherSheet({super.key});

  @override
  ConsumerState<NotebookSwitcherSheet> createState() =>
      _NotebookSwitcherSheetState();
}

class _NotebookSwitcherSheetState extends ConsumerState<NotebookSwitcherSheet> {
  String? _generatedInviteCode;
  bool _isGeneratingInvite = false;

  Future<void> _generateInvite(Notebook nb) async {
    setState(() {
      _isGeneratingInvite = true;
    });
    try {
      final code =
          await ref.read(appProvider.notifier).api.createInviteCode(nb.id);
      if (mounted) {
        setState(() {
          _generatedInviteCode = code;
        });
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(e.toString())),
        );
      }
    } finally {
      if (mounted) {
        setState(() {
          _isGeneratingInvite = false;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final appState = ref.watch(appProvider);
    final currentNb = appState.currentNotebook;

    return SafeArea(
      child: Padding(
        padding: EdgeInsets.only(
          left: 20,
          right: 20,
          top: 16,
          bottom: MediaQuery.of(context).viewInsets.bottom + 24,
        ),
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  const Text(
                    '📚 お子さまのことば帳',
                    style: TextStyle(
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
              const SizedBox(height: 8),
              ...appState.notebooks.map((nb) {
                final isSelected = nb.id == currentNb?.id;
                final ageNow = nb.ageLabelAt(DateTime.now());
                return Card(
                  elevation: isSelected ? 2 : 0,
                  color: isSelected
                      ? nb.color.withValues(alpha: 0.14)
                      : Colors.grey.shade50,
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(16),
                    side: BorderSide(
                      color: isSelected ? nb.color : Colors.grey.shade200,
                      width: isSelected ? 2 : 1,
                    ),
                  ),
                  child: ListTile(
                    leading: CircleAvatar(
                      backgroundColor: nb.color.withValues(alpha: 0.25),
                      child: Text(
                        nb.iconEmoji,
                        style: const TextStyle(fontSize: 22),
                      ),
                    ),
                    title: Text(
                      nb.title,
                      style: const TextStyle(fontWeight: FontWeight.bold),
                    ),
                    subtitle: Text(
                      '${nb.birthDate} 生まれ ($ageNow) • 家族 ${nb.members.length}人',
                      style: const TextStyle(fontSize: 12),
                    ),
                    trailing: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        IconButton(
                          tooltip: 'ことば帳の編集',
                          icon: const Icon(Icons.edit_outlined, size: 20),
                          onPressed: () {
                            showDialog(
                              context: context,
                              builder: (_) => NotebookFormDialog(existing: nb),
                            );
                          },
                        ),
                        if (isSelected)
                          Icon(Icons.check_circle, color: nb.color),
                      ],
                    ),
                    onTap: () async {
                      await ref.read(appProvider.notifier).selectNotebook(nb.id);
                      if (context.mounted) Navigator.of(context).pop();
                    },
                  ),
                );
              }),
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(
                    child: FilledButton.tonalIcon(
                      onPressed: () {
                        showDialog(
                          context: context,
                          builder: (_) => const NotebookFormDialog(),
                        );
                      },
                      icon: const Icon(Icons.add),
                      label: const Text('新しいことば帳を作る'),
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: OutlinedButton.icon(
                      onPressed: () {
                        showDialog(
                          context: context,
                          builder: (_) => const JoinNotebookDialog(),
                        );
                      },
                      icon: const Icon(Icons.group_add_outlined),
                      label: const Text('招待コードで参加'),
                    ),
                  ),
                ],
              ),
              if (currentNb != null) ...[
                const SizedBox(height: 20),
                const Divider(),
                const SizedBox(height: 8),
                Text(
                  '「${currentNb.title}」を家族と共有',
                  style: const TextStyle(
                    fontWeight: FontWeight.bold,
                    fontSize: 14,
                  ),
                ),
                const SizedBox(height: 6),
                Wrap(
                  spacing: 6,
                  runSpacing: 6,
                  children: currentNb.members
                      .map(
                        (m) => Chip(
                          avatar: const Icon(Icons.person, size: 16),
                          label: Text(
                            m.displayName,
                            style: const TextStyle(fontSize: 12),
                          ),
                          visualDensity: VisualDensity.compact,
                        ),
                      )
                      .toList(),
                ),
                const SizedBox(height: 8),
                if (_generatedInviteCode == null)
                  OutlinedButton.icon(
                    onPressed: _isGeneratingInvite
                        ? null
                        : () => _generateInvite(currentNb),
                    icon: const Icon(Icons.share_outlined, size: 18),
                    label: Text(
                      _isGeneratingInvite
                          ? '発行中...'
                          : '家族招待コードを発行する（7日間有効）',
                    ),
                  )
                else
                  Container(
                    padding: const EdgeInsets.all(12),
                    decoration: BoxDecoration(
                      color: Colors.amber.shade50,
                      borderRadius: BorderRadius.circular(12),
                      border: Border.all(color: Colors.amber.shade300),
                    ),
                    child: Row(
                      children: [
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              const Text(
                                '家族にこの招待コードを伝えてください:',
                                style: TextStyle(fontSize: 12),
                              ),
                              SelectableText(
                                _generatedInviteCode!,
                                style: const TextStyle(
                                  fontSize: 22,
                                  fontWeight: FontWeight.bold,
                                  letterSpacing: 3,
                                ),
                              ),
                            ],
                          ),
                        ),
                        IconButton(
                          tooltip: 'コピー',
                          icon: const Icon(Icons.copy),
                          onPressed: () {
                            Clipboard.setData(
                              ClipboardData(text: _generatedInviteCode!),
                            );
                            ScaffoldMessenger.of(context).showSnackBar(
                              const SnackBar(
                                content: Text('招待コードをコピーしました'),
                              ),
                            );
                          },
                        ),
                      ],
                    ),
                  ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

class NotebookFormDialog extends ConsumerStatefulWidget {
  final Notebook? existing;
  const NotebookFormDialog({super.key, this.existing});

  @override
  ConsumerState<NotebookFormDialog> createState() => _NotebookFormDialogState();
}

class _NotebookFormDialogState extends ConsumerState<NotebookFormDialog> {
  late final TextEditingController _nameCtrl;
  late DateTime _birthDate;
  late String _selectedEmoji;
  late String _selectedColor;
  bool _isSaving = false;

  static const _emojis = ['👶', '🧒', '👧', '👦', '🦖', '🐰', '🐻', '🐣', '🌸', '🚀'];
  static const _colors = [
    '#FF8A65', // Warm Coral
    '#4DB6AC', // Mint Teal
    '#64B5F6', // Sky Blue
    '#BA68C8', // Lavender
    '#FFB74D', // Sunny Orange
    '#81C784', // Soft Green
  ];

  @override
  void initState() {
    super.initState();
    final ex = widget.existing;
    _nameCtrl = TextEditingController(text: ex?.childName ?? '');
    _birthDate = ex?.parsedBirthDate ??
        DateTime.now().subtract(const Duration(days: 365));
    _selectedEmoji = ex?.iconEmoji ?? _emojis.first;
    _selectedColor = ex?.themeColor ?? _colors.first;
  }

  @override
  void dispose() {
    _nameCtrl.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final name = _nameCtrl.text.trim();
    if (name.isEmpty) return;

    setState(() => _isSaving = true);
    try {
      final birthStr = DateFormat('yyyy-MM-dd').format(_birthDate);
      final notifier = ref.read(appProvider.notifier);
      if (widget.existing == null) {
        await notifier.createNotebook(
          childName: name,
          birthDate: birthStr,
          themeColor: _selectedColor,
          iconEmoji: _selectedEmoji,
        );
      } else {
        await notifier.updateNotebook(
          id: widget.existing!.id,
          childName: name,
          birthDate: birthStr,
          themeColor: _selectedColor,
          iconEmoji: _selectedEmoji,
        );
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

  @override
  Widget build(BuildContext context) {
    final isEdit = widget.existing != null;
    return AlertDialog(
      title: Text(isEdit ? 'ことば帳の編集' : '新しいことば帳を作成'),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            TextField(
              controller: _nameCtrl,
              decoration: const InputDecoration(
                labelText: 'お子さまの名前・ニックネーム（例: はるくん）',
                border: OutlineInputBorder(),
              ),
            ),
            const SizedBox(height: 16),
            ListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('生年月日（月齢の自動計算に使います）'),
              subtitle: Text(DateFormat('yyyy年M月d日').format(_birthDate)),
              trailing: const Icon(Icons.calendar_month),
              onTap: () async {
                final picked = await showDatePicker(
                  context: context,
                  initialDate: _birthDate,
                  firstDate: DateTime(2010),
                  lastDate: DateTime.now().add(const Duration(days: 30)),
                );
                if (picked != null) {
                  setState(() => _birthDate = picked);
                }
              },
            ),
            const SizedBox(height: 12),
            const Text('アイコン', style: TextStyle(fontWeight: FontWeight.bold)),
            const SizedBox(height: 6),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: _emojis.map((emoji) {
                final selected = emoji == _selectedEmoji;
                return ChoiceChip(
                  label: Text(emoji, style: const TextStyle(fontSize: 20)),
                  selected: selected,
                  onSelected: (_) => setState(() => _selectedEmoji = emoji),
                );
              }).toList(),
            ),
            const SizedBox(height: 12),
            const Text('テーマカラー', style: TextStyle(fontWeight: FontWeight.bold)),
            const SizedBox(height: 8),
            Wrap(
              spacing: 10,
              children: _colors.map((hex) {
                final c = Color(int.parse('FF${hex.substring(1)}', radix: 16));
                final selected = hex == _selectedColor;
                return GestureDetector(
                  onTap: () => setState(() => _selectedColor = hex),
                  child: CircleAvatar(
                    radius: 18,
                    backgroundColor: c,
                    child: selected
                        ? const Icon(Icons.check, color: Colors.white, size: 20)
                        : null,
                  ),
                );
              }).toList(),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('キャンセル'),
        ),
        FilledButton(
          onPressed: _isSaving ? null : _save,
          child: Text(isEdit ? '更新する' : '作成する'),
        ),
      ],
    );
  }
}

class JoinNotebookDialog extends ConsumerStatefulWidget {
  const JoinNotebookDialog({super.key});

  @override
  ConsumerState<JoinNotebookDialog> createState() => _JoinNotebookDialogState();
}

class _JoinNotebookDialogState extends ConsumerState<JoinNotebookDialog> {
  final _codeCtrl = TextEditingController();
  bool _isJoining = false;

  @override
  void dispose() {
    _codeCtrl.dispose();
    super.dispose();
  }

  Future<void> _join() async {
    final code = _codeCtrl.text.trim();
    if (code.isEmpty) return;
    setState(() => _isJoining = true);
    try {
      await ref.read(appProvider.notifier).joinNotebook(code);
      if (mounted) Navigator.of(context).pop();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(e.toString())),
        );
      }
    } finally {
      if (mounted) setState(() => _isJoining = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('招待コードで参加'),
      content: TextField(
        controller: _codeCtrl,
        textCapitalization: TextCapitalization.characters,
        decoration: const InputDecoration(
          labelText: '8桁の招待コード',
          hintText: '例: AB12CD34',
          border: OutlineInputBorder(),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('キャンセル'),
        ),
        FilledButton(
          onPressed: _isJoining ? null : _join,
          child: const Text('参加する'),
        ),
      ],
    );
  }
}
