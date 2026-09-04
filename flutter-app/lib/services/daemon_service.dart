// Package services provides communication with the Ripple Go daemon.
// The only transport is the real WebSocket bridge — there is no fake or
// demo mode. If the daemon is unreachable the app reports disconnected;
// it never invents peers, messages, or receipts.
library;

import 'dart:async';
import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'package:web_socket_channel/web_socket_channel.dart';
import '../models/message.dart';
import '../models/contact.dart';
import '../models/file_transfer.dart';
import '../models/delivery_receipt.dart';
import '../models/relay_event.dart';

/// Callback types for daemon events.
typedef MessageCallback = void Function(Message message);
typedef PeerCallback = void Function(Contact contact);
typedef ConnectionCallback = void Function(bool connected);
typedef DeliveryReceiptCallback = void Function(DeliveryReceipt receipt);

/// Abstract interface for daemon communication.
///
/// Note: the daemon deliberately has no "list peers/conversations/messages"
/// endpoints over the wire. Peer presence arrives as events, and message
/// history is derived locally by [AppState]. The interface therefore only
/// exposes what the daemon genuinely serves.
abstract class DaemonService {
  bool get isConnected;
  String get localPeerId;
  String get nickname;
  String get localPubKey; // hex-encoded Curve25519 public key

  Future<bool> connect({String? host, int? port});
  Future<void> disconnect();

  Future<bool> sendMessage(Message message);

  /// Sends our Curve25519 public key to [peerId] so E2E can be established.
  Future<bool> sendKeyExchange(String peerId);

  /// Requests the node's 24-word BIP39 backup phrase from the daemon.
  /// Returns null if the daemon is unreachable or refuses.
  Future<String?> exportSeed();

  Stream<Message> get onMessage;
  Stream<FileTransfer> get onFileTransfer;
  Stream<Contact> get onPeerJoined;
  Stream<Contact> get onPeerLeft;
  Stream<bool> get onConnectionState;
  Stream<DeliveryReceipt> get onDeliveryReceipt;
  Stream<RelayEvent> get onRelayEvent;
}

/// WebSocket implementation — connects to the Ripple Go daemon's bridge.
class WebSocketDaemonService extends DaemonService {
  final String _defaultHost;
  final int _defaultPort;

  /// [host]/[port] point at the Ripple Go daemon's WebSocket bridge.
  /// On the Android emulator the host machine is reachable at 10.0.2.2.
  WebSocketDaemonService({
    String host = 'localhost',
    int port = 9876,
  })  : _defaultHost = host,
        _defaultPort = port;

  WebSocketChannel? _channel;
  bool _connected = false;
  String _peerId = '';
  String _nick = '';
  String _pubKey = '';

  final _messageController = StreamController<Message>.broadcast();
  final _fileTransferController = StreamController<FileTransfer>.broadcast();
  final _peerJoinController = StreamController<Contact>.broadcast();
  final _peerLeaveController = StreamController<Contact>.broadcast();
  final _connectionController = StreamController<bool>.broadcast();
  final _deliveryReceiptController = StreamController<DeliveryReceipt>.broadcast();
  final _relayEventController = StreamController<RelayEvent>.broadcast();
  Completer<String>? _seedCompleter;


  @override
  bool get isConnected => _connected;

  @override
  String get localPeerId => _peerId;

  @override
  String get nickname => _nick;

  @override
  String get localPubKey => _pubKey;

  @override
  Future<bool> connect({String? host, int? port}) async {
    if (_connected && _channel != null) return true; // already connected
    try {
      final h = host ?? _defaultHost;
      final p = port ?? _defaultPort;
      final uri = Uri.parse('ws://$h:$p/ws');
      _channel = WebSocketChannel.connect(uri);
      await _channel!.ready;
      _connected = true;
      _connectionController.add(true);
      _listen();
      return true;
    } catch (e) {
      debugPrint('WebSocket connect failed: $e');
      _connected = false;
      _connectionController.add(false);
      return false;
    }
  }

