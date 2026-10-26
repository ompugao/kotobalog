import 'dart:convert';
import 'dart:typed_data';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:http/http.dart' as http;
import '../models/notebook.dart';
import '../models/word_entry.dart';

class ApiException implements Exception {
  final String message;
  ApiException(this.message);

  @override
  String toString() => message;
}

class ApiClient {
  String baseUrl;
  String? token;

  ApiClient({required this.baseUrl, this.token});

  static String defaultBaseUrl() {
    if (kIsWeb) {
      final origin = Uri.base.origin;
      // Flutter web dev server port -> point to backend 8080 by default
      if (origin.contains(':8081') || origin.contains(':5000')) {
        return 'http://localhost:8080';
      }
      return origin;
    }
    return 'https://kotobalog.example.com';
  }

  Map<String, String> _headers() {
    final h = <String, String>{
      'Content-Type': 'application/json',
    };
    if (token != null && token!.isNotEmpty) {
      h['Authorization'] = 'Bearer $token';
    }
    return h;
  }

  Uri _uri(String path, [Map<String, String>? query]) {
    final cleanBase = baseUrl.endsWith('/')
        ? baseUrl.substring(0, baseUrl.length - 1)
        : baseUrl;
    return Uri.parse('$cleanBase$path').replace(queryParameters: query);
  }

  Map<String, dynamic> _decodeObj(http.Response res) {
    final body = utf8.decode(res.bodyBytes);
    final data = body.isNotEmpty ? jsonDecode(body) : <String, dynamic>{};
    if (res.statusCode >= 400) {
      final err = (data is Map && data['error'] != null)
          ? data['error'].toString()
          : 'エラーが発生しました (${res.statusCode})';
      throw ApiException(err);
    }
    return data as Map<String, dynamic>;
  }

  List<dynamic> _decodeList(http.Response res) {
    final body = utf8.decode(res.bodyBytes);
    final data = body.isNotEmpty ? jsonDecode(body) : <dynamic>[];
    if (res.statusCode >= 400) {
      final err = (data is Map && data['error'] != null)
          ? data['error'].toString()
          : 'エラーが発生しました (${res.statusCode})';
      throw ApiException(err);
    }
    return data as List<dynamic>;
  }

  Future<Map<String, bool>> getOAuthStatus() async {
    try {
      final res = await http.get(_uri('/api/health'));
      final data = _decodeObj(res);
      final oauth = data['oauth'] as Map<String, dynamic>? ?? {};
      return {
        'google': oauth['google'] == true,
        'github': oauth['github'] == true,
      };
    } catch (_) {
      return {'google': false, 'github': false};
    }
  }

  String oauthUrl(String provider, {String? redirectUri}) {
    final query = <String, String>{};
    if (redirectUri != null) {
      query['redirect_uri'] = redirectUri;
    }
    return _uri('/api/auth/oauth/$provider', query.isEmpty ? null : query)
        .toString();
  }

  Future<User> getMe() async {
    final res = await http.get(_uri('/api/auth/me'), headers: _headers());
    return User.fromJson(_decodeObj(res));
  }

  Future<User> updateDisplayName(String displayName) async {
    final res = await http.put(
      _uri('/api/auth/me'),
      headers: _headers(),
      body: jsonEncode({'display_name': displayName}),
    );
    return User.fromJson(_decodeObj(res));
  }

  Future<List<Notebook>> listNotebooks() async {
    final res = await http.get(_uri('/api/notebooks'), headers: _headers());
    return _decodeList(res)
        .map((e) => Notebook.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<Notebook> createNotebook({
    required String childName,
    required String birthDate,
    required String themeColor,
    required String iconEmoji,
  }) async {
    final res = await http.post(
      _uri('/api/notebooks'),
      headers: _headers(),
      body: jsonEncode({
        'child_name': childName,
        'birth_date': birthDate,
        'theme_color': themeColor,
        'icon_emoji': iconEmoji,
      }),
    );
    return Notebook.fromJson(_decodeObj(res));
  }

  Future<Notebook> updateNotebook({
    required String id,
    required String childName,
    required String birthDate,
    required String themeColor,
    required String iconEmoji,
  }) async {
    final res = await http.put(
      _uri('/api/notebooks/$id'),
      headers: _headers(),
      body: jsonEncode({
        'child_name': childName,
        'birth_date': birthDate,
        'theme_color': themeColor,
        'icon_emoji': iconEmoji,
      }),
    );
    return Notebook.fromJson(_decodeObj(res));
  }

  Future<List<WordEntry>> listEntries(
    String notebookId, {
    String? query,
    String? month,
  }) async {
    final params = <String, String>{};
    if (query != null && query.trim().isNotEmpty) {
      params['q'] = query.trim();
    }
    if (month != null && month.trim().isNotEmpty) {
      params['month'] = month.trim();
    }
    final res = await http.get(
      _uri('/api/notebooks/$notebookId/entries',
          params.isEmpty ? null : params),
      headers: _headers(),
    );
    return _decodeList(res)
        .map((e) => WordEntry.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<WordEntry> createEntry({
    required String notebookId,
    required String word,
    required String meaning,
    required DateTime spokenAt,
    Uint8List? audioBytes,
    String audioMime = 'audio/wav',
    int audioDurationMs = 0,
  }) async {
    final body = <String, dynamic>{
      'word': word,
      'meaning': meaning,
      'spoken_at': spokenAt.toUtc().toIso8601String(),
      'audio_duration_ms': audioDurationMs,
    };
    if (audioBytes != null && audioBytes.isNotEmpty) {
      body['audio_base64'] = base64Encode(audioBytes);
      body['audio_mime'] = audioMime;
    }
    final res = await http.post(
      _uri('/api/notebooks/$notebookId/entries'),
      headers: _headers(),
      body: jsonEncode(body),
    );
    return WordEntry.fromJson(_decodeObj(res));
  }

  Future<void> deleteEntry(String notebookId, String entryId) async {
    final res = await http.delete(
      _uri('/api/notebooks/$notebookId/entries/$entryId'),
      headers: _headers(),
    );
    _decodeObj(res);
  }

  String audioUrl(String notebookId, String entryId) {
    final query = <String, String>{};
    final isSameOriginWeb = kIsWeb && baseUrl == Uri.base.origin;
    if (!isSameOriginWeb && token != null && token!.isNotEmpty) {
      query['token'] = token!;
    }
    return _uri(
      '/api/notebooks/$notebookId/entries/$entryId/audio',
      query.isEmpty ? null : query,
    ).toString();
  }
}
