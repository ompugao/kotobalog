import 'package:flutter_test/flutter_test.dart';
import 'package:record/record.dart';
import 'package:kotobalog/services/audio_device_service.dart';

void main() {
  group('AudioDeviceHelper tests', () {
    test('identifies bluetooth and external device types correctly', () {
      expect(AudioDeviceHelper.isBluetoothType(InputDeviceType.bluetoothSco), isTrue);
      expect(AudioDeviceHelper.isBluetoothType(InputDeviceType.bluetoothLe), isTrue);
      expect(AudioDeviceHelper.isBluetoothType(InputDeviceType.bluetoothA2dp), isTrue);
      expect(AudioDeviceHelper.isBluetoothType(InputDeviceType.builtIn), isFalse);
      expect(AudioDeviceHelper.isBluetoothType(InputDeviceType.usb), isFalse);

      expect(AudioDeviceHelper.isExternalType(InputDeviceType.usb), isTrue);
      expect(AudioDeviceHelper.isExternalType(InputDeviceType.bluetoothSco), isTrue);
      expect(AudioDeviceHelper.isExternalType(InputDeviceType.wiredHeadset), isTrue);
      expect(AudioDeviceHelper.isExternalType(InputDeviceType.builtIn), isFalse);
    });

    test('formats device names user-friendly with address and position', () {
      const builtInBottom = InputDevice(
        id: '1',
        label: 'Built-in microphone',
        type: InputDeviceType.builtIn,
      );
      expect(
        AudioDeviceHelper.formatDeviceName(builtInBottom, address: 'bottom'),
        '端末の内蔵マイク（底面）',
      );

      const builtInBack = InputDevice(
        id: '2',
        label: 'Built-in microphone',
        type: InputDeviceType.builtIn,
      );
      expect(
        AudioDeviceHelper.formatDeviceName(builtInBack, address: 'back'),
        '端末の内蔵マイク（背面）',
      );

      const djiMic = InputDevice(
        id: '3',
        label: 'DJI Mic Receiver',
        type: InputDeviceType.usb,
      );
      expect(AudioDeviceHelper.formatDeviceName(djiMic), 'DJI Mic Receiver');

      const airPods = InputDevice(
        id: '4',
        label: 'AirPods Pro',
        type: InputDeviceType.bluetoothSco,
      );
      expect(AudioDeviceHelper.formatDeviceName(airPods), 'AirPods Pro');
    });
  });

  group('AudioDeviceSettings serialization', () {
    test('serializes and deserializes correctly', () {
      const settings = AudioDeviceSettings(
        mode: AudioDeviceMode.specific,
        preferredDeviceId: 'dev-123',
        preferredDeviceLabel: 'DJI Mic',
        filterChildSuitableOnly: true,
      );

      final map = settings.toMap();
      final restored = AudioDeviceSettings.fromMap(map);

      expect(restored.mode, AudioDeviceMode.specific);
      expect(restored.preferredDeviceId, 'dev-123');
      expect(restored.preferredDeviceLabel, 'DJI Mic');
      expect(restored.filterChildSuitableOnly, isTrue);
    });

    test('copyWith handles clearSpecificDevice and filterChildSuitableOnly', () {
      const settings = AudioDeviceSettings(
        mode: AudioDeviceMode.specific,
        preferredDeviceId: 'dev-123',
        preferredDeviceLabel: 'DJI Mic',
        filterChildSuitableOnly: false,
      );

      final updated = settings.copyWith(
        mode: AudioDeviceMode.preferBuiltIn,
        clearSpecificDevice: true,
        filterChildSuitableOnly: true,
      );

      expect(updated.mode, AudioDeviceMode.preferBuiltIn);
      expect(updated.preferredDeviceId, isNull);
      expect(updated.preferredDeviceLabel, isNull);
      expect(updated.filterChildSuitableOnly, isTrue);
    });
  });

  group('AudioDeviceService.enrichDevices tests', () {
    test('identifies bottom, back, and top mics using getAddress()', () {
      const mic1 = InputDevice(
        id: '8',
        label: 'Built-in microphone',
        type: InputDeviceType.builtIn,
      );
      const mic2 = InputDevice(
        id: '9',
        label: 'Built-in microphone',
        type: InputDeviceType.builtIn,
      );
      const micUsb = InputDevice(
        id: '10',
        label: 'DJI Mic Receiver',
        type: InputDeviceType.usb,
      );

      final nativeDetails = {
        '8': {'id': '8', 'address': 'bottom'},
        '9': {'id': '9', 'address': 'back'},
        '10': {'id': '10', 'address': ''},
      };

      final enriched = AudioDeviceService.enrichDevices(
        [mic1, mic2, micUsb],
        nativeDetails,
      );

      expect(enriched.length, 3);

      // 底面メインマイク: 子どもの声に最適
      expect(enriched[0].address, 'bottom');
      expect(enriched[0].positionLabel, '底面');
      expect(enriched[0].isRecommendedForChild, isTrue);
      expect(enriched[0].displayName, '端末の内蔵マイク（底面）');
      expect(enriched[0].recommendationReason.contains('（'), isFalse);

      // 背面マイク: カメラ・環境音用
      expect(enriched[1].address, 'back');
      expect(enriched[1].positionLabel, '背面');
      expect(enriched[1].isRecommendedForChild, isFalse);
      expect(enriched[1].displayName, '端末の内蔵マイク（背面）');
      expect(enriched[1].recommendationReason.contains('（'), isFalse);

      // DJI Mic
      expect(enriched[2].isRecommendedForChild, isTrue);
      expect(enriched[2].displayName, 'DJI Mic Receiver');
      expect(enriched[2].recommendationReason.contains('（'), isFalse);
    });
  });

  group('AudioDeviceService.resolveDevice tests', () {
    const bottomMic = EnrichedAudioDevice(
      device: InputDevice(
        id: 'mic-bottom',
        label: 'Built-in Mic',
        type: InputDeviceType.builtIn,
      ),
      address: 'bottom',
      positionLabel: '底面 / メイン',
      isRecommendedForChild: true,
    );

    const backMic = EnrichedAudioDevice(
      device: InputDevice(
        id: 'mic-back',
        label: 'Built-in Mic',
        type: InputDeviceType.builtIn,
      ),
      address: 'back',
      positionLabel: '背面 / カメラ用',
      isRecommendedForChild: false,
    );

    const djiUsb = EnrichedAudioDevice(
      device: InputDevice(
        id: 'mic-dji',
        label: 'DJI Mic Receiver',
        type: InputDeviceType.usb,
      ),
      isRecommendedForChild: true,
    );

    const btHeadset = EnrichedAudioDevice(
      device: InputDevice(
        id: 'mic-bt',
        label: 'Pixel Buds',
        type: InputDeviceType.bluetoothSco,
      ),
      isRecommendedForChild: false,
    );

    final allDevices = [bottomMic, backMic, djiUsb, btHeadset];

    test('auto mode returns null for system default', () {
      final res = AudioDeviceService.resolveDevice(
        enrichedDevices: allDevices,
        settings: const AudioDeviceSettings(mode: AudioDeviceMode.auto),
      );
      expect(res, isNull);
    });

    test('preferBuiltIn mode selects bottom (child-recommended) mic over back mic', () {
      // backMic が先頭にあるリストでも bottomMic を優先選択することを確認
      final res = AudioDeviceService.resolveDevice(
        enrichedDevices: [backMic, bottomMic, djiUsb],
        settings: const AudioDeviceSettings(mode: AudioDeviceMode.preferBuiltIn),
      );
      expect(res, isNotNull);
      expect(res?.id, 'mic-bottom');
    });

    test('preferExternal mode prioritizes USB (DJI Mic) over Bluetooth and Built-in', () {
      final res = AudioDeviceService.resolveDevice(
        enrichedDevices: allDevices,
        settings: const AudioDeviceSettings(mode: AudioDeviceMode.preferExternal),
      );
      expect(res, isNotNull);
      expect(res?.id, 'mic-dji');
    });

    test('specific mode matches device by id', () {
      final res = AudioDeviceService.resolveDevice(
        enrichedDevices: allDevices,
        settings: const AudioDeviceSettings(
          mode: AudioDeviceMode.specific,
          preferredDeviceId: 'mic-bt',
          preferredDeviceLabel: 'Pixel Buds',
        ),
      );
      expect(res, isNotNull);
      expect(res?.id, 'mic-bt');
    });

    test('returns null when enrichedDevices is empty', () {
      final res = AudioDeviceService.resolveDevice(
        enrichedDevices: [],
        settings: const AudioDeviceSettings(mode: AudioDeviceMode.preferBuiltIn),
      );
      expect(res, isNull);
    });
  });
}
