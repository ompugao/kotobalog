import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:record/record.dart';
import '../providers/audio_device_provider.dart';
import '../services/audio_device_service.dart';

/// マイク選択・設定用シート
class MicSelectorSheet extends ConsumerStatefulWidget {
  /// 現在選択されているマイク（シート表示時の初期値）
  final InputDevice? currentDevice;

  /// マイクが選択された時のコールバック
  final ValueChanged<InputDevice?>? onDeviceSelected;

  const MicSelectorSheet({
    super.key,
    this.currentDevice,
    this.onDeviceSelected,
  });

  /// ボトムシートとして表示するヘルパー関数
  static Future<InputDevice?> show(
    BuildContext context, {
    InputDevice? currentDevice,
    ValueChanged<InputDevice?>? onDeviceSelected,
  }) {
    return showModalBottomSheet<InputDevice?>(
      context: context,
      isScrollControlled: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(24)),
      ),
      builder: (_) => MicSelectorSheet(
        currentDevice: currentDevice,
        onDeviceSelected: onDeviceSelected,
      ),
    );
  }

  @override
  ConsumerState<MicSelectorSheet> createState() => _MicSelectorSheetState();
}

class _MicSelectorSheetState extends ConsumerState<MicSelectorSheet> {
  bool _saveAsDefault = true;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(audioDeviceProvider.notifier).refreshDevices();
    });
  }

  @override
  Widget build(BuildContext context) {
    final deviceState = ref.watch(audioDeviceProvider);
    final displayedEnriched = deviceState.displayedEnrichedDevices;
    final activeDevice = widget.currentDevice ?? deviceState.resolvedDevice;
    final isChildFilterOn = deviceState.settings.filterChildSuitableOnly;

    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 16, 20, 24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            // ハンドルバー
            Center(
              child: Container(
                width: 36,
                height: 4,
                decoration: BoxDecoration(
                  color: Colors.brown.withValues(alpha: 0.25),
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),
            const SizedBox(height: 16),

            // ヘッダー
            Row(
              children: [
                const Icon(Icons.mic, color: Color(0xFF5D4037), size: 24),
                const SizedBox(width: 8),
                const Expanded(
                  child: Text(
                    'マイクを選択',
                    style: TextStyle(
                      fontSize: 18,
                      fontWeight: FontWeight.bold,
                      color: Color(0xFF3E2723),
                    ),
                  ),
                ),
                if (deviceState.isLoading)
                  const SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                else
                  IconButton(
                    tooltip: 'マイクを再検出',
                    icon: const Icon(Icons.refresh, size: 20),
                    onPressed: () {
                      ref.read(audioDeviceProvider.notifier).refreshDevices();
                    },
                  ),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              'DJI MicレシーバーやBluetoothイヤホン接続時に、どのマイクで録音するかを選択できます。',
              style: TextStyle(
                fontSize: 12,
                color: Colors.brown.shade600,
              ),
            ),
            const SizedBox(height: 12),

            // 子どもの声適性フィルタトグル
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
              decoration: BoxDecoration(
                color: const Color(0xFFFFF3E0),
                borderRadius: BorderRadius.circular(12),
                border: Border.all(color: const Color(0xFFFFCC80)),
              ),
              child: Row(
                children: [
                  const Icon(Icons.child_care, size: 20, color: Color(0xFFE65100)),
                  const SizedBox(width: 8),
                  const Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          '子どもの声向きのマイクのみ表示',
                          style: TextStyle(
                            fontSize: 12,
                            fontWeight: FontWeight.bold,
                            color: Color(0xFF5D4037),
                          ),
                        ),
                        Text(
                          '底面メインマイクやDJI Micなど会話向きに絞り込み',
                          style: TextStyle(fontSize: 11, color: Colors.brown),
                        ),
                      ],
                    ),
                  ),
                  Switch.adaptive(
                    value: isChildFilterOn,
                    activeThumbColor: const Color(0xFFE65100),
                    onChanged: (val) {
                      ref
                          .read(audioDeviceProvider.notifier)
                          .toggleFilterChildSuitableOnly(val);
                    },
                  ),
                ],
              ),
            ),
            const SizedBox(height: 12),

            // 接続中のデバイス一覧
            if (displayedEnriched.isEmpty && !deviceState.isLoading)
              Container(
                padding: const EdgeInsets.all(16),
                decoration: BoxDecoration(
                  color: Colors.brown.withValues(alpha: 0.06),
                  borderRadius: BorderRadius.circular(12),
                ),
                child: const Row(
                  children: [
                    Icon(Icons.info_outline, size: 20, color: Colors.brown),
                    SizedBox(width: 10),
                    Expanded(
                      child: Text(
                        'マイクが検出されませんでした。OSの標準マイクを使用します。',
                        style: TextStyle(fontSize: 13, color: Colors.brown),
                      ),
                    ),
                  ],
                ),
              )
            else
              Flexible(
                child: ListView(
                  shrinkWrap: true,
                  children: [
                    // 自動（システム既定）
                    _buildDeviceTile(
                      title: '自動（システム標準）',
                      subtitle: 'OSの標準設定に従って録音します',
                      icon: Icons.tune,
                      isSelected: activeDevice == null,
                      onTap: () {
                        _handleSelect(null);
                      },
                    ),

                    const Divider(height: 16),

                    // 検出されたマイク一覧
                    ...displayedEnriched.map((enriched) {
                      final isSelected = activeDevice?.id == enriched.device.id;
                      final icon =
                          AudioDeviceHelper.getDeviceIcon(enriched.device.type);
                      final title = enriched.displayName;
                      final subtitle = enriched.recommendationReason;

                      return _buildDeviceTile(
                        title: title,
                        subtitle: subtitle,
                        icon: icon,
                        isSelected: isSelected,
                        onTap: () {
                          _handleSelect(enriched.device);
                        },
                      );
                    }),
                  ],
                ),
              ),

            const SizedBox(height: 16),

            // 設定保存スイッチ
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
              decoration: BoxDecoration(
                color: Colors.brown.withValues(alpha: 0.05),
                borderRadius: BorderRadius.circular(12),
              ),
              child: Row(
                children: [
                  const Icon(Icons.bookmark_outline, size: 18, color: Colors.brown),
                  const SizedBox(width: 8),
                  const Expanded(
                    child: Text(
                      '次回以降もこの設定を優先する',
                      style: TextStyle(
                        fontSize: 13,
                        fontWeight: FontWeight.w500,
                        color: Color(0xFF3E2723),
                      ),
                    ),
                  ),
                  Switch.adaptive(
                    value: _saveAsDefault,
                    onChanged: (val) {
                      setState(() => _saveAsDefault = val);
                    },
                  ),
                ],
              ),
            ),

            const SizedBox(height: 10),

            // Bluetooth権限の案内（必要な場合）
            OutlinedButton.icon(
              style: OutlinedButton.styleFrom(
                foregroundColor: Colors.brown.shade700,
                side: BorderSide(color: Colors.brown.shade300),
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(12),
                ),
              ),
              icon: const Icon(Icons.bluetooth_searching, size: 18),
              label: const Text('Bluetoothマイク権限をチェック / 許可'),
              onPressed: () async {
                final granted = await ref
                    .read(audioDeviceProvider.notifier)
                    .requestBluetoothPermission();
                if (context.mounted) {
                  ScaffoldMessenger.of(context).showSnackBar(
                    SnackBar(
                      content: Text(
                        granted
                            ? 'Bluetooth機器のアクセス権限を確認しました'
                            : 'Bluetooth権限が許可されませんでした',
                      ),
                    ),
                  );
                }
              },
            ),
          ],
        ),
      ),
    );
  }

  void _handleSelect(InputDevice? device) {
    if (_saveAsDefault) {
      ref.read(audioDeviceProvider.notifier).selectDevice(device, saveAsPreferred: true);
    } else {
      ref.read(audioDeviceProvider.notifier).selectDevice(device, saveAsPreferred: false);
    }
    widget.onDeviceSelected?.call(device);
    Navigator.of(context).pop(device);
  }

  Widget _buildDeviceTile({
    required String title,
    required String subtitle,
    required IconData icon,
    required bool isSelected,
    required VoidCallback onTap,
  }) {
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      decoration: BoxDecoration(
        color: isSelected
            ? const Color(0xFFFF8A65).withValues(alpha: 0.12)
            : Colors.transparent,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(
          color: isSelected
              ? const Color(0xFFFF8A65)
              : Colors.brown.withValues(alpha: 0.18),
          width: isSelected ? 1.5 : 1.0,
        ),
      ),
      child: ListTile(
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
        ),
        leading: Container(
          width: 40,
          height: 40,
          decoration: BoxDecoration(
            color: isSelected
                ? const Color(0xFFFF8A65).withValues(alpha: 0.2)
                : Colors.brown.withValues(alpha: 0.08),
            shape: BoxShape.circle,
          ),
          child: Icon(
            icon,
            color: isSelected ? const Color(0xFFD84315) : Colors.brown.shade700,
            size: 22,
          ),
        ),
        title: Text(
          title,
          style: TextStyle(
            fontSize: 14,
            fontWeight: isSelected ? FontWeight.bold : FontWeight.w500,
            color: const Color(0xFF3E2723),
          ),
        ),
        subtitle: Text(
          subtitle,
          style: TextStyle(
            fontSize: 12,
            color: Colors.brown.shade600,
          ),
        ),
        trailing: isSelected
            ? const Icon(Icons.check_circle, color: Color(0xFFD84315))
            : const Icon(Icons.radio_button_unchecked, color: Colors.grey),
        onTap: onTap,
      ),
    );
  }
}
