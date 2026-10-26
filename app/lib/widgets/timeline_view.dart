import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../providers/app_state.dart';
import 'word_entry_card.dart';

class TimelineView extends ConsumerStatefulWidget {
  const TimelineView({super.key});

  @override
  ConsumerState<TimelineView> createState() => _TimelineViewState();
}

class _TimelineViewState extends ConsumerState<TimelineView> {
  final _searchCtrl = TextEditingController();

  @override
  void dispose() {
    _searchCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final appState = ref.watch(appProvider);
    final nb = appState.currentNotebook;
    if (nb == null) return const SizedBox.shrink();

    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
          child: TextField(
            controller: _searchCtrl,
            onChanged: (val) {
              ref.read(appProvider.notifier).setSearchQuery(val);
            },
            decoration: InputDecoration(
              hintText: 'ことば・意味で検索（例: ぶーぶー、救急車）',
              prefixIcon: const Icon(Icons.search),
              suffixIcon: _searchCtrl.text.isNotEmpty
                  ? IconButton(
                      icon: const Icon(Icons.clear),
                      onPressed: () {
                        _searchCtrl.clear();
                        ref.read(appProvider.notifier).setSearchQuery('');
                      },
                    )
                  : null,
              filled: true,
              fillColor: Colors.white,
              contentPadding: const EdgeInsets.symmetric(vertical: 0),
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(28),
                borderSide: BorderSide(color: Colors.brown.shade100),
              ),
              enabledBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(28),
                borderSide: BorderSide(color: Colors.brown.shade100),
              ),
            ),
          ),
        ),
        Expanded(
          child: appState.isLoadingEntries
              ? const Center(child: CircularProgressIndicator())
              : appState.entries.isEmpty
                  ? Center(
                      child: Padding(
                        padding: const EdgeInsets.all(32),
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Text(
                              nb.iconEmoji,
                              style: const TextStyle(fontSize: 52),
                            ),
                            const SizedBox(height: 12),
                            Text(
                              appState.searchQuery.isNotEmpty
                                  ? '「${appState.searchQuery}」に一致することばがありません'
                                  : 'まだ${nb.childName}のことばが記録されていません。\n右下のボタンから最初のひとことを残しましょう！',
                              textAlign: TextAlign.center,
                              style: TextStyle(
                                color: Colors.brown.shade600,
                                fontSize: 14,
                              ),
                            ),
                          ],
                        ),
                      ),
                    )
                  : RefreshIndicator(
                      onRefresh: () =>
                          ref.read(appProvider.notifier).refreshEntries(),
                      child: ListView.builder(
                        padding: const EdgeInsets.only(top: 4, bottom: 96),
                        itemCount: appState.entries.length,
                        itemBuilder: (context, index) {
                          return WordEntryCard(
                            entry: appState.entries[index],
                            notebook: nb,
                          );
                        },
                      ),
                    ),
        ),
      ],
    );
  }
}
