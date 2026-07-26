// BLE Transport Service — direct device-to-device communication via Bluetooth Low Energy.
// Ripple uses a fixed set of UUIDs so all mesh nodes can discover each other.
//
// Architecture:
//   Flutter owns the BLE stack (flutter_blue_plus). When bytes arrive from a BLE peer,
//   they are forwarded to the Go daemon via the WebSocket bridge as a special message type.
//   The daemon treats the data as if it arrived from a libp2p transport.
//
// UUIDs (based on the Nordic UART Service convention):
//   SERVICE_UUID — primary BLE service for Ripple mesh transport
//   TX_CHAR_UUID — characteristic for *sending* data to the peer (we write here)
//   RX_CHAR_UUID — characteristic for *receiving* data from the peer (we subscribe here)

library;

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';
import 'package:flutter/foundation.dart';
import 'package:flutter_blue_plus/flutter_blue_plus.dart';

/// Service UUID for Ripple's BLE mesh transport.
const String kRippleBLEServiceUuid = '6e400001-b5a3-f393-e0a9-e50e24dcca9e';

/// TX characteristic — used to send data TO a connected peer.
const String kRippleBLETxCharUuid = '6e400002-b5a3-f393-e0a9-e50e24dcca9e';

/// RX characteristic — used to receive data FROM a connected peer.
const String kRippleBLERxCharUuid = '6e400003-b5a3-f393-e0a9-e50e24dcca9e';

/// Manufacturer ID reserved for development (0xFFFF).
const int kRippleManufacturerId = 0xFFFF;

/// Maximum advertisement interval for scanning (15 seconds).
const Duration kScanTimeout = Duration(seconds: 15);

/// Low-level BLE transport that handles advertising, scanning, connecting,
/// and data exchange with nearby Ripple mesh nodes.
class BLETransportService {
  bool _isAdvertising = false;
  bool _isScanning = false;
  StreamSubscription<List<ScanResult>>? _scanSubscription;

  /// Connected peripherals keyed by remote device ID.
  final Map<String, BluetoothDevice> _connectedDevices = {};

  /// Stream controller for BLE transport state changes (advertising + scanning).
  final _bleTransportStateController = StreamController<bool>.broadcast();

  // ── Callbacks ──

  /// Fired when raw data arrives from a connected BLE peer.
  void Function(String peerId, Uint8List data)? onDataReceived;

  /// Fired when a new BLE peer is discovered and connected.
  void Function(String peerId)? onPeerDiscovered;

  /// Fired when a BLE peer disconnects.
  void Function(String peerId)? onPeerDisconnected;

  // ── Getters ──

  bool get isAdvertising => _isAdvertising;
  bool get isScanning => _isScanning;
  int get connectedCount => _connectedDevices.length;
  List<String> get connectedPeerIds => _connectedDevices.keys.toList();

  // ── Initialization ──

  /// Initialise BLE on the device.
  ///
  /// Returns `true` if Bluetooth is available and ready. If Bluetooth is
  /// currently off, a request to turn it on is issued.
  Future<bool> initialize() async {
    try {
      var state = await FlutterBluePlus.adapterState.first;
      if (state == BluetoothAdapterState.off) {
        await FlutterBluePlus.turnOn();
        // Wait for the adapter to actually come on.
        state = await FlutterBluePlus.adapterState
            .firstWhere((s) => s == BluetoothAdapterState.on);
      }
      return state == BluetoothAdapterState.on;
    } catch (e) {
      debugPrint('BLE init error: $e');
      return false;
    }
  }

  // ── Advertising ──

  /// Start BLE advertising so nearby Ripple devices can discover us.
  ///
  /// [localPeerId] is included in manufacturer data so peers can identify
  /// the node without connecting first.
  Future<void> startAdvertising(String localPeerId, String nickname) async {
    if (_isAdvertising) {
      debugPrint('BLE: already advertising');
      return;
    }

    try {
      final manufacturerPayload = utf8.encode('ripple:$localPeerId:$nickname');

      await FlutterBluePlus.startAdvertising(
        advertiseMode: AdvertiseMode.balanced,
        txPowerLevel: TransmitPowerLevel.medium,
        serviceUuid: BluetoothUuid(kRippleBLEServiceUuid),
        manufacturerData: ManufacturerData(
          id: kRippleManufacturerId,
          data: Uint8List.fromList(manufacturerPayload),
        ),
      );

      _isAdvertising = true;
      debugPrint('BLE: started advertising as $localPeerId');
    } catch (e) {
      debugPrint('BLE advertising error: $e');
    }
  }

  /// Stop advertising.
  Future<void> stopAdvertising() async {
    if (!_isAdvertising) return;
    try {
      await FlutterBluePlus.stopAdvertising();
      _isAdvertising = false;
      debugPrint('BLE: stopped advertising');
    } catch (e) {
      debugPrint('BLE stop advertising error: $e');
    }
  }

  // ── Scanning ──

