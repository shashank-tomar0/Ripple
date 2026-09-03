// Package models defines the data types shared across the Ripple Flutter app.
// These mirror the Go daemon's message types for seamless serialization.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';
import 'package:flutter/foundation.dart';
import 'package:web_socket_channel/web_socket_channel.dart';
import '../utils/time.dart';
import '../models/message.dart';
import '../models/contact.dart';
import '../models/file_transfer.dart';
import '../models/delivery_receipt.dart';

/// Callback types for daemon events.
typedef MessageCallback = void Function(Message message);
typedef PeerCallback = void Function(Contact contact);
typedef ConnectionCallback = void Function(bool connected);
typedef DeliveryReceiptCallback = void Function(DeliveryReceipt receipt);

/// Abstract interface for daemon communication.
abstract class DaemonService {
  bool get isConnected;
  String get localPeerId;
  String get nickname;
  String get localPubKey; // hex-encoded Curve25519 public key

  Future<bool> connect({String host = 'localhost', int port = 9876});
  Future<void> disconnect();

  Future<bool> sendMessage(Message message);
  Future<List<Contact>> getPeers();
  Future<List<Conversation>> getConversations();
  Future<List<Message>> getMessages(String peerId);

  Stream<Message> get onMessage;
  Stream<FileTransfer> get onFileTransfer;
  Stream<Contact> get onPeerJoined;
  Stream<Contact> get onPeerLeft;
  Stream<bool> get onConnectionState;
  Stream<DeliveryReceipt> get onDeliveryReceipt;

  /// Send raw bytes into the mesh (used by BLE relay and other transports).
  /// The data is wrapped in a `ble_data` message and sent to the daemon.
  void sendRaw(Uint8List data);

  /// Stream of raw bytes received from the mesh that should be forwarded
  /// to a local transport (e.g. BLE). Each event is a raw payload destined
  /// for a local peer.
  Stream<Uint8List> get onBLEData;

  /// Stream indicating the BLE transport connection state between the
  /// Flutter app and the daemon.
  Stream<bool> get onBLETransportState;
}

/// WebSocket implementation — connects to the Ripple Go daemon.
class WebSocketDaemonService extends DaemonService {
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

  // BLE relay controllers.
  final _bleDataController = StreamController<Uint8List>.broadcast();
  final _bleTransportStateController = StreamController<bool>.broadcast();

  @override
  bool get isConnected => _connected;

  @override
  String get localPeerId => _peerId;

  @override
  String get nickname => _nick;

  @override
  String get localPubKey => _pubKey;

  @override
  Stream<FileTransfer> get onFileTransfer => _fileTransferController.stream;

  @override
  Stream<DeliveryReceipt> get onDeliveryReceipt => _deliveryReceiptController.stream;

