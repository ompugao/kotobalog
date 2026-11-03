package com.ompugao.kotobalog

import android.content.Context
import android.media.AudioDeviceInfo
import android.media.AudioManager
import android.media.MicrophoneInfo
import android.os.Build
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterActivity() {
    private val CHANNEL = "com.ompugao.kotobalog/audio_devices"

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)

        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, CHANNEL).setMethodCallHandler { call, result ->
            when (call.method) {
                "getMicrophoneDetails" -> {
                    try {
                        val audioManager = getSystemService(Context.AUDIO_SERVICE) as AudioManager
                        val devices = audioManager.getDevices(AudioManager.GET_DEVICES_INPUTS)
                        val deviceDetails = mutableMapOf<String, Map<String, Any?>>()

                        for (device in devices) {
                            val id = "${device.id}"
                            val address = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                                device.address ?: ""
                            } else {
                                ""
                            }

                            deviceDetails[id] = mapOf(
                                "id" to id,
                                "address" to address,
                                "type" to device.type,
                                "productName" to device.productName.toString()
                            )
                        }

                        val micInfos = mutableListOf<Map<String, Any?>>()
                        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                            try {
                                val microphones = audioManager.microphones
                                for (mic in microphones) {
                                    micInfos.add(mapOf(
                                        "id" to "${mic.id}",
                                        "description" to mic.description,
                                        "location" to mic.location, // 0 = LOCATION_MAIN, 1 = LOCATION_PERIPHERAL, 2 = LOCATION_UNKNOWN
                                        "group" to mic.group,
                                        "indexInTheGroup" to mic.indexInTheGroup
                                    ))
                                }
                            } catch (_: Exception) {}
                        }

                        result.success(mapOf(
                            "devices" to deviceDetails,
                            "microphones" to micInfos
                        ))
                    } catch (e: Exception) {
                        result.success(emptyMap<String, Any?>())
                    }
                }
                else -> result.notImplemented()
            }
        }
    }
}
