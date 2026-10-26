import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';
import '../providers/app_state.dart';

class AuthScreen extends ConsumerStatefulWidget {
  const AuthScreen({super.key});

  @override
  ConsumerState<AuthScreen> createState() => _AuthScreenState();
}

class _AuthScreenState extends ConsumerState<AuthScreen> {
  bool _showServerConfig = false;
  late final TextEditingController _serverCtrl;
  final _tokenCtrl = TextEditingController();

  @override
  void initState() {
    super.initState();
    final url = ref.read(appProvider).serverUrl;
    _serverCtrl = TextEditingController(text: url);
  }

  @override
  void dispose() {
    _serverCtrl.dispose();
    _tokenCtrl.dispose();
    super.dispose();
  }

  Future<void> _startOAuth(String provider) async {
    final notifier = ref.read(appProvider.notifier);
    if (_showServerConfig) {
      await notifier.setServerUrl(_serverCtrl.text);
    }
    final redirect = kIsWeb ? Uri.base.origin : 'kotobalog://auth';
    final url = notifier.api.oauthUrl(provider, redirectUri: redirect);
    final uri = Uri.parse(url);
    await launchUrl(
      uri,
      webOnlyWindowName: kIsWeb ? '_self' : null,
      mode: LaunchMode.externalApplication,
    );
  }

  @override
  Widget build(BuildContext context) {
    final appState = ref.watch(appProvider);

    return Scaffold(
      backgroundColor: const Color(0xFFFFFBF7),
      body: SafeArea(
        child: Center(
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(24),
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 420),
              child: Card(
                elevation: 2,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(24),
                ),
                child: Padding(
                  padding: const EdgeInsets.all(28),
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      const Text(
                        '📖 ことばログ',
                        textAlign: TextAlign.center,
                        style: TextStyle(
                          fontSize: 28,
                          fontWeight: FontWeight.bold,
                          color: Color(0xFF4E342E),
                        ),
                      ),
                      const SizedBox(height: 8),
                      Text(
                        'お子さまが話したはじめての言葉や\nかわいい言い間違いを家族で記録しよう',
                        textAlign: TextAlign.center,
                        style: TextStyle(
                          fontSize: 13,
                          color: Colors.brown.shade600,
                        ),
                      ),
                      const SizedBox(height: 24),
                      if (appState.authError != null) ...[
                        Container(
                          padding: const EdgeInsets.all(12),
                          decoration: BoxDecoration(
                            color: Colors.red.shade50,
                            borderRadius: BorderRadius.circular(12),
                            border: Border.all(color: Colors.red.shade200),
                          ),
                          child: Text(
                            appState.authError!,
                            style: TextStyle(
                              color: Colors.red.shade800,
                              fontSize: 13,
                            ),
                          ),
                        ),
                        const SizedBox(height: 16),
                      ],
                      FilledButton.icon(
                        onPressed: () => _startOAuth('google'),
                        style: FilledButton.styleFrom(
                          padding: const EdgeInsets.symmetric(vertical: 15),
                          shape: RoundedRectangleBorder(
                            borderRadius: BorderRadius.circular(14),
                          ),
                        ),
                        icon: const Icon(Icons.account_circle_outlined),
                        label: const Text(
                          'Googleでログイン',
                          style: TextStyle(
                            fontSize: 16,
                            fontWeight: FontWeight.bold,
                          ),
                        ),
                      ),
                      const SizedBox(height: 12),
                      FilledButton.tonalIcon(
                        onPressed: () => _startOAuth('github'),
                        style: FilledButton.styleFrom(
                          padding: const EdgeInsets.symmetric(vertical: 15),
                          shape: RoundedRectangleBorder(
                            borderRadius: BorderRadius.circular(14),
                          ),
                        ),
                        icon: const Icon(Icons.code),
                        label: const Text(
                          'GitHubでログイン',
                          style: TextStyle(
                            fontSize: 16,
                            fontWeight: FontWeight.bold,
                          ),
                        ),
                      ),
                      const SizedBox(height: 16),
                      Text(
                        '※ サーバーの ALLOWLIST に登録された家族アカウントのみログインできます',
                        textAlign: TextAlign.center,
                        style: TextStyle(
                          fontSize: 11,
                          color: Colors.brown.shade500,
                        ),
                      ),
                      const SizedBox(height: 12),
                      const Divider(),
                      TextButton.icon(
                        onPressed: () {
                          setState(() {
                            _showServerConfig = !_showServerConfig;
                          });
                        },
                        icon: const Icon(Icons.dns_outlined, size: 18),
                        label: Text(
                          '接続先サーバー (NAS): ${appState.serverUrl}',
                          style: const TextStyle(fontSize: 12),
                        ),
                      ),
                      if (_showServerConfig) ...[
                        const SizedBox(height: 8),
                        Row(
                          children: [
                            Expanded(
                              child: TextField(
                                controller: _serverCtrl,
                                decoration: const InputDecoration(
                                  labelText:
                                      'サーバーURL (例: https://kotobalog.example.com)',
                                  isDense: true,
                                  border: OutlineInputBorder(),
                                ),
                              ),
                            ),
                            const SizedBox(width: 8),
                            FilledButton.tonal(
                              onPressed: () async {
                                await ref
                                    .read(appProvider.notifier)
                                    .setServerUrl(_serverCtrl.text);
                                if (mounted) {
                                  setState(() {
                                    _showServerConfig = false;
                                  });
                                }
                              },
                              child: const Text('保存'),
                            ),
                          ],
                        ),
                        const SizedBox(height: 10),
                        Row(
                          children: [
                            Expanded(
                              child: TextField(
                                controller: _tokenCtrl,
                                decoration: const InputDecoration(
                                  labelText: 'アクセストークン直接入力 (Android / 開発用)',
                                  isDense: true,
                                  border: OutlineInputBorder(),
                                ),
                              ),
                            ),
                            const SizedBox(width: 8),
                            OutlinedButton(
                              onPressed: () async {
                                try {
                                  await ref
                                      .read(appProvider.notifier)
                                      .loginWithToken(_tokenCtrl.text);
                                } catch (_) {}
                              },
                              child: const Text('認証'),
                            ),
                          ],
                        ),
                      ],
                    ],
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
