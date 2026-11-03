import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:permission_handler/permission_handler.dart';
import 'package:record/record.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// 録音マイクの選択・優先モード
enum AudioDeviceMode {
  /// システム自動（OSの標準動作）
  auto,

  /// 端末の内蔵マイクを常に優先（Bluetooth接続時でもスマホマイクを使用）
  preferBuiltIn,

  /// 外部マイク（USB / DJI Mic / Bluetooth等）を優先
  preferExternal,

  /// 特定の機器を固定指定
  specific,
}

extension AudioDeviceModeExt on AudioDeviceMode {
  String get label {
    switch (this) {
      case AudioDeviceMode.auto:
        return '自動（システム標準）';
      case AudioDeviceMode.preferBuiltIn:
        return '端末の内蔵マイクを優先';
      case AudioDeviceMode.preferExternal:
        return '外部マイク（DJI Mic / イヤホン）を優先';
      case AudioDeviceMode.specific:
        return '特定のマイクに固定';
    }
  }

  String get description {
    switch (this) {
      case AudioDeviceMode.auto:
        return 'OS標準の音声ルーティングを使用します';
      case AudioDeviceMode.preferBuiltIn:
        return 'イヤホン接続時でもスマートフォンのマイクで子どもの声を拾います';
      case AudioDeviceMode.preferExternal:
        return 'DJI MicやBluetooth機器が接続されていればそちらを優先します';
      case AudioDeviceMode.specific:
        return '選択した特定のマイクで常に録音します';
    }
  }
}

/// マイク設定データクラス
class AudioDeviceSettings {
  final AudioDeviceMode mode;
  final String? preferredDeviceId;
  final String? preferredDeviceLabel;
  final bool filterChildSuitableOnly;

  const AudioDeviceSettings({
    this.mode = AudioDeviceMode.auto,
    this.preferredDeviceId,
    this.preferredDeviceLabel,
    this.filterChildSuitableOnly = false,
  });

  Map<String, dynamic> toMap() => {
        'mode': mode.name,
        'preferredDeviceId': preferredDeviceId,
        'preferredDeviceLabel': preferredDeviceLabel,
        'filterChildSuitableOnly': filterChildSuitableOnly,
      };

  factory AudioDeviceSettings.fromMap(Map<String, dynamic> map) {
    final modeName = map['mode'] as String?;
    final mode = AudioDeviceMode.values.firstWhere(
      (m) => m.name == modeName,
      orElse: () => AudioDeviceMode.auto,
    );
    return AudioDeviceSettings(
      mode: mode,
      preferredDeviceId: map['preferredDeviceId'] as String?,
      preferredDeviceLabel: map['preferredDeviceLabel'] as String?,
      filterChildSuitableOnly:
          map['filterChildSuitableOnly'] as bool? ?? false,
    );
  }

  AudioDeviceSettings copyWith({
    AudioDeviceMode? mode,
    String? preferredDeviceId,
    String? preferredDeviceLabel,
    bool clearSpecificDevice = false,
    bool? filterChildSuitableOnly,
  }) {
    return AudioDeviceSettings(
      mode: mode ?? this.mode,
      preferredDeviceId: clearSpecificDevice
          ? null
          : (preferredDeviceId ?? this.preferredDeviceId),
      preferredDeviceLabel: clearSpecificDevice
          ? null
          : (preferredDeviceLabel ?? this.preferredDeviceLabel),
      filterChildSuitableOnly:
          filterChildSuitableOnly ?? this.filterChildSuitableOnly,
    );
  }
}

/// 物理位置や子ども音声録音の適性情報を付加したマイク情報
class EnrichedAudioDevice {
  final InputDevice device;

  /// Android AudioDeviceInfo.getAddress()（例: "bottom", "back", "top" 等）
  final String? address;

  /// ユーザー向け物理位置ラベル（例: "底面 / メイン", "背面 / カメラ用"）
  final String positionLabel;

  /// 子どもの声・日常会話の録音に向いているか
  final bool isRecommendedForChild;

  /// 推奨または非推奨の理由説明
  final String recommendationReason;

