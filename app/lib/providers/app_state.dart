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

  @override
  AppState build() {
    Future.microtask(_initialize);
    return AppState(serverUrl: ApiClient.defaultBaseUrl());
  }

  ApiClient get api => ApiClient(
        baseUrl: state.serverUrl,
        token: state.token,
      );

  Future<void> _initialize() async {
    final prefs = await SharedPreferences.getInstance();
    final savedUrl = prefs.getString(_prefServerUrl) ?? ApiClient.defaultBaseUrl();
    String? savedToken = prefs.getString(_prefToken);
    final savedNotebookId = prefs.getString(_prefNotebookId);

    // Check URL query param ?token= (from OAuth redirect on Web)
    if (kIsWeb) {
      final urlToken = Uri.base.queryParameters['token'];
      if (urlToken != null && urlToken.isNotEmpty) {
        savedToken = urlToken;
        await prefs.setString(_prefToken, urlToken);
      }
    }

    state = state.copyWith(
      serverUrl: savedUrl,
      token: savedToken,
      selectedNotebookId: savedNotebookId,
    );

    final oauth = await api.getOAuthStatus();
    state = state.copyWith(oauthProviders: oauth);

    if (savedToken != null && savedToken.isNotEmpty) {
      try {
        final user = await api.getMe();
        final notebooks = await api.listNotebooks();
        String? activeNbId = savedNotebookId;
        if (notebooks.isNotEmpty &&
            (activeNbId == null || !notebooks.any((n) => n.id == activeNbId))) {
          activeNbId = notebooks.first.id;
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
        return;
      } catch (_) {
        await prefs.remove(_prefToken);
        state = state.copyWith(
          isInitializing: false,
          clearToken: true,
          clearUser: true,
        );
        return;
      }
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

  Future<void> login(String email, String password) async {
    final res = await api.login(email: email, password: password);
    await _onAuthSuccess(res.token, res.user);
  }

  Future<void> register(String email, String password, String displayName) async {
    final res = await api.register(
      email: email,
      password: password,
      displayName: displayName,
    );
    await _onAuthSuccess(res.token, res.user);
  }

  Future<void> _onAuthSuccess(String token, User user) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_prefToken, token);
    state = state.copyWith(token: token, user: user);

    final notebooks = await api.listNotebooks();
    String? activeNbId = state.selectedNotebookId;
    if (notebooks.isNotEmpty &&
        (activeNbId == null || !notebooks.any((n) => n.id == activeNbId))) {
      activeNbId = notebooks.first.id;
      await prefs.setString(_prefNotebookId, activeNbId);
    }
    state = state.copyWith(
      notebooks: notebooks,
      selectedNotebookId: activeNbId,
      entries: const [],
    );
    if (activeNbId != null) {
      await refreshEntries();
    }
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

  Future<Notebook> joinNotebook(String code) async {
    final nb = await api.joinNotebook(code);
    final notebooks = await api.listNotebooks();
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_prefNotebookId, nb.id);
    state = state.copyWith(
      notebooks: notebooks,
      selectedNotebookId: nb.id,
    );
    await refreshEntries();
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
