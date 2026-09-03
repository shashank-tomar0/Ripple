import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

class ForegroundService {
  static const _channel = MethodChannel('com.ripple.app/mesh_service');

  static Future<bool> start() async {
    try {
      await _channel.invokeMethod('startService');
      return true;
    } catch (e) {
      debugPrint('ForegroundService start error: $e');
      return false;
    }
  }

  static Future<bool> stop() async {
    try {
      await _channel.invokeMethod('stopService');
      return true;
    } catch (e) {
      debugPrint('ForegroundService stop error: $e');
      return false;
    }
  }

  static Future<bool> isRunning() async {
    try {
      return await _channel.invokeMethod('isServiceRunning') ?? false;
    } catch (e) {
      return false;
    }
  }
}