  const EnrichedAudioDevice({
    required this.device,
    this.address,
    this.positionLabel = '',
    this.isRecommendedForChild = false,
    this.recommendationReason = '',
  });

  String get displayName => AudioDeviceHelper.formatDeviceName(
        device,
        address: address,
        positionLabel: positionLabel,
      );
}

/// マイク情報補助ユーティリティ
class AudioDeviceHelper {
  /// デバイス種別に応じたアイコンを返す
  static IconData getDeviceIcon(InputDeviceType type) {
    switch (type) {
      case InputDeviceType.builtIn:
        return Icons.phone_android;
      case InputDeviceType.usb:
        return Icons.mic_external_on;
      case InputDeviceType.bluetoothSco:
      case InputDeviceType.bluetoothA2dp:
      case InputDeviceType.bluetoothLe:
        return Icons.bluetooth_audio;
      case InputDeviceType.wiredHeadset:
      case InputDeviceType.lineIn:
        return Icons.headset_mic;
      case InputDeviceType.hdmi:
      case InputDeviceType.airPlay:
      case InputDeviceType.thunderbolt:
      case InputDeviceType.displayPort:
      case InputDeviceType.unknown:
        return Icons.mic;
    }
  }

  /// デバイス種別のカテゴリ表示名
  static String getCategoryLabel(InputDeviceType type) {
    switch (type) {
      case InputDeviceType.builtIn:
        return '内蔵マイク';
      case InputDeviceType.usb:
        return 'USBマイク / DJI Mic';
      case InputDeviceType.bluetoothSco:
      case InputDeviceType.bluetoothA2dp:
      case InputDeviceType.bluetoothLe:
        return 'Bluetoothイヤホン / マイク';
      case InputDeviceType.wiredHeadset:
      case InputDeviceType.lineIn:
        return '有線マイク';
      default:
        return '外部マイク';
    }
  }

  /// ユーザー向けのわかりやすいマイク名表示
  static String formatDeviceName(
    InputDevice device, {
    String? address,
    String? positionLabel,
  }) {
    final rawLabel = device.label.trim();

    if (device.type == InputDeviceType.builtIn) {
      if (positionLabel != null && positionLabel.isNotEmpty) {
        return '端末の内蔵マイク（$positionLabel）';
      }
      if (address != null && address.isNotEmpty) {
        final lower = address.toLowerCase();
        if (lower.contains('bottom')) {
          return '端末の内蔵マイク（底面）';
        } else if (lower.contains('back')) {
          return '端末の内蔵マイク（背面）';
        } else if (lower.contains('top')) {
          return '端末の内蔵マイク（天面）';
        }
      }
      return '端末の内蔵マイク';
    }

    if (device.type == InputDeviceType.usb) {
      if (rawLabel.isEmpty) return 'USBマイク';
      return rawLabel;
    }

    if (isBluetoothType(device.type)) {
      if (rawLabel.isEmpty) return 'Bluetoothイヤホン';
      return rawLabel;
    }

    if (device.type == InputDeviceType.wiredHeadset ||
        device.type == InputDeviceType.lineIn) {
      if (rawLabel.isEmpty) return '有線マイク';
      return rawLabel;
    }

    return rawLabel.isNotEmpty ? rawLabel : 'マイク (${device.id})';
  }

  /// Bluetooth機器かどうかを判定
  static bool isBluetoothType(InputDeviceType type) {
    return type == InputDeviceType.bluetoothSco ||
        type == InputDeviceType.bluetoothA2dp ||
        type == InputDeviceType.bluetoothLe;
  }

  /// 外部マイク（USB, Bluetooth, 有線）かどうか
  static bool isExternalType(InputDeviceType type) {
    return type != InputDeviceType.builtIn && type != InputDeviceType.unknown;
  }
}

/// 音声入力デバイス管理サービス
class AudioDeviceService {
  static const _prefSettingsKey = 'kotobalog_mic_settings';
  static const _channel = MethodChannel('com.ompugao.kotobalog/audio_devices');

