package com.ripple.app

import android.content.Intent
import android.os.Build
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterActivity() {
    private val CHANNEL = "com.ripple.app/mesh_service"

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)

        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, CHANNEL).setMethodCallHandler { call, result ->
            when (call.method) {
                "startService" -> {
                    val intent = Intent(this, MeshForegroundService::class.java)
                    intent.action = MeshForegroundService.ACTION_START
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                        startForegroundService(intent)
                    } else {
                        startService(intent)
                    }
                    result.success(true)
                }
                "stopService" -> {
                    val intent = Intent(this, MeshForegroundService::class.java)
                    intent.action = MeshForegroundService.ACTION_STOP
                    startService(intent)
                    result.success(true)
                }
                "isServiceRunning" -> {
                    result.success(false) // simplified; real impl would check service state
                }
                else -> result.notImplemented()
            }
        }
    }
}
