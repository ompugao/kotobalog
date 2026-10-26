import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../models/notebook.dart';
import '../providers/app_state.dart';

class NotebookSwitcherSheet extends ConsumerWidget {
  const NotebookSwitcherSheet({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
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
                      '${nb.birthDate} 生まれ ($ageNow)',
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
                      await ref
                          .read(appProvider.notifier)
                          .selectNotebook(nb.id);
                      if (context.mounted) Navigator.of(context).pop();
                    },
                  ),
                );
              }),
              const SizedBox(height: 12),
              FilledButton.tonalIcon(
                onPressed: () {
                  showDialog(
                    context: context,
                    builder: (_) => const NotebookFormDialog(),
                  );
                },
                icon: const Icon(Icons.add),
                label: const Text('新しいことば帳を作る'),
              ),
              if (currentNb != null && currentNb.members.isNotEmpty) ...[
                const SizedBox(height: 20),
                const Divider(),
                const SizedBox(height: 8),
                const Text(
                  '家族メンバー (ALLOWLIST 共有中)',
                  style: TextStyle(
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

  static const _emojis = [
    '👶',
    '🧒',
    '👧',
    '👦',
    '🦖',
    '🐰',
    '🐻',
    '🐣',
    '🌸',
    '🚀'
  ];
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
    _birthDate =
        ex?.parsedBirthDate ?? DateTime.now().subtract(const Duration(days: 365));
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

class EditDisplayNameDialog extends ConsumerStatefulWidget {
  final String currentName;
  const EditDisplayNameDialog({super.key, required this.currentName});

  @override
  ConsumerState<EditDisplayNameDialog> createState() =>
      _EditDisplayNameDialogState();
}

class _EditDisplayNameDialogState extends ConsumerState<EditDisplayNameDialog> {
  late final TextEditingController _ctrl;
  bool _saving = false;

  @override
  void initState() {
    super.initState();
    _ctrl = TextEditingController(text: widget.currentName);
  }

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final name = _ctrl.text.trim();
    if (name.isEmpty) return;
    setState(() => _saving = true);
    try {
      await ref.read(appProvider.notifier).updateDisplayName(name);
      if (mounted) Navigator.of(context).pop();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(e.toString())),
        );
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('表示名の変更'),
      content: TextField(
        controller: _ctrl,
        decoration: const InputDecoration(
          labelText: '記録に表示する名前（例: パパ、ママ）',
          border: OutlineInputBorder(),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('キャンセル'),
        ),
        FilledButton(
          onPressed: _saving ? null : _save,
          child: const Text('保存する'),
        ),
      ],
    );
  }
}