  /// Androidネイティブから AudioDeviceInfo.getAddress() を含む詳細情報を取得
  static Future<Map<String, Map<String, dynamic>>> fetchNativeDeviceDetails() async {
    if (kIsWeb) return const {};
    try {
      final res = await _channel.invokeMethod<dynamic>('getMicrophoneDetails');
      if (res is Map && res['devices'] is Map) {
        final rawDevices = res['devices'] as Map;
        final map = <String, Map<String, dynamic>>{};
        rawDevices.forEach((key, value) {
          if (value is Map) {
            map[key.toString()] = Map<String, dynamic>.from(value);
          }
        });
        return map;
      }
    } catch (e) {
      debugPrint('AudioDeviceService: fetchNativeDeviceDetails: $e');
    }
    return const {};
  }

  /// 利用可能な入力デバイスを詳細情報付きで取得
  static Future<List<EnrichedAudioDevice>> listEnrichedDevices(
      AudioRecorder recorder) async {
    try {
      final devices = await recorder.listInputDevices();
      final nativeDetails = await fetchNativeDeviceDetails();
      return enrichDevices(devices, nativeDetails);
    } catch (e) {
      debugPrint('AudioDeviceService: listEnrichedDevices error: $e');
      return const [];
    }
  }

  /// InputDevice 一覧に getAddress() などの物理位置や子どもの声向きの判定を付加
  static List<EnrichedAudioDevice> enrichDevices(
    List<InputDevice> devices,
    Map<String, Map<String, dynamic>> nativeDetails,
  ) {
    final result = <EnrichedAudioDevice>[];
    final builtInList =
        devices.where((d) => d.type == InputDeviceType.builtIn).toList();
    int builtInIndex = 0;

    for (final dev in devices) {
      final detail = nativeDetails[dev.id];
      final address = (detail?['address'] as String?)?.trim() ?? '';
      final lowerAddress = address.toLowerCase();

      if (dev.type == InputDeviceType.builtIn) {
        builtInIndex++;
        final isBottom = lowerAddress.contains('bottom') ||
            (address.isEmpty && builtInIndex == 1);
        final isBack = lowerAddress.contains('back');
        final isTop = lowerAddress.contains('top');

        String posLabel = 'メイン';
        bool isRec = true;
        String reason = '子どもの声・日常会話の録音用';

        if (isBottom) {
          posLabel = '底面';
          isRec = true;
          reason = '子どもの声・日常会話の録音用';
        } else if (isBack) {
          posLabel = '背面';
          isRec = false;
          reason = 'カメラ動画・環境音の録音用';
        } else if (isTop) {
          posLabel = '天面';
          isRec = false;
          reason = 'スピーカー通話・環境ノイズ低減用';
        } else if (builtInList.length > 1) {
          if (builtInIndex == 1) {
            posLabel = 'メイン';
            isRec = true;
            reason = '子どもの声・日常会話の録音用';
          } else {
            posLabel = 'サブ $builtInIndex';
            isRec = false;
            reason = '環境音・ノイズ低減用';
          }
        }

        result.add(EnrichedAudioDevice(
          device: dev,
          address: address.isNotEmpty ? address : null,
          positionLabel: posLabel,
          isRecommendedForChild: isRec,
          recommendationReason: reason,
        ));
      } else if (dev.type == InputDeviceType.usb) {
        result.add(EnrichedAudioDevice(
          device: dev,
          address: address.isNotEmpty ? address : null,
          positionLabel: 'USB外付けマイク',
          isRecommendedForChild: true,
          recommendationReason: '外付けマイク',
        ));
      } else if (AudioDeviceHelper.isBluetoothType(dev.type)) {
        result.add(EnrichedAudioDevice(
          device: dev,
          address: address.isNotEmpty ? address : null,
          positionLabel: 'Bluetooth接続',
          isRecommendedForChild: false,
          recommendationReason: 'イヤホンマイク',
        ));
      } else {
        result.add(EnrichedAudioDevice(
          device: dev,
          address: address.isNotEmpty ? address : null,
          positionLabel: '外部有線マイク',
          isRecommendedForChild: true,
          recommendationReason: '有線マイク',
        ));
      }
    }

    return result;
  }