  @override
  Future<bool> connect({String host = 'localhost', int port = 9876}) async {
    try {
      final uri = Uri.parse('ws://$host:$port/ws');
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
      case 'ble_data':
        // BLE data relayed from the daemon (received from another mesh peer).
        final payload = json['payload'] as String?;
        if (payload != null) {
          final bytes = base64Decode(payload);
          _bleDataController.add(bytes);
        }
        break;
      case 'ble_transport_state':
        // Daemon reports BLE transport state change.
        final enabled = json['enabled'] as bool? ?? false;
        _bleTransportStateController.add(enabled);
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
  void sendRaw(Uint8List data) {
    if (!_connected || _channel == null) return;
    try {
      final msg = {
        'type': 'ble_data',
        'payload': base64Encode(data),
      };
      _channel!.sink.add(jsonEncode(msg));
    } catch (e) {
      debugPrint('BLE sendRaw error: $e');
    }
  }

  @override
  Future<List<Contact>> getPeers() async => [];

  @override
  Future<List<Conversation>> getConversations() async => [];

  @override
  Future<List<Message>> getMessages(String peerId) async => [];

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
  Stream<DeliveryReceipt> get onDeliveryReceipt => _deliveryReceiptController.stream;

  @override
  Stream<Uint8List> get onBLEData => _bleDataController.stream;

  @override
  Stream<bool> get onBLETransportState => _bleTransportStateController.stream;
}

/// Local demo service — generates fake messages for UI development.
class LocalDaemonService extends DaemonService {
  bool _connected = false;
  final _messageController = StreamController<Message>.broadcast();
  final _fileTransferController = StreamController<FileTransfer>.broadcast();
  final _peerJoinController = StreamController<Contact>.broadcast();
  final _peerLeaveController = StreamController<Contact>.broadcast();
  final _connectionController = StreamController<bool>.broadcast();
  final _deliveryReceiptController = StreamController<DeliveryReceipt>.broadcast();
  final _bleDataController = StreamController<Uint8List>.broadcast();
  final _bleTransportStateController = StreamController<bool>.broadcast();

  final _contacts = <Contact>[
    Contact(peerId: '12D3KooW9a…v1x2', nickname: 'Alice', isOnline: true, hopCount: 0),
    Contact(peerId: '12D3KooW8b…q3w4', nickname: 'Bob', isOnline: true, hopCount: 1),
    Contact(peerId: '12D3KooW7c…r5t6', nickname: 'Carol', isOnline: false, hopCount: 2),
  ];

  final _messages = <String, List<Message>>{};
  final _rand = Random(42);

  @override
  bool get isConnected => _connected;

  @override
  String get localPeerId => '12D3KooW0demo1234567';

  @override
  String get nickname => 'You';

  @override
  String get localPubKey => 'abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789';

  @override
  Stream<FileTransfer> get onFileTransfer => _fileTransferController.stream;

  @override
  Stream<DeliveryReceipt> get onDeliveryReceipt => _deliveryReceiptController.stream;

  @override
  Future<bool> connect({String host = 'localhost', int port = 9876}) async {
    _connected = true;
    _connectionController.add(true);
    _peerJoinController.add(_contacts[0]);
    _peerJoinController.add(_contacts[1]);
    return true;
  }

  @override
  Future<void> disconnect() async {
    _connected = false;
    _connectionController.add(false);
  }

  @override
  Future<bool> sendMessage(Message message) async {
    _messages.putIfAbsent(
        message.recipient ?? 'broadcast', () => []);
    _messages[message.recipient ?? 'broadcast']!.insert(0, message);
    message.isSent = true;
    _messageController.add(message);

    // Simulate delivery receipts: sent -> delivered -> read
    if (message.recipient != null) {
      // "sent" is immediate
      _deliveryReceiptController.add(DeliveryReceipt(
        messageId: message.id,
        status: DeliveryStatus.sent,
        timestamp: unixNanosNow(),
      ));

      // "delivered" after 0.5-1 second
      Future.delayed(Duration(milliseconds: 500 + _rand.nextInt(500)), () {
        _deliveryReceiptController.add(DeliveryReceipt(
          messageId: message.id,
          status: DeliveryStatus.delivered,
          hops: 1 + _rand.nextInt(3),
          timestamp: unixNanosNow(),
        ));
      });

      // "read" after 2-5 seconds
      Future.delayed(Duration(seconds: 2 + _rand.nextInt(3)), () {
        _deliveryReceiptController.add(DeliveryReceipt(
          messageId: message.id,
          status: DeliveryStatus.read,
          hops: 1 + _rand.nextInt(3),
          timestamp: unixNanosNow(),
        ));
      });

      // Simulate a reply after 1-2 seconds
      Future.delayed(Duration(seconds: 1 + _rand.nextInt(2)), () {
        final reply = Message(
          id: DateTime.now().microsecondsSinceEpoch.toString(),
          type: 'chat',
          sender: message.recipient!,
          senderNick: _contacts
                  .where((c) => c.peerId == message.recipient)
                  .firstOrNull
                  ?.nickname ??
              'Unknown',
          recipient: localPeerId,
          payload: _randomReply(),
          timestamp: unixNanosNow(),
        );
        _messages.putIfAbsent(reply.sender, () => []);
        _messages[reply.sender]!.insert(0, reply);
        _messageController.add(reply);
      });
    }

    // Simulate file transfer progress if this is a file message
    if (message.isFile && message.recipient != null) {
      _simulateFileTransfer(message);
    }

    return true;
  }

  void _simulateFileTransfer(Message message) {
    String fileName;
    int fileSize;
    String mimeType;
    try {
      final meta = jsonDecode(message.payload);
      fileName = meta['file_name'] as String? ?? 'unknown_file.bin';
      fileSize = meta['file_size'] as int? ?? 1048576;
      mimeType = meta['mime_type'] as String? ?? 'application/octet-stream';
    } catch (_) {
      fileName = message.payload;
      fileSize = 1048576;
      mimeType = 'application/octet-stream';
    }

    final ft = FileTransfer(
      fileId: message.id,
      fileName: fileName,
      fileSize: fileSize,
      mimeType: mimeType,
      sender: message.sender,
      senderNick: message.senderNick,
      recipient: message.recipient,
      timestamp: message.timestamp,
      status: FileTransferStatus.sending,
      isIncoming: false,
    );
    _fileTransferController.add(ft);

    // Simulate progress updates
    final steps = 5;
    for (var i = 1; i <= steps; i++) {
      Future.delayed(Duration(milliseconds: 300 * i), () {
        final progress = i / steps;
        ft.status = i < steps
            ? FileTransferStatus.sending
            : FileTransferStatus.complete;
        ft.progress = progress;
        _fileTransferController.add(ft);
      });
    }
  }

  String _randomReply() {
    final replies = [
      'Got it! 👋',
      'That works for me',
      'Where are you right now?',
      'Can you send that again?',
      '👍',
      'Sure, on my way!',
      'Haha 😄',
      'Let me check and get back to you',
      'Perfect timing!',
      'I\'ll be there in 5',
    ];
    return replies[_rand.nextInt(replies.length)];
  }

  @override
  Future<List<Contact>> getPeers() async => _contacts;

  @override
  Future<List<Conversation>> getConversations() async {
    return _contacts.map((c) {
      final msgs = _messages[c.peerId] ?? [];
      return Conversation(
        contact: c,
        lastMessage: msgs.isNotEmpty ? msgs.first : null,
        unreadCount: msgs.where((m) => !m.isSent && m.isIncoming).length,
      );
    }).toList();
  }

  @override
  Future<List<Message>> getMessages(String peerId) async {
    return _messages[peerId] ?? [];
  }

  @override
  Stream<Message> get onMessage => _messageController.stream;

  @override
  Stream<Contact> get onPeerJoined => _peerJoinController.stream;

  @override
  Stream<Contact> get onPeerLeft => _peerLeaveController.stream;

  @override
  Stream<bool> get onConnectionState => _connectionController.stream;

  @override
  Stream<DeliveryReceipt> get onDeliveryReceipt => _deliveryReceiptController.stream;

  @override
  void sendRaw(Uint8List data) {
    // In local demo mode, we just echo the data back via onBLEData
    // for testing BLE transport integration.
    Future.microtask(() => _bleDataController.add(data));
  }

  @override
  Stream<Uint8List> get onBLEData => _bleDataController.stream;

  @override
  Stream<bool> get onBLETransportState => _bleTransportStateController.stream;
}
