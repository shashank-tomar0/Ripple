// Package app_state provides state management for the Ripple Flutter app.
// Uses a central AppState class with ChangeNotifier for Provider-based DI.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:math';
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
      // Chat messages only — SOS alerts arrive on their own stream and
      // files on theirs; neither belongs in the chat timeline.
      if (msg.isSos || msg.isFile) return;
      // Deduplicate against already-loaded messages (e.g. from local storage)
      if (!_messages.any((m) => m.id == msg.id)) {
        _messages.insert(0, msg);
        _refreshConversations();
        notifyListeners();
        _schedulePersist();
      }
    });

    daemon.onFileTransfer.listen((ft) {
      final idx = _fileTransfers.indexWhere((t) => t.fileId == ft.fileId);
      if (idx >= 0) {
        // Merge — the bridge's frames are sparse (progress/complete carry
        // only a few fields), so a blind replace would wipe the metadata
        // the `started` frame populated.
        final t = _fileTransfers[idx];
        if (ft.fileName != 'Unknown file') t.fileName = ft.fileName;
        if (ft.fileSize > 0) t.fileSize = ft.fileSize;
        if (ft.sender.isNotEmpty) t.sender = ft.sender;
        if (ft.senderNick.isNotEmpty) t.senderNick = ft.senderNick;
        if (ft.recipient != null) t.recipient = ft.recipient;
        if (ft.outputPath != null) t.outputPath = ft.outputPath;
        if (ft.error != null) t.error = ft.error;
        if (ft.progress > 0) t.progress = ft.progress.clamp(0.0, 1.0);
        // Direction: an incoming `started` frame moves a new transfer to
        // receiving; terminal states stick; never downgrade an active one.
        if (ft.status == FileTransferStatus.complete ||
            ft.status == FileTransferStatus.failed ||
            ft.status == FileTransferStatus.cancelled) {
          t.status = ft.status;
        } else if (t.status == FileTransferStatus.pending) {
          t.status = ft.isIncoming
              ? FileTransferStatus.receiving
              : FileTransferStatus.sending;
        }
      } else {
        // A brand-new transfer: from the wire it can only be incoming.
        final incoming = ft.sender.isNotEmpty && ft.sender != _localPeerId;
        _fileTransfers.insert(0, FileTransfer(
          fileId: ft.fileId,
          fileName: ft.fileName,
          fileSize: ft.fileSize,
          mimeType: ft.mimeType,
          chunkCount: ft.chunkCount,
          sender: ft.sender,
          senderNick: ft.senderNick,
          recipient: ft.recipient,
          timestamp: ft.timestamp,
          status: incoming ? FileTransferStatus.receiving : ft.status,
          progress: ft.progress,
          outputPath: ft.outputPath,
          error: ft.error,
          isIncoming: incoming,
        ));

        // An incoming file also belongs in the chat timeline: the row's ID
        // equals the transfer's fileId (the daemon's canonical ID), so the
        // file bubble can look its progress up by message ID.
        if (incoming && !_messages.any((m) => m.id == ft.fileId)) {
          _messages.insert(0, Message(
            id: ft.fileId,
            type: 'file',
            sender: ft.sender,
            senderNick: ft.senderNick,
            recipient: ft.recipient,
            payload: jsonEncode({
              'file_name': ft.fileName,
              'file_size': ft.fileSize,
              'mime_type': ft.mimeType,
              'chunk_count': ft.chunkCount,
            }),
            timestamp: ft.timestamp,
            isSent: false,
          ));
          _refreshConversations();
        }
      }
      notifyListeners();
    });

    daemon.onSOS.listen((alert) {
      final idx = _activeAlerts.indexWhere((a) => a.id == alert.id);
      if (idx >= 0) {
        // Already tracking (e.g. our own send echo) — nothing new to add.
        return;
      }
      // The daemon relays our own broadcast back as an echo with sender ==
      // us — that is how a sent alert lands in the banner. Anything from
      // another peer is a real incoming alert.
      final isOwn = alert.sender == _localPeerId && _localPeerId.isNotEmpty;
      final tracked = SOSAlert(
        id: alert.id,
        sender: alert.sender,
        senderNick: alert.senderNick,
        message: alert.message,
        urgency: alert.urgency,
        latitude: alert.latitude,
        longitude: alert.longitude,
        accuracy: alert.accuracy,
        receivedAt: alert.receivedAt,
        expiresAt: alert.expiresAt,
        ackRequired: alert.ackRequired,
        isOwn: isOwn,
      );
      _activeAlerts.insert(0, tracked);
      notifyListeners();
      _schedulePersist();
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

  /// Generates a message ID that cannot collide with another device's:
  /// microsecond timestamp + random suffix (a bare timestamp is guessable
  /// and collisions break dedup and receipt matching).
  String _newId() {
    final rnd = Random.secure().nextInt(0xFFFFFF).toRadixString(16);
    return '${DateTime.now().microsecondsSinceEpoch.toRadixString(16)}-$rnd';
  }

  Future<bool> sendMessage(String text, {String? recipient}) async {
    final msg = Message(
      id: _newId(),
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
      id: _newId(),
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
      // The daemon re-broadcasts our own alert back to us with the real
      // (daemon-generated) message ID and 60-minute expiry — that echo is
      // what the onSOS listener tracks. Nothing is invented here.
      notifyListeners();
      _persistMessages();
    }
    return ok;
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

    final id = _newId();
    final payload = jsonEncode({
      'file_name': fileName,
      'file_size': fileSize,
      'mime_type': mimeType,
      'chunk_count': 1,
      // The daemon and the app run on the same device; the daemon reads
      // the file from this absolute path to stream it chunk by chunk.
      'file_path': filePath,
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