  /// 利用可能な入力デバイスを安全に列挙
  static Future<List<InputDevice>> listDevices(AudioRecorder recorder) async {
    final enriched = await listEnrichedDevices(recorder);
    return enriched.map((e) => e.device).toList();
  }

  /// Android 12+ の Bluetooth 権限をチェック・要求
  static Future<bool> ensureBluetoothPermission() async {
    if (kIsWeb) return true;
    try {
      final status = await Permission.bluetoothConnect.status;
      if (status.isGranted) return true;
      final requested = await Permission.bluetoothConnect.request();
      return requested.isGranted;
    } catch (e) {
      debugPrint('AudioDeviceService: bluetoothConnect request error: $e');
      return false;
    }
  }

  /// 設定と接続中のデバイス一覧から、録音に使用するデバイスを解決
  static InputDevice? resolveDevice({
    required List<EnrichedAudioDevice> enrichedDevices,
    required AudioDeviceSettings settings,
  }) {
    if (enrichedDevices.isEmpty) return null;

    final availableDevices = enrichedDevices.map((e) => e.device).toList();

    switch (settings.mode) {
      case AudioDeviceMode.preferBuiltIn:
        // 1. 子どもの声に適したメイン/底面内蔵マイクを最優先
        for (final enriched in enrichedDevices) {
          if (enriched.device.type == InputDeviceType.builtIn &&
              enriched.isRecommendedForChild) {
            return enriched.device;
          }
        }
        // 2. その他の内蔵マイク
        for (final dev in availableDevices) {
          if (dev.type == InputDeviceType.builtIn) {
            return dev;
          }
        }
        return null;

      case AudioDeviceMode.preferExternal:
        // 1. USBマイク（DJI Mic等）を最優先
        for (final dev in availableDevices) {
          if (dev.type == InputDeviceType.usb) {
            return dev;
          }
        }
        // 2. Bluetooth機器
        for (final dev in availableDevices) {
          if (AudioDeviceHelper.isBluetoothType(dev.type)) {
            return dev;
          }
        }
        // 3. 有線ヘッドセット
        for (final dev in availableDevices) {
          if (dev.type == InputDeviceType.wiredHeadset ||
              dev.type == InputDeviceType.lineIn) {
            return dev;
          }
        }
        // 外部マイクがなければ推奨内蔵マイク
        for (final enriched in enrichedDevices) {
          if (enriched.device.type == InputDeviceType.builtIn &&
              enriched.isRecommendedForChild) {
            return enriched.device;
          }
        }
        return null;

      case AudioDeviceMode.specific:
        // ID一致
        if (settings.preferredDeviceId != null) {
          for (final dev in availableDevices) {
            if (dev.id == settings.preferredDeviceId) {
              return dev;
            }
          }
        }
        // ラベル一致（再接続でIDが変わった場合）
        if (settings.preferredDeviceLabel != null) {
          for (final dev in availableDevices) {
            if (dev.label == settings.preferredDeviceLabel) {
              return dev;
            }
          }
        }
        // 指定デバイスが見つからない場合は推奨内蔵マイク
        for (final enriched in enrichedDevices) {
          if (enriched.device.type == InputDeviceType.builtIn &&
              enriched.isRecommendedForChild) {
            return enriched.device;
          }
        }
        return null;

      case AudioDeviceMode.auto:
        // システム既定 (device = null)
        return null;
    }
  }

  /// 設定を読み込み
  static Future<AudioDeviceSettings> loadSettings() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final jsonStr = prefs.getString(_prefSettingsKey);
      if (jsonStr != null && jsonStr.isNotEmpty) {
        final decoded = jsonDecode(jsonStr);
        if (decoded is Map<String, dynamic>) {
          return AudioDeviceSettings.fromMap(decoded);
        }
      }
    } catch (e) {
      debugPrint('AudioDeviceService: loadSettings error: $e');
    }
    return const AudioDeviceSettings();
  }

  /// 設定を保存
  static Future<void> saveSettings(AudioDeviceSettings settings) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final jsonStr = jsonEncode(settings.toMap());
      await prefs.setString(_prefSettingsKey, jsonStr);
    } catch (e) {
      debugPrint('AudioDeviceService: saveSettings error: $e');
    }
  }
}