  @override
  Future<void> disconnect() async {
    await _channel?.sink.close();
    _connected = false;
    _connectionController.add(false);
  }

  void _listen() {
    _channel!.stream.listen(
      (data) {
        try {
          final json = jsonDecode(data as String) as Map<String, dynamic>;
          _handleMessage(json);
        } catch (e) {
          debugPrint('WS parse error: $e');
        }
      },
      onError: (error) {
        debugPrint('WS error: $error');
        _connected = false;
        _connectionController.add(false);
      },
      onDone: () {
        _connected = false;
        _connectionController.add(false);
      },
    );
  }

  void _handleMessage(Map<String, dynamic> json) {
    final type = json['type'] as String?;

    switch (type) {
      case 'identity':
        _peerId = json['peer_id'] as String? ?? '';
        _nick = json['nickname'] as String? ?? '';
        _pubKey = json['public_key'] as String? ?? '';
        break;
      case 'chat':
      case 'file':
      case 'sos':
        _messageController.add(Message.fromJson(json));
        break;
      case 'file_meta':
      case 'file_progress':
      case 'file_complete':
      case 'file_error':
        _fileTransferController.add(FileTransfer.fromJson(json));
        break;
      case 'peer_join':
        _peerJoinController.add(Contact.fromJson(json['peer'] as Map<String, dynamic>));
        break;
      case 'peer_leave':
        _peerLeaveController.add(Contact.fromJson(json['peer'] as Map<String, dynamic>));
        break;
      case 'delivery_ack':
        _deliveryReceiptController.add(DeliveryReceipt.fromJson(json));
        break;
      case 'relay':
        _relayEventController.add(RelayEvent.fromJson(json));
        break;
      case 'seed_export_reply':
        _seedCompleter?.complete(json['mnemonic'] as String? ?? '');
        _seedCompleter = null;
        break;
      case 'error':
        _seedCompleter?.completeError(
          Exception(json['payload'] as String? ?? 'daemon error'),
        );
        _seedCompleter = null;
        break;
    }
  }

  @override
  Future<bool> sendMessage(Message message) async {
    if (!_connected || _channel == null) return false;
    try {
      _channel!.sink.add(jsonEncode(message.toJson()));
      return true;
    } catch (e) {
      debugPrint('Send error: $e');
      return false;
    }
  }

  @override
  Future<bool> sendKeyExchange(String peerId) async {
    if (!_connected || _channel == null || peerId.isEmpty) return false;
    try {
      _channel!.sink.add(jsonEncode({
        'type': 'key_exchange',
        'recipient': peerId,
      }));
      return true;
    } catch (e) {
      debugPrint('Key exchange send error: $e');
      return false;
    }
  }

  @override
  Future<String?> exportSeed() async {
    if (!_connected || _channel == null) return null;
    if (_seedCompleter != null) return null; // a request is already in flight

    final completer = Completer<String>();
    _seedCompleter = completer;
    try {
      _channel!.sink.add(jsonEncode({'type': 'seed_export'}));
      return await completer.future.timeout(const Duration(seconds: 5));
    } catch (e) {
      debugPrint('Seed export failed: $e');
      return null;
    } finally {
      if (_seedCompleter == completer) _seedCompleter = null;
    }
  }

  @override
  Stream<Message> get onMessage => _messageController.stream;

  @override
  Stream<FileTransfer> get onFileTransfer => _fileTransferController.stream;

  @override
  Stream<Contact> get onPeerJoined => _peerJoinController.stream;

  @override
  Stream<Contact> get onPeerLeft => _peerLeaveController.stream;

  @override
  Stream<bool> get onConnectionState => _connectionController.stream;

  @override
  Stream<DeliveryReceipt> get onDeliveryReceipt =>
      _deliveryReceiptController.stream;

  @override
  Stream<RelayEvent> get onRelayEvent => _relayEventController.stream;
}
