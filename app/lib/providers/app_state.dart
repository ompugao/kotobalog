import 'dart:async';
import 'package:app_links/app_links.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../models/notebook.dart';
import '../models/word_entry.dart';
import '../services/api_client.dart';

class AppState {
  final bool isInitializing;
  final String serverUrl;
  final String? token;
  final User? user;
  final String? authError;
  final List<Notebook> notebooks;
  final String? selectedNotebookId;
  final List<WordEntry> entries;
  final bool isLoadingEntries;
  final String searchQuery;
  final Map<String, bool> oauthProviders;

  const AppState({
    this.isInitializing = true,
    required this.serverUrl,
    this.token,
    this.user,
    this.authError,
    this.notebooks = const [],
    this.selectedNotebookId,
    this.entries = const [],
    this.isLoadingEntries = false,
    this.searchQuery = '',
    this.oauthProviders = const {'google': false, 'github': false},
  });

  Notebook? get currentNotebook {
    if (notebooks.isEmpty) return null;
    if (selectedNotebookId != null) {
      for (final n in notebooks) {
        if (n.id == selectedNotebookId) return n;
      }
    }
    return notebooks.first;
  }

  AppState copyWith({
    bool? isInitializing,
    String? serverUrl,
    String? token,
    bool clearToken = false,
    User? user,
    bool clearUser = false,
    String? authError,
    bool clearAuthError = false,
    List<Notebook>? notebooks,
    String? selectedNotebookId,
    List<WordEntry>? entries,
    bool? isLoadingEntries,
    String? searchQuery,
    Map<String, bool>? oauthProviders,
  }) {
    return AppState(
      isInitializing: isInitializing ?? this.isInitializing,
      serverUrl: serverUrl ?? this.serverUrl,
      token: clearToken ? null : (token ?? this.token),
      user: clearUser ? null : (user ?? this.user),
      authError: clearAuthError ? null : (authError ?? this.authError),
      notebooks: notebooks ?? this.notebooks,
      selectedNotebookId: selectedNotebookId ?? this.selectedNotebookId,
      entries: entries ?? this.entries,
      isLoadingEntries: isLoadingEntries ?? this.isLoadingEntries,
      searchQuery: searchQuery ?? this.searchQuery,
      oauthProviders: oauthProviders ?? this.oauthProviders,
    );
  }
}

class AppNotifier extends Notifier<AppState> {
  static const _prefServerUrl = 'kotobalog_server_url';
  static const _prefToken = 'kotobalog_token';
  static const _prefNotebookId = 'kotobalog_notebook_id';
  StreamSubscription<Uri>? _linkSub;

  @override
  AppState build() {
    ref.onDispose(() {
      _linkSub?.cancel();
    });
    Future.microtask(_initialize);
    return AppState(serverUrl: ApiClient.defaultBaseUrl());
  }

  ApiClient get api => ApiClient(
        baseUrl: state.serverUrl,
        token: state.token,
      );

  Future<void> _handleCallbackUri(Uri uri) async {
    final errCode = uri.queryParameters['error'];
    final errIdentity = uri.queryParameters['identity'] ?? '';
    if (errCode == 'not_allowed') {
      state = state.copyWith(
        authError:
            'このアカウント ($errIdentity) は ALLOWLIST に登録されていないためログインできません。',
      );
      return;
    } else if (errCode != null && errCode.isNotEmpty) {
      state = state.copyWith(
        authError: 'OAuthログインに失敗しました ($errCode)',
      );
      return;
    }

    final urlToken = uri.queryParameters['token'];
    if (urlToken != null && urlToken.isNotEmpty) {
      await loginWithToken(urlToken);
    }
  }

  Future<void> _initialize() async {
    final prefs = await SharedPreferences.getInstance();
    final savedUrl =
        prefs.getString(_prefServerUrl) ?? ApiClient.defaultBaseUrl();
    String? savedToken = prefs.getString(_prefToken);
    final savedNotebookId = prefs.getString(_prefNotebookId);
    String? oauthErrorMsg;

    if (kIsWeb) {
      final errCode = Uri.base.queryParameters['error'];
      final errIdentity = Uri.base.queryParameters['identity'] ?? '';
      if (errCode == 'not_allowed') {
        oauthErrorMsg =
            'このアカウント ($errIdentity) は ALLOWLIST に登録されていないためログインできません。';
      } else if (errCode != null && errCode.isNotEmpty) {
        oauthErrorMsg = 'OAuthログインに失敗しました ($errCode)';
      }

      final urlToken = Uri.base.queryParameters['token'];
      if (urlToken != null && urlToken.isNotEmpty) {
        savedToken = urlToken;
        await prefs.setString(_prefToken, urlToken);
      }
    } else {
      try {
        final appLinks = AppLinks();
        final initialUri = await appLinks.getInitialLink();
        if (initialUri != null && initialUri.scheme == 'kotobalog') {
          final urlToken = initialUri.queryParameters['token'];
          if (urlToken != null && urlToken.isNotEmpty) {
            savedToken = urlToken;
            await prefs.setString(_prefToken, urlToken);
          }
          final errCode = initialUri.queryParameters['error'];
          final errIdentity = initialUri.queryParameters['identity'] ?? '';
          if (errCode == 'not_allowed') {
            oauthErrorMsg =
                'このアカウント ($errIdentity) は ALLOWLIST に登録されていないためログインできません。';
          } else if (errCode != null && errCode.isNotEmpty) {
            oauthErrorMsg = 'OAuthログインに失敗しました ($errCode)';
          }
        }
        _linkSub = appLinks.uriLinkStream.listen((uri) {
          if (uri.scheme == 'kotobalog') {
            _handleCallbackUri(uri);
          }
        });
      } catch (_) {}
    }

    state = state.copyWith(
      serverUrl: savedUrl,
      token: savedToken,
      selectedNotebookId: savedNotebookId,
      authError: oauthErrorMsg,
    );

    final oauth = await api.getOAuthStatus();
    state = state.copyWith(oauthProviders: oauth);

    if (savedToken != null && savedToken.isNotEmpty) {
      await loginWithToken(savedToken, isInitial: true);
      return;
    }

    state = state.copyWith(isInitializing: false);
  }

