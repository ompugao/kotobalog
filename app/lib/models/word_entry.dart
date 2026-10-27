class WordEntry {
  final String id;
  final String notebookId;
  final String createdBy;
  final String creatorName;
  final String word;
  final String meaning;
  final bool hasAudio;
  final String audioMime;
  final int audioDurationMs;
  final DateTime spokenAt;
  final DateTime createdAt;
  final DateTime? deletedAt;

  const WordEntry({
    required this.id,
    required this.notebookId,
    required this.createdBy,
    required this.creatorName,
    required this.word,
    required this.meaning,
    required this.hasAudio,
    required this.audioMime,
    required this.audioDurationMs,
    required this.spokenAt,
    required this.createdAt,
    this.deletedAt,
  });

  factory WordEntry.fromJson(Map<String, dynamic> json) {
    final rawDeletedAt = json['deleted_at'] as String?;
    return WordEntry(
      id: json['id'] as String? ?? '',
      notebookId: json['notebook_id'] as String? ?? '',
      createdBy: json['created_by'] as String? ?? '',
      creatorName: json['creator_name'] as String? ?? '家族',
      word: json['word'] as String? ?? '',
      meaning: json['meaning'] as String? ?? '',
      hasAudio: json['has_audio'] as bool? ?? false,
      audioMime: json['audio_mime'] as String? ?? '',
      audioDurationMs: json['audio_duration_ms'] as int? ?? 0,
      spokenAt: DateTime.tryParse(json['spoken_at'] as String? ?? '')?.toLocal() ??
          DateTime.now(),
      createdAt: DateTime.tryParse(json['created_at'] as String? ?? '')?.toLocal() ??
          DateTime.now(),
      deletedAt: rawDeletedAt != null
          ? DateTime.tryParse(rawDeletedAt)?.toLocal()
          : null,
    );
  }
}
