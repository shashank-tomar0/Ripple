// Package models defines the Ripple application models.
library;

import 'dart:convert';

class Contact {
  final String peerId;
  final String nickname;
  final String? publicKey;
  final bool isOnline;
  final DateTime lastSeen;
  final int hopCount;

  Contact({
    required this.peerId,
    required this.nickname,
    this.publicKey,
    this.isOnline = false,
    DateTime? lastSeen,
    this.hopCount = 0,
  }) : lastSeen = lastSeen ?? DateTime.now();

  factory Contact.fromJson(Map<String, dynamic> json) => Contact(
        peerId: json['peer_id'] as String,
        nickname: json['nickname'] as String? ?? json['peer_id'].toString().substring(0, 8),
        publicKey: json['public_key'] as String?,
        isOnline: json['is_online'] as bool? ?? false,
        lastSeen: json['last_seen'] != null
            ? DateTime.fromMillisecondsSinceEpoch(json['last_seen'] as int)
            : DateTime.now(),
        hopCount: json['hop_count'] as int? ?? 0,
      );

  Map<String, dynamic> toJson() => {
        'peer_id': peerId,
        'nickname': nickname,
        'public_key': publicKey,
        'is_online': isOnline,
        'last_seen': lastSeen.millisecondsSinceEpoch,
        'hop_count': hopCount,
      };

  String get shortId => peerId.length > 8 ? peerId.substring(0, 8) : peerId;

  String get displayName => nickname.isNotEmpty ? nickname : shortId;
}

class Conversation {
  final Contact contact;
  final Message? lastMessage;
  final int unreadCount;

  Conversation({
    required this.contact,
    this.lastMessage,
    this.unreadCount = 0,
  });

  String get title => contact.displayName;

  String get subtitle {
    if (lastMessage == null) return 'No messages yet';
    if (lastMessage!.isFile) {
      try {
        final meta = jsonDecode(lastMessage!.payload);
        final name = meta['file_name'] as String? ?? 'File';
        return lastMessage!.isSent ? 'You: $name' : name;
      } catch (_) {
        return lastMessage!.isSent
            ? 'You: ${lastMessage!.payload}'
            : lastMessage!.payload;
      }
    }
    final preview = lastMessage!.payload.length > 40
        ? '${lastMessage!.payload.substring(0, 40)}…'
        : lastMessage!.payload;
    return lastMessage!.isSent ? 'You: $preview' : preview;
  }
}
