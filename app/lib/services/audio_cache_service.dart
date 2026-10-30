import 'dart:io';
import 'package:audioplayers/audioplayers.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:http/http.dart' as http;
import 'package:path_provider/path_provider.dart';

class AudioCacheService {
  static final AudioCacheService _instance = AudioCacheService._internal();
  factory AudioCacheService() => _instance;
  AudioCacheService._internal();

  Directory? _cacheDir;

  Future<Directory?> _getCacheDirectory() async {
    if (kIsWeb) return null;
    if (_cacheDir != null) return _cacheDir;
    try {
      final baseDir = await getApplicationCacheDirectory();
      final dir = Directory('${baseDir.path}/audio_cache');
      if (!await dir.exists()) {
        await dir.create(recursive: true);
      }
      _cacheDir = dir;
      return _cacheDir;
    } catch (_) {
      return null;
    }
  }

  String _cacheFileName(String notebookId, String entryId) {
    return '${notebookId}_$entryId.audio';
  }

  /// 指定したエントリの再生用 Source を取得します。
  /// キャッシュが存在すればローカルの DeviceFileSource、
  /// 存在しなければダウンロードしてキャッシュ保存した上で DeviceFileSource を返します。
  /// 失敗時またはWeb環境では UrlSource(url) にフォールバックします。
  Future<Source> getAudioSource({
    required String notebookId,
    required String entryId,
    required String url,
  }) async {
    if (kIsWeb) {
      return UrlSource(url);
    }

    try {
      final dir = await _getCacheDirectory();
      if (dir == null) return UrlSource(url);

      final file = File('${dir.path}/${_cacheFileName(notebookId, entryId)}');
      if (await file.exists() && await file.length() > 0) {
        return DeviceFileSource(file.path);
      }

      // 未キャッシュの場合はダウンロードして保存
      final res = await http.get(Uri.parse(url));
      if (res.statusCode == 200 && res.bodyBytes.isNotEmpty) {
        await file.writeAsBytes(res.bodyBytes, flush: true);
        return DeviceFileSource(file.path);
      }
    } catch (_) {
      // ネットワークやファイルIOエラー時はUrlSourceへフォールバック
    }

    return UrlSource(url);
  }

  /// エントリの音声が更新・削除された際にキャッシュを無効化します。
  Future<void> evict(String notebookId, String entryId) async {
    if (kIsWeb) return;
    try {
      final dir = await _getCacheDirectory();
      if (dir == null) return;
      final file = File('${dir.path}/${_cacheFileName(notebookId, entryId)}');
      if (await file.exists()) {
        await file.delete();
      }
    } catch (_) {}
  }
}
