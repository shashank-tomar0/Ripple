// Package app_state provides state management for the Ripple Flutter app.
// Uses a central AppState class with ChangeNotifier for Provider-based DI.
library;

import 'dart:async';
import 'dart:convert';
import 'package:flutter/foundation.dart';
import '../utils/time.dart';
import '../models/message.dart';
import '../models/contact.dart';
import '../models/file_transfer.dart';
import '../models/sos_alert.dart';
import '../models/delivery_receipt.dart';
import '../models/relay_event.dart';
import '../services/daemon_service.dart';
import '../services/local_storage_service.dart';

/// Central application state for Ripple.
/// Provides the daemon service interface and caches messages/contacts
/// for all screens to consume via Provider.
class AppState extends ChangeNotifier {
  final DaemonService daemon;

  bool _connected = false;
  String _localPeerId = '';
  String _nickname = '';
  /// Known peers keyed by peer ID. Events from the daemon (peer_join /
  /// peer_leave) and QR scans are the only sources — there is no list
  /// endpoint to poll, and no fake data.
  final Map<String, Contact> _peersById = {};
  List<Message> _messages = [];
  List<Conversation> _conversations = [];
  List<FileTransfer> _fileTransfers = [];
  List<SOSAlert> _activeAlerts = [];
  bool _loading = false;
  String? _error;

  /// Most recent relay events from the daemon, newest first. Bounded so the
  /// topology map only animates recent, real message traffic.
  static const _maxRelayTrail = 80;
  final List<RelayEvent> _relayTrail = [];

  /// Debounce timer for persisting state to local storage.
  Timer? _persistTimer;

  AppState({required this.daemon}) {
    _setupListeners();
  }

  // ── Getters ──
  bool get connected => _connected;
  String get localPeerId => _localPeerId;
  String get nickname => _nickname;
  String get localPubKey => daemon.localPubKey;
  List<Contact> get peers {
    final list = _peersById.values.toList()
      ..sort((a, b) => a.displayName.toLowerCase()
          .compareTo(b.displayName.toLowerCase()));
    return list;
  }

  List<Message> get messages => _messages;
  List<Conversation> get conversations => _conversations;
  bool get loading => _loading;
  String? get error => _error;
  int get peerCount => _peersById.length;
  int get unreadTotal =>
      _conversations.fold(0, (sum, c) => sum + c.unreadCount);

  List<FileTransfer> get fileTransfers => _fileTransfers;
  List<SOSAlert> get activeAlerts => _activeAlerts;
  bool get hasActiveSOS => _activeAlerts.isNotEmpty;
  List<RelayEvent> get relayTrail => List.unmodifiable(_relayTrail);

  // ── Lifecycle ──
  Future<void> init() async {
    _loading = true;
    notifyListeners();

    // Load cached data from last session for immediate display
    await _loadFromStorage();

    await daemon.connect();
    _connected = daemon.isConnected;
    _localPeerId = daemon.localPeerId;
    _nickname = daemon.nickname;

    // Peers arrive as events; conversations are derived from the local
    // message cache (the daemon serves neither as a list endpoint).
    await _refreshConversations();

    _loading = false;
    notifyListeners();
  }

  Future<void> dispose() async {
    _persistTimer?.cancel();
    // Flush any pending saves before disconnect
    await _flushPersist();
    await daemon.disconnect();
    super.dispose();
  }

  // ── Local Storage ──

  /// Loads messages and contacts from SharedPreferences into memory.
  Future<void> _loadFromStorage() async {
    final results = await Future.wait([
      LocalStorageService.loadMessages(),
      LocalStorageService.loadContacts(),
    ]);
    _messages = results[0] as List<Message>;
    for (final c in results[1] as List<Contact>) {
      _peersById[c.peerId] = c;
    }
  }

  /// Schedules a debounced persist (2 second quiet window).
  void _schedulePersist() {
    _persistTimer?.cancel();
    _persistTimer = Timer(const Duration(seconds: 2), () {
      _persistMessages();
      _persistContacts();
    });
  }