  Future<void> setServerUrl(String url) async {
    final clean = url.trim();
    if (clean.isEmpty) return;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_prefServerUrl, clean);
    state = state.copyWith(serverUrl: clean);
    final oauth = await api.getOAuthStatus();
    state = state.copyWith(oauthProviders: oauth);
  }

  Future<void> loginWithToken(String token, {bool isInitial = false}) async {
    final clean = token.trim();
    if (clean.isEmpty) return;

    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_prefToken, clean);
    state = state.copyWith(token: clean, clearAuthError: true);

    try {
      final user = await api.getMe();
      final notebooks = await api.listNotebooks();
      String? activeNbId = state.selectedNotebookId;
      if (notebooks.isNotEmpty &&
          (activeNbId == null || !notebooks.any((n) => n.id == activeNbId))) {
        activeNbId = notebooks.first.id;
        await prefs.setString(_prefNotebookId, activeNbId);
      }
      state = state.copyWith(
        isInitializing: false,
        user: user,
        notebooks: notebooks,
        selectedNotebookId: activeNbId,
      );
      if (activeNbId != null) {
        await refreshEntries();
      }
    } catch (e) {
      await prefs.remove(_prefToken);
      state = state.copyWith(
        isInitializing: false,
        clearToken: true,
        clearUser: true,
        authError: isInitial ? null : e.toString(),
      );
      if (!isInitial) rethrow;
    }
  }

  Future<void> updateDisplayName(String displayName) async {
    final updatedUser = await api.updateDisplayName(displayName);
    final notebooks = await api.listNotebooks();
    state = state.copyWith(
      user: updatedUser,
      notebooks: notebooks,
    );
    await refreshEntries();
  }

  Future<void> logout() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_prefToken);
    state = state.copyWith(
      clearToken: true,
      clearUser: true,
      notebooks: const [],
      entries: const [],
    );
  }

  Future<void> selectNotebook(String notebookId) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_prefNotebookId, notebookId);
    state = state.copyWith(
      selectedNotebookId: notebookId,
      searchQuery: '',
    );
    await refreshEntries();
  }

  Future<Notebook> createNotebook({
    required String childName,
    required String birthDate,
    required String themeColor,
    required String iconEmoji,
  }) async {
    final nb = await api.createNotebook(
      childName: childName,
      birthDate: birthDate,
      themeColor: themeColor,
      iconEmoji: iconEmoji,
    );
    final updated = [...state.notebooks, nb];
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_prefNotebookId, nb.id);
    state = state.copyWith(
      notebooks: updated,
      selectedNotebookId: nb.id,
      entries: const [],
    );
    return nb;
  }

  Future<Notebook> updateNotebook({
    required String id,
    required String childName,
    required String birthDate,
    required String themeColor,
    required String iconEmoji,
  }) async {
    final nb = await api.updateNotebook(
      id: id,
      childName: childName,
      birthDate: birthDate,
      themeColor: themeColor,
      iconEmoji: iconEmoji,
    );
    final updated = state.notebooks.map((n) => n.id == id ? nb : n).toList();
    state = state.copyWith(notebooks: updated);
    return nb;
  }

  Future<void> setSearchQuery(String query) async {
    state = state.copyWith(searchQuery: query);
    await refreshEntries();
  }

  Future<void> refreshEntries() async {
    final nb = state.currentNotebook;
    if (nb == null) return;
    state = state.copyWith(isLoadingEntries: true);
    try {
      final list = await api.listEntries(
        nb.id,
        query: state.searchQuery,
      );
      state = state.copyWith(entries: list, isLoadingEntries: false);
    } catch (_) {
      state = state.copyWith(isLoadingEntries: false);
    }
  }

  Future<void> deleteEntry(String entryId) async {
    final nb = state.currentNotebook;
    if (nb == null) return;
    await api.deleteEntry(nb.id, entryId);
    state = state.copyWith(
      entries: state.entries.where((e) => e.id != entryId).toList(),
    );
  }
}

final appProvider = NotifierProvider<AppNotifier, AppState>(AppNotifier.new);
