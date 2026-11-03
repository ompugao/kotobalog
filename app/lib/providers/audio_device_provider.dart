import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:record/record.dart';
import '../services/audio_device_service.dart';

class AudioDeviceState {
  final bool isLoading;
  final List<InputDevice> availableDevices;
  final List<EnrichedAudioDevice> enrichedDevices;
  final AudioDeviceSettings settings;
  final InputDevice? resolvedDevice;
  final bool bluetoothPermissionGranted;

  const AudioDeviceState({
    this.isLoading = true,
    this.availableDevices = const [],
    this.enrichedDevices = const [],
    this.settings = const AudioDeviceSettings(),
    this.resolvedDevice,
    this.bluetoothPermissionGranted = false,
  });

  /// 外部マイクが現在接続されているか
  bool get hasExternalDevices {
    return availableDevices.any((d) => AudioDeviceHelper.isExternalType(d.type));
  }

  /// 画面に表示するマイク一覧（フィルタ適用時）
  List<EnrichedAudioDevice> get displayedEnrichedDevices {
    if (settings.filterChildSuitableOnly) {
      final filtered =
          enrichedDevices.where((d) => d.isRecommendedForChild).toList();
      return filtered.isNotEmpty ? filtered : enrichedDevices;
    }
    return enrichedDevices;
  }

  /// 現在使用されるマイクの表示名
  String get activeDeviceDisplayName {
    if (resolvedDevice != null) {
      // enrichedDevices から一致するものを探して位置付き名称を取得
      for (final e in enrichedDevices) {
        if (e.device.id == resolvedDevice!.id) {
          return e.displayName;
        }
      }
      return AudioDeviceHelper.formatDeviceName(resolvedDevice!);
    }
    switch (settings.mode) {
      case AudioDeviceMode.preferBuiltIn:
        return '端末の内蔵マイク（底面優先）';
      case AudioDeviceMode.preferExternal:
        return '外部マイク優先（未接続時は内蔵）';
      case AudioDeviceMode.specific:
        return settings.preferredDeviceLabel != null
            ? '${settings.preferredDeviceLabel} (未接続)'
            : '指定マイク (未接続)';
      case AudioDeviceMode.auto:
        return '自動（システム標準）';
    }
  }

  AudioDeviceState copyWith({
    bool? isLoading,
    List<InputDevice>? availableDevices,
    List<EnrichedAudioDevice>? enrichedDevices,
    AudioDeviceSettings? settings,
    InputDevice? resolvedDevice,
    bool clearResolvedDevice = false,
    bool? bluetoothPermissionGranted,
  }) {
    return AudioDeviceState(
      isLoading: isLoading ?? this.isLoading,
      availableDevices: availableDevices ?? this.availableDevices,
      enrichedDevices: enrichedDevices ?? this.enrichedDevices,
      settings: settings ?? this.settings,
      resolvedDevice:
          clearResolvedDevice ? null : (resolvedDevice ?? this.resolvedDevice),
      bluetoothPermissionGranted:
          bluetoothPermissionGranted ?? this.bluetoothPermissionGranted,
    );
  }
}

class AudioDeviceNotifier extends Notifier<AudioDeviceState> {
  final AudioRecorder _recorder = AudioRecorder();

  @override
  AudioDeviceState build() {
    ref.onDispose(() {
      _recorder.dispose();
    });
    Future.microtask(_initialize);
    return const AudioDeviceState();
  }

  Future<void> _initialize() async {
    final settings = await AudioDeviceService.loadSettings();
    state = state.copyWith(settings: settings);
    await refreshDevices();
  }

  /// 利用可能なマイクを再取得
  Future<void> refreshDevices() async {
    state = state.copyWith(isLoading: true);
    final enriched = await AudioDeviceService.listEnrichedDevices(_recorder);
    final devices = enriched.map((e) => e.device).toList();
    final resolved = AudioDeviceService.resolveDevice(
      enrichedDevices: enriched,
      settings: state.settings,
    );

    state = state.copyWith(
      isLoading: false,
      availableDevices: devices,
      enrichedDevices: enriched,
      resolvedDevice: resolved,
      clearResolvedDevice: resolved == null,
    );
  }

  /// 「子どもの声に向いているマイクのみ表示」フィルタの切り替え
  Future<void> toggleFilterChildSuitableOnly(bool value) async {
    final newSettings =
        state.settings.copyWith(filterChildSuitableOnly: value);
    await AudioDeviceService.saveSettings(newSettings);
    state = state.copyWith(settings: newSettings);
  }

  /// 優先モードを更新して保存
  Future<void> updateMode(AudioDeviceMode mode) async {
    final newSettings = state.settings.copyWith(
      mode: mode,
      clearSpecificDevice: mode != AudioDeviceMode.specific,
    );
    await AudioDeviceService.saveSettings(newSettings);
    final resolved = AudioDeviceService.resolveDevice(
      enrichedDevices: state.enrichedDevices,
      settings: newSettings,
    );

    state = state.copyWith(
      settings: newSettings,
      resolvedDevice: resolved,
      clearResolvedDevice: resolved == null,
    );
  }

  /// 特定のマイクを選択
  Future<void> selectDevice(InputDevice? device, {bool saveAsPreferred = true}) async {
    if (device == null) {
      // システム自動
      await updateMode(AudioDeviceMode.auto);
      return;
    }

    if (device.type == InputDeviceType.builtIn) {
      if (saveAsPreferred) {
        // 内蔵マイク優先モード
        final newSettings = state.settings.copyWith(
          mode: AudioDeviceMode.preferBuiltIn,
          preferredDeviceId: device.id,
          preferredDeviceLabel: device.label,
        );
        await AudioDeviceService.saveSettings(newSettings);
        state = state.copyWith(
          settings: newSettings,
          resolvedDevice: device,
        );
      } else {
        state = state.copyWith(resolvedDevice: device);
      }
      return;
    }

    if (saveAsPreferred) {
      final newSettings = state.settings.copyWith(
        mode: AudioDeviceMode.specific,
        preferredDeviceId: device.id,
        preferredDeviceLabel: device.label,
      );
      await AudioDeviceService.saveSettings(newSettings);
      state = state.copyWith(
        settings: newSettings,
        resolvedDevice: device,
      );
    } else {
      state = state.copyWith(resolvedDevice: device);
    }
  }

  /// Bluetooth 権限を要求して再スキャン
  Future<bool> requestBluetoothPermission() async {
    final granted = await AudioDeviceService.ensureBluetoothPermission();
    state = state.copyWith(bluetoothPermissionGranted: granted);
    await refreshDevices();
    return granted;
  }
}

final audioDeviceProvider =
    NotifierProvider<AudioDeviceNotifier, AudioDeviceState>(
  AudioDeviceNotifier.new,
);
