// Package app_state provides state management for the Ripple Flutter app.
// Uses a central AppState class with ChangeNotifier for Provider-based DI.
library;

import 'package:flutter/foundation.dart';
import '../models/message.dart';
import '../models/contact.dart';
import '../services/daemon_service.dart';

/// Central application state for Ripple.
/// Provides the daemon service interface and caches messages/contacts
/// for all screens to consume via Provider.
class AppState extends ChangeNotifier {
  final DaemonService daemon;

  bool _connected = false;
  String _localPeerId = '';
  String _nickname = '';
  List<Contact> _peers = [];
  List<Message> _messages = [];
  List<Conversation> _conversations = [];
  bool _loading = false;
  String? _error;

  AppState({required this.daemon}) {
    _setupListeners();
  }

  // ── Getters ──
  bool get connected => _connected;
  String get localPeerId => _localPeerId;
  String get nickname => _nickname;
  List<Contact> get peers => _peers;
  List<Message> get messages => _messages;
  List<Conversation> get conversations => _conversations;
  bool get loading => _loading;
  String? get error => _error;
  int get peerCount => _peers.length;
  int get unreadTotal =>
      _conversations.fold(0, (sum, c) => sum + c.unreadCount);

  // ── Lifecycle ──
  Future<void> init() async {
    _loading = true;
    notifyListeners();

    await daemon.connect();
    _connected = daemon.isConnected;
    _localPeerId = daemon.localPeerId;
    _nickname = daemon.nickname;

    await _refreshPeers();
    await _refreshConversations();

    _loading = false;
    notifyListeners();
  }

  Future<void> dispose() async {
    await daemon.disconnect();
    super.dispose();
  }

  // ── Listeners ──
  void _setupListeners() {
    daemon.onConnectionState.listen((connected) {
      _connected = connected;
      notifyListeners();
    });

    daemon.onMessage.listen((msg) {
      _messages.insert(0, msg);
      _refreshConversations();
      notifyListeners();
    });

    daemon.onPeerJoined.listen((peer) {
      _refreshPeers();
      notifyListeners();
    });

    daemon.onPeerLeft.listen((peer) {
      _refreshPeers();
      notifyListeners();
    });
  }

  // ── Actions ──
  Future<bool> sendMessage(String text, {String? recipient}) async {
    final msg = Message(
      id: DateTime.now().microsecondsSinceEpoch.toString(),
      type: 'chat',
      sender: _localPeerId,
      senderNick: _nickname,
      recipient: recipient,
      payload: text,
      timestamp: DateTime.now().microsecondsSinceEpoch,
      isSent: true,
    );

    final ok = await daemon.sendMessage(msg);
    if (ok) {
      _messages.insert(0, msg);
      await _refreshConversations();
      notifyListeners();
    }
    return ok;
  }

  Future<void> refresh() async {
    _loading = true;
    notifyListeners();
    await Future.wait([
      _refreshPeers(),
      _refreshConversations(),
    ]);
    _loading = false;
    notifyListeners();
  }

  Future<void> _refreshPeers() async {
    _peers = await daemon.getPeers();
  }

  Future<void> _refreshConversations() async {
    _conversations = await daemon.getConversations();
  }

  // ── Helpers ──
  List<Message> messagesForContact(String peerId) {
    return _messages
        .where((m) => m.sender == peerId || m.recipient == peerId)
        .toList();
  }

  Contact? contactForPeerId(String peerId) {
    return _peers.where((c) => c.peerId == peerId).firstOrNull;
  }
}
