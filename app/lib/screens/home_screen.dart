import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../providers/app_state.dart';
import '../widgets/calendar_view.dart';
import '../widgets/notebook_sheets.dart';
import '../widgets/record_entry_sheet.dart';
import '../widgets/timeline_view.dart';

class HomeScreen extends ConsumerStatefulWidget {
  const HomeScreen({super.key});

  @override
  ConsumerState<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends ConsumerState<HomeScreen> {
  int _tabIndex = 0;

  void _openNotebookSwitcher() {
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
      ),
      builder: (_) => const NotebookSwitcherSheet(),
    );
  }

  void _openRecordSheet() {
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
      ),
      builder: (_) => const RecordEntrySheet(),
    );
  }

  @override
  Widget build(BuildContext context) {
    final appState = ref.watch(appProvider);
    final nb = appState.currentNotebook;

    return Scaffold(
      backgroundColor: const Color(0xFFFFFBF7),
      appBar: AppBar(
        backgroundColor: nb != null
            ? nb.color.withValues(alpha: 0.16)
            : const Color(0xFFFFF3E0),
        elevation: 0,
        title: InkWell(
          borderRadius: BorderRadius.circular(12),
          onTap: _openNotebookSwitcher,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  nb?.iconEmoji ?? '📖',
                  style: const TextStyle(fontSize: 24),
                ),
                const SizedBox(width: 8),
                Flexible(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Flexible(
                            child: Text(
                              nb?.title ?? 'ことば帳を選択',
                              overflow: TextOverflow.ellipsis,
                              style: const TextStyle(
                                fontSize: 17,
                                fontWeight: FontWeight.bold,
                                color: Color(0xFF3E2723),
                              ),
                            ),
                          ),
                          const Icon(Icons.arrow_drop_down,
                              color: Color(0xFF3E2723)),
                        ],
                      ),
                      if (nb != null)
                        Text(
                          '現在 ${nb.ageLabelAt(DateTime.now())} • 家族 ${nb.members.length}人で共有中',
                          style: TextStyle(
                            fontSize: 11,
                            color: Colors.brown.shade700,
                          ),
                        ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        ),
        actions: [
          IconButton(
            tooltip: 'ことば帳の切り替え・家族共有',
            onPressed: _openNotebookSwitcher,
            icon: const Icon(Icons.family_restroom),
          ),
          PopupMenuButton<String>(
            tooltip: 'アカウントメニュー',
            onSelected: (value) {
              if (value == 'logout') {
                ref.read(appProvider.notifier).logout();
              }
            },
            itemBuilder: (_) => [
              PopupMenuItem(
                enabled: false,
                child: Text(
                  '${appState.user?.displayName ?? ''} (${appState.user?.email ?? ''})',
                  style: const TextStyle(fontSize: 12),
                ),
              ),
              const PopupMenuDivider(),
              const PopupMenuItem(
                value: 'logout',
                child: Row(
                  children: [
                    Icon(Icons.logout, size: 18),
                    SizedBox(width: 8),
                    Text('ログアウト'),
                  ],
                ),
              ),
            ],
          ),
        ],
      ),
      body: nb == null
          ? Center(
              child: Padding(
                padding: const EdgeInsets.all(28),
                child: ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 400),
                  child: Card(
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(24),
                    ),
                    child: Padding(
                      padding: const EdgeInsets.all(28),
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Text('🐣', style: TextStyle(fontSize: 54)),
                          const SizedBox(height: 12),
                          const Text(
                            '最初のことば帳をつくりましょう！',
                            textAlign: TextAlign.center,
                            style: TextStyle(
                              fontSize: 18,
                              fontWeight: FontWeight.bold,
                            ),
                          ),
                          const SizedBox(height: 8),
                          Text(
                            'お子さまごとに「ことば帳」を作成して、家族みんなで言葉や声を記録・共有できます。複数の兄弟姉妹がいる場合も上部のバーからいつでも切り替えられます。',
                            textAlign: TextAlign.center,
                            style: TextStyle(
                              fontSize: 13,
                              color: Colors.brown.shade600,
                            ),
                          ),
                          const SizedBox(height: 20),
                          FilledButton.icon(
                            onPressed: () {
                              showDialog(
                                context: context,
                                builder: (_) => const NotebookFormDialog(),
                              );
                            },
                            icon: const Icon(Icons.add),
                            label: const Text('新しいことば帳をつくる'),
                          ),
                          const SizedBox(height: 10),
                          OutlinedButton.icon(
                            onPressed: () {
                              showDialog(
                                context: context,
                                builder: (_) => const JoinNotebookDialog(),
                              );
                            },
                            icon: const Icon(Icons.group_add_outlined),
                            label: const Text('家族の招待コードで参加する'),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
            )
          : IndexedStack(
              index: _tabIndex,
              children: const [
                TimelineView(),
                CalendarView(),
              ],
            ),
      floatingActionButton: nb == null
          ? null
          : FloatingActionButton.extended(
              onPressed: _openRecordSheet,
              backgroundColor: nb.color,
              foregroundColor: Colors.white,
              icon: const Icon(Icons.mic),
              label: const Text(
                'ことばを記録',
                style: TextStyle(fontWeight: FontWeight.bold),
              ),
            ),
      bottomNavigationBar: nb == null
          ? null
          : NavigationBar(
              selectedIndex: _tabIndex,
              onDestinationSelected: (idx) => setState(() => _tabIndex = idx),
              destinations: const [
                NavigationDestination(
                  icon: Icon(Icons.view_agenda_outlined),
                  selectedIcon: Icon(Icons.view_agenda),
                  label: 'タイムライン',
                ),
                NavigationDestination(
                  icon: Icon(Icons.calendar_month_outlined),
                  selectedIcon: Icon(Icons.calendar_month),
                  label: 'カレンダー',
                ),
              ],
            ),
    );
  }
}
