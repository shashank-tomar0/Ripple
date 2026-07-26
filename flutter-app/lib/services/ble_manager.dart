// BLE Manager — high-level integration of BLE transport with the Ripple mesh.
//
// This class coordinates between:
//   - BLETransportService (low-level BLE: advertising, scanning, connections)
//   - DaemonService (WebSocket bridge to the Go daemon)
//
// Data flow:
//   BLE peer → BLETransportService → onDataReceived → BLEManager → daemon.sendRaw()
//   daemon.onBLEData → BLEManager → BLETransportService.sendData()

library;

import 'dart:async';
import 'dart:typed_data';
import 'package:flutter/foundation.dart';
import 'ble_transport_service.dart';
import 'daemon_service.dart';

/// High-level BLE mesh transport manager.
///
/// Integrates the low-level BLE transport with the Ripple mesh via the daemon.
/// Handles starting/stopping the transport, routing data between BLE and the
/// mesh, and exposing BLE state to the UI.
class BLEManager {
  final BLETransportService _transport;
  final DaemonService _daemon;

  StreamSubscription<Uint8List>? _daemonBLEDataSub;
  bool _started = false;

  BLETransportService get transport => _transport;
  bool get started => _started;

  /// Stream of BLE transport state changes (enabled/disabled).
  Stream<bool> get onBLETransportState => _transportStateController.stream;
  final StreamController<bool> _transportStateController = StreamController<bool>.broadcast();

  BLEManager(this._daemon) : _transport = BLETransportService();

  /// Start the BLE mesh transport.
  ///
  /// Initializes BLE, starts advertising as [localPeerId] with [nickname],
  /// and begins scanning for nearby Ripple nodes.
  ///
  /// Returns `true` on success, `false` if BLE is unavailable.
  Future<bool> start(String localPeerId, String nickname) async {
    if (_started) {
      debugPrint('BLEManager: already started');
      return true;
    }

    final ok = await _transport.initialize();
    if (!ok) {
      debugPrint('BLEManager: BLE initialization failed');
      return false;
    }

    // Data received from BLE peer → forward into mesh via daemon.
    _transport.onDataReceived = (peerId, data) {
      debugPrint('BLEManager: received ${data.length} bytes from $peerId');
      _daemon.sendRaw(data);
    };

    // Data received from mesh (via daemon) → forward to BLE peer(s).
    _daemonBLEDataSub = _daemon.onBLEData.listen((data) {
      // Broadcast to all connected BLE peers.
      for (final peerId in _transport.connectedPeerIds) {
        _transport.sendData(peerId, data);
      }
    });

    // Start advertising and scanning.
    await Future.wait([
      _transport.startAdvertising(localPeerId, nickname),
      _transport.startScanning(),
    ]);

    _started = true;
    _transportStateController.add(true);
    debugPrint('BLEManager: started');
    return true;
  }

  /// Stop the BLE mesh transport.
  Future<void> stop() async {
    if (!_started) return;

    await _daemonBLEDataSub?.cancel();
    _daemonBLEDataSub = null;

    await _transport.dispose();
    _started = false;
    _transportStateController.add(false);
    debugPrint('BLEManager: stopped');
  }

  /// Send raw [data] to a specific BLE [peerId].
  ///
  /// Returns `true` if the peer is connected and data was written.
  Future<bool> sendTo(String peerId, List<int> data) {
    return _transport.sendData(peerId, Uint8List.fromList(data));
  }

  /// Dispose all resources.
  void dispose() {
    stop();
    _transportStateController.close();
  }
}