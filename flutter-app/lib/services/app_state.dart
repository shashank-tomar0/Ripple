// Package app_state provides state management for the Ripple Flutter app.
// Uses a central AppState class with ChangeNotifier for Provider-based DI.
library;

import 'dart:async';
import 'dart:convert';
import 'package:flutter/foundation.dart';
import '../models/message.dart';
import '../models/contact.dart';
import '../models/file_transfer.dart';
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
  List<Contact> _peers = [];
  List<Message> _messages = [];
  List<Conversation> _conversations = [];
  List<FileTransfer> _fileTransfers = [];
  bool _loading = false;
  String? _error;

  /// Debounce timer for persisting state to local storage.
  Timer? _persistTimer;

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

  List<FileTransfer> get fileTransfers => _fileTransfers;

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

    await _refreshPeers();
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
    _peers = results[1] as List<Contact>;
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
    await LocalStorageService.saveContacts(_peers);
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
      _refreshPeers();
      notifyListeners();
      _schedulePersist();
    });

    daemon.onPeerLeft.listen((peer) {
      _refreshPeers();
      notifyListeners();
      _schedulePersist();
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
      timestamp: DateTime.now().microsecondsSinceEpoch,
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
      timestamp: DateTime.now().microsecondsSinceEpoch,
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