  /// Immediately persists messages without waiting for the debounce timer.
  Future<void> _persistMessages() async {
    await LocalStorageService.saveMessages(_messages);
  }

  /// Immediately persists contacts without waiting for the debounce timer.
  Future<void> _persistContacts() async {
    await LocalStorageService.saveContacts(peers);
  }

  /// Flushes any pending debounced save immediately.
  Future<void> _flushPersist() async {
    _persistTimer?.cancel();
    _persistTimer = null;
    await Future.wait([
      _persistMessages(),
      _persistContacts(),
    ]);
  }

  // ── Listeners ──
  void _setupListeners() {
    daemon.onConnectionState.listen((connected) {
      _connected = connected;
      notifyListeners();
    });

    daemon.onMessage.listen((msg) {
      // Deduplicate against already-loaded messages (e.g. from local storage)
      if (!_messages.any((m) => m.id == msg.id)) {
        _messages.insert(0, msg);

        // Create a FileTransfer entry for incoming file messages
        if (msg.isFile && msg.isIncoming) {
          try {
            final meta = jsonDecode(msg.payload);
            final ft = FileTransfer(
              fileId: msg.id,
              fileName: meta['file_name'] as String? ?? 'Unknown file',
              fileSize: meta['file_size'] as int? ?? 0,
              mimeType: meta['mime_type'] as String? ?? 'application/octet-stream',
              chunkCount: meta['chunk_count'] as int? ?? 1,
              sender: msg.sender,
              senderNick: msg.senderNick,
              recipient: msg.recipient,
              timestamp: msg.timestamp,
              status: FileTransferStatus.receiving,
              isIncoming: true,
            );
            _fileTransfers.insert(0, ft);
          } catch (_) {
            // If payload can't be parsed as JSON, create a minimal entry
            _fileTransfers.insert(0, FileTransfer(
              fileId: msg.id,
              fileName: msg.payload,
              fileSize: 0,
              sender: msg.sender,
              senderNick: msg.senderNick,
              recipient: msg.recipient,
              timestamp: msg.timestamp,
              status: FileTransferStatus.receiving,
              isIncoming: true,
            ));
          }
        }

        _refreshConversations();
        notifyListeners();
        _schedulePersist();
      }
    });

    daemon.onFileTransfer.listen((ft) {
      // Update existing file transfer or add new one
      final idx = _fileTransfers.indexWhere((t) => t.fileId == ft.fileId);
      if (idx >= 0) {
        _fileTransfers[idx] = ft;
      } else {
        _fileTransfers.insert(0, ft);
      }
      notifyListeners();
    });

    daemon.onPeerJoined.listen((peer) {
      _upsertPeer(peer);
      notifyListeners();
      _schedulePersist();
    });

    daemon.onPeerLeft.listen((peer) {
      // The bridge sends peer_leave with is_online already false.
      _upsertPeer(peer);
      notifyListeners();
      _schedulePersist();
    });

    daemon.onDeliveryReceipt.listen((receipt) {
      handleDeliveryReceipt(receipt);
    });

    daemon.onRelayEvent.listen((event) {
      _relayTrail.insert(0, event);
      if (_relayTrail.length > _maxRelayTrail) {
        _relayTrail.removeRange(_maxRelayTrail, _relayTrail.length);
      }
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
      timestamp: unixNanosNow(),
      isSent: true,
    );

    final ok = await daemon.sendMessage(msg);
    if (ok) {
      _messages.insert(0, msg);
      await _refreshConversations();
      notifyListeners();
      _persistMessages();
    }
    return ok;
  }

  /// Sends an SOS emergency broadcast to the mesh network.
  Future<bool> sendSOS({
    required String message,
    required SOSUrgency urgency,
    double? latitude,
    double? longitude,
  }) async {
    if (!_connected) return false;

    final payload = jsonEncode({
      'urgency': urgency.value,
      'message': message,
      'lat': latitude,
      'lon': longitude,
      'accuracy': 10.0, // Default accuracy in meters
      'ts': DateTime.now().millisecondsSinceEpoch,
      'expire_minutes': 60,
      'ack_required': true,
    });

    final msg = Message(
      id: DateTime.now().microsecondsSinceEpoch.toString(),
      type: 'sos',
      sender: _localPeerId,
      senderNick: _nickname,
      recipient: null, // Broadcast
      payload: payload,
      timestamp: unixNanosNow(),
      ttl: 64,
      hopCount: 0,
      isSent: true,
    );

    final ok = await daemon.sendMessage(msg);
    if (ok) {
      _messages.insert(0, msg);

      // Track locally as our own active alert
      final alert = SOSAlert(
        id: msg.id,
        sender: _localPeerId,
        senderNick: _nickname,
        message: message,
        urgency: urgency,
        latitude: latitude,
        longitude: longitude,
        accuracy: 10.0,
        receivedAt: DateTime.now(),
        expiresAt: DateTime.now().add(const Duration(minutes: 10)),
        ackRequired: true,
        isOwn: true,
      );
      _activeAlerts.insert(0, alert);

      await _refreshConversations();
      notifyListeners();
      _persistMessages();
    }
    return ok;
  }

  /// Handles an incoming SOS message from the mesh.
  Future<void> handleIncomingSOS(Map<String, dynamic> json) async {
    final alert = SOSAlert.fromJson(json);

    // Check if we already have this alert
    if (!_activeAlerts.any((a) => a.id == alert.id)) {
      _activeAlerts.insert(0, alert);
    }

    // Also add to messages for chat history
    final msg = Message.fromJson(json);
    if (!_messages.any((m) => m.id == msg.id)) {
      _messages.insert(0, msg);
    }

    await _refreshConversations();
    notifyListeners();
    _schedulePersist();
  }

  /// Starts a file transfer to [recipient].
  ///
  /// Creates a [FileTransfer] entry and a [Message] with type 'file', then
  /// sends the file metadata via the daemon. Progress updates arrive through
  /// the [onFileTransfer] stream and update the stored [FileTransfer] in place.
  Future<FileTransfer?> sendFile({
    required String fileName,
    required int fileSize,
    required String mimeType,
    required String filePath,
    String? recipient,
  }) async {
    if (!_connected) return null;

    final id = DateTime.now().microsecondsSinceEpoch.toString();
    final payload = jsonEncode({
      'file_name': fileName,
      'file_size': fileSize,
      'mime_type': mimeType,
      'chunk_count': 1,
    });

    final msg = Message(
      id: id,
      type: 'file',
      sender: _localPeerId,
      senderNick: _nickname,
      recipient: recipient,
      payload: payload,
      timestamp: unixNanosNow(),
      isSent: true,
    );

    final ft = FileTransfer(
      fileId: id,
      fileName: fileName,
      fileSize: fileSize,
      mimeType: mimeType,
      sender: _localPeerId,
      senderNick: _nickname,
      recipient: recipient,
      timestamp: unixNanosNow(),
      status: FileTransferStatus.sending,
      isIncoming: false,
    );

    _fileTransfers.insert(0, ft);
    _messages.insert(0, msg);
    notifyListeners();

    final ok = await daemon.sendMessage(msg);
    if (!ok) {
      ft.status = FileTransferStatus.failed;
      ft.error = 'Failed to send file';
      notifyListeners();
      return null;
    }

    return ft;
  }

  /// Updates the progress of an in-progress file transfer.
  void updateFileTransferProgress(String fileId, double progress) {
    final ft = fileTransferForId(fileId);
    if (ft != null) {
      ft.progress = progress.clamp(0.0, 1.0);
      notifyListeners();
    }
  }

  /// Looks up a [FileTransfer] by its [fileId].
  FileTransfer? fileTransferForId(String fileId) {
    return _fileTransfers.where((t) => t.fileId == fileId).firstOrNull;
  }

  /// Returns all file transfers associated with a given [peerId].
  List<FileTransfer> fileTransfersForContact(String peerId) {
    return _fileTransfers
        .where((t) => t.sender == peerId || t.recipient == peerId)
        .toList();
  }

  Future<void> refresh() async {
    _loading = true;
    notifyListeners();
    await _refreshConversations();
    _loading = false;
    notifyListeners();
  }

  /// Adds or replaces a peer, preserving richer fields (public key, E2E key
  /// id, hop count) that presence events don't carry.
  void _upsertPeer(Contact incoming) {
    final existing = _peersById[incoming.peerId];
    if (existing == null) {
      _peersById[incoming.peerId] = incoming;
      return;
    }
    _peersById[incoming.peerId] = Contact(
      peerId: incoming.peerId,
      nickname: incoming.nickname.isNotEmpty
          ? incoming.nickname
          : existing.nickname,
      publicKey: existing.publicKey ?? incoming.publicKey,
      e2eKeyId: existing.e2eKeyId ?? incoming.e2eKeyId,
      isOnline: incoming.isOnline,
      lastSeen: incoming.isOnline ? DateTime.now() : existing.lastSeen,
      hopCount: incoming.hopCount != 0 ? incoming.hopCount : existing.hopCount,
    );
  }

  /// Registers a peer discovered by scanning their QR code. The data comes
  /// from the scanned `ripple:` URI — peer ID, nickname, public key — and
  /// nothing is invented. If a key was shared, E2E can be established.
  Future<bool> connectToScannedPeer({
    required String peerId,
    required String nickname,
    String publicKey = '',
  }) async {
    if (peerId.isEmpty || peerId == _localPeerId) return false;

    _upsertPeer(Contact(
      peerId: peerId,
      nickname: nickname,
      publicKey: publicKey.isEmpty ? null : publicKey,
      isOnline: false, // presence arrives via peer_join from the daemon
    ));
    await _persistContacts();
    notifyListeners();

    // Ask the daemon to send our public key so the remote side can encrypt
    // to us. Real network action — not a fake "connected" state.
    return daemon.sendKeyExchange(peerId);
  }

  /// Conversations are derived locally from the message cache and known
  /// peers. The daemon does not serve conversation history over the wire
  /// yet, so the app is the source of truth for its own chat list.
  Future<void> _refreshConversations() async {
    final byPeer = <String, List<Message>>{};
    for (final m in _messages) {
      // SOS broadcasts have their own UI; keep them out of the chat list.
      if (m.type == 'sos') continue;
      final other = m.isSent ? m.recipient : m.sender;
      if (other == null || other.isEmpty) continue;
      byPeer.putIfAbsent(other, () => []).add(m);
    }

    final allPeerIds = <String>{...byPeer.keys, ..._peersById.keys};
    final convs = <Conversation>[];
    for (final pid in allPeerIds) {
      final msgs = byPeer[pid] ?? const <Message>[];
      convs.add(Conversation(
        contact: contactForPeerId(pid) ??
            Contact(peerId: pid, nickname: ''), // unknown peer -> short ID shown
        // _messages is newest-first, so the first entry is the latest.
        lastMessage: msgs.isEmpty ? null : msgs.first,
        unreadCount: msgs.where((m) => !m.isSent).length,
      ));
    }
    convs.sort((a, b) {
      final ta = a.lastMessage?.timestamp ?? 0;
      final tb = b.lastMessage?.timestamp ?? 0;
      return tb.compareTo(ta); // most recently active conversation first
    });
    _conversations = convs;
  }

  /// Handles incoming delivery receipts and updates message status.
  void handleDeliveryReceipt(DeliveryReceipt receipt) {
    // Find the message in our local cache
    final idx = _messages.indexWhere((m) => m.id == receipt.messageId);
    if (idx >= 0) {
      final msg = _messages[idx];
      // Update the message's delivery status
      msg.status = receipt.status;
      msg.deliveryHops = receipt.hops;
      notifyListeners();
      _schedulePersist();
    }
  }

  // ── Helpers ──
  List<Message> messagesForContact(String peerId) {
    return _messages
        .where((m) => m.sender == peerId || m.recipient == peerId)
        .toList();
  }

  Contact? contactForPeerId(String peerId) => _peersById[peerId];
}
