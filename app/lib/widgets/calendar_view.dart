import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import 'package:table_calendar/table_calendar.dart';
import '../models/word_entry.dart';
import '../providers/app_state.dart';
import 'word_entry_card.dart';

class CalendarView extends ConsumerStatefulWidget {
  const CalendarView({super.key});

  @override
  ConsumerState<CalendarView> createState() => _CalendarViewState();
}

class _CalendarViewState extends ConsumerState<CalendarView> {
  DateTime _focusedDay = DateTime.now();
  DateTime _selectedDay = DateTime.now();

  List<WordEntry> _entriesForDay(List<WordEntry> all, DateTime day) {
    return all.where((e) => isSameDay(e.spokenAt, day)).toList();
  }

  @override
  Widget build(BuildContext context) {
    final appState = ref.watch(appProvider);
    final nb = appState.currentNotebook;
    if (nb == null) return const SizedBox.shrink();

    final dayEntries = _entriesForDay(appState.entries, _selectedDay);
    final selectedAge = nb.ageLabelAt(_selectedDay);

    return Column(
      children: [
        Card(
          margin: const EdgeInsets.fromLTRB(16, 12, 16, 8),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(20),
          ),
          child: TableCalendar<WordEntry>(
            locale: 'ja_JP',
            firstDay: DateTime(2015, 1, 1),
            lastDay: DateTime.now().add(const Duration(days: 365)),
            focusedDay: _focusedDay,
            selectedDayPredicate: (day) => isSameDay(_selectedDay, day),
            calendarFormat: CalendarFormat.month,
            availableCalendarFormats: const {
              CalendarFormat.month: '月表示',
            },
            eventLoader: (day) => _entriesForDay(appState.entries, day),
            onDaySelected: (selectedDay, focusedDay) {
              setState(() {
                _selectedDay = selectedDay;
                _focusedDay = focusedDay;
              });
            },
            calendarStyle: CalendarStyle(
              todayDecoration: BoxDecoration(
                color: nb.color.withValues(alpha: 0.35),
                shape: BoxShape.circle,
              ),
              selectedDecoration: BoxDecoration(
                color: nb.color,
                shape: BoxShape.circle,
              ),
              markerDecoration: BoxDecoration(
                color: Colors.brown.shade700,
                shape: BoxShape.circle,
              ),
            ),
            headerStyle: const HeaderStyle(
              formatButtonVisible: false,
              titleCentered: true,
            ),
          ),
        ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 4),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                '${DateFormat('yyyy年M月d日').format(_selectedDay)} の記録 (${dayEntries.length}件)',
                style: const TextStyle(
                  fontWeight: FontWeight.bold,
                  fontSize: 14,
                ),
              ),
              if (selectedAge.isNotEmpty)
                Text(
                  '当時: $selectedAge',
                  style: TextStyle(
                    fontSize: 12,
                    color: Colors.brown.shade600,
                    fontWeight: FontWeight.w600,
                  ),
                ),
            ],
          ),
        ),
        Expanded(
          child: dayEntries.isEmpty
              ? Center(
                  child: Text(
                    'この日の記録はありません',
                    style: TextStyle(color: Colors.brown.shade400),
                  ),
                )
              : ListView.builder(
                  padding: const EdgeInsets.only(bottom: 96),
                  itemCount: dayEntries.length,
                  itemBuilder: (context, index) {
                    return WordEntryCard(
                      entry: dayEntries[index],
                      notebook: nb,
                    );
                  },
                ),
        ),
      ],
    );
  }
}