  /// Start scanning for nearby Ripple BLE devices.
  ///
  /// Only devices advertising the Ripple BLE service UUID are discovered.
  /// Each discovered device is automatically connected via [connectToDevice].
  Future<void> startScanning() async {
    if (_isScanning) {
      debugPrint('BLE: already scanning');
      return;
    }

    _isScanning = true;

    _scanSubscription = FlutterBluePlus.scanResults.listen((results) {
      for (final result in results) {
        _handleScanResult(result);
      }
    });

    try {
      await FlutterBluePlus.startScan(
        withServices: [BluetoothUuid(kRippleBLEServiceUuid)],
        timeout: kScanTimeout,
      );
    } catch (e) {
      debugPrint('BLE scan error: $e');
    }

    _isScanning = false;
  }

  /// Stop scanning for BLE devices.
  Future<void> stopScanning() async {
    try {
      await FlutterBluePlus.stopScan();
    } catch (e) {
      debugPrint('BLE stop scan error: $e');
    }
    _isScanning = false;
    await _scanSubscription?.cancel();
    _scanSubscription = null;
  }

  void _handleScanResult(ScanResult result) {
    final device = result.device;
    final peerId = device.remoteId.str;

    if (peerId.isEmpty) return;
    if (_connectedDevices.containsKey(peerId)) return;
    if (device.remoteId.str ==
        FlutterBluePlus.instanceId.toString()) {
      debugPrint('BLE: skipping self-discovery');
      return;
    }

    // Parse manufacturer data for peer display info (used for logging).
    final md = result.advertisementData.manufacturerData;
    for (final entry in md.entries) {
      try {
        final info = utf8.decode(entry.value);
        debugPrint('BLE: discovered device $peerId ($info)');
      } catch (_) {
        // Non-UTF8 manufacturer data — skip gracefully.
      }
    }

    // Automatically connect to discovered peers.
    connectToDevice(device);
    onPeerDiscovered?.call(peerId);
  }

  // ── Connection Management ──

  /// Connect to a discovered BLE device and subscribe to its RX characteristic.
  Future<bool> connectToDevice(BluetoothDevice device) async {
    final peerId = device.remoteId.str;
    debugPrint('BLE: connecting to $peerId');

    try {
      await device.connect();
      _connectedDevices[peerId] = device;
      debugPrint('BLE: connected to $peerId');

      // Discover all services on the device.
      await device.discoverServices();

      // Find the Ripple service and subscribe to the RX characteristic.
      for (final service in device.services) {
        if (service.uuid.toString().toUpperCase() ==
            kRippleBLEServiceUuid.toUpperCase()) {
          for (final char in service.characteristics) {
            if (char.uuid.toString().toUpperCase() ==
                kRippleBLERxCharUuid.toUpperCase()) {
              await char.setNotifyValue(true);
              char.onValueReceived.listen((data) {
                onDataReceived?.call(peerId, data);
              });
              debugPrint('BLE: subscribed to RX on $peerId');
            }
          }
        }
      }

      // Handle disconnection.
      device.onDisconnected.listen((_) {
        _connectedDevices.remove(peerId);
        onPeerDisconnected?.call(peerId);
        debugPrint('BLE: disconnected from $peerId');
      });

      return true;
    } catch (e) {
      debugPrint('BLE connect error ($peerId): $e');
      return false;
    }
  }

  /// Send raw [data] to a connected BLE peer identified by [peerId].
  ///
  /// Returns `true` on success, `false` if the peer is not connected or the
  /// write fails.
  Future<bool> sendData(String peerId, Uint8List data) async {
    final device = _connectedDevices[peerId];
    if (device == null) {
      debugPrint('BLE send: $peerId not connected');
      return false;
    }

    try {
      // Re-discover services if needed (they may have been cached).
      final services = await device.discoverServices();
      for (final service in services) {
        if (service.uuid.toString().toUpperCase() ==
            kRippleBLEServiceUuid.toUpperCase()) {
          for (final char in service.characteristics) {
            if (char.uuid.toString().toUpperCase() ==
                kRippleBLETxCharUuid.toUpperCase()) {
              await char.write(data);
              return true;
            }
          }
        }
      }
      debugPrint('BLE send: TX characteristic not found on $peerId');
      return false;
    } catch (e) {
      debugPrint('BLE send error ($peerId): $e');
      return false;
    }
  }

  /// Disconnect from a specific [peerId].
  Future<void> disconnect(String peerId) async {
    final device = _connectedDevices[peerId];
    if (device != null) {
      try {
        await device.disconnect();
      } catch (e) {
        debugPrint('BLE disconnect error ($peerId): $e');
      }
      _connectedDevices.remove(peerId);
    }
  }

  /// Disconnect all peers and stop advertising and scanning.
  Future<void> dispose() async {
    await stopAdvertising();
    await stopScanning();

    final devices = _connectedDevices.values.toList();
    for (final device in devices) {
      try {
        await device.disconnect();
      } catch (e) {
        debugPrint('BLE dispose disconnect error: $e');
      }
    }
    _connectedDevices.clear();
    debugPrint('BLE: disposed');
  }
}
