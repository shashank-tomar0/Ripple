// Package models defines the data types shared across the Ripple Flutter app.
// These mirror the Go daemon's message types for seamless serialization.
library;

class Message {
  final String id;
  final String type;
  final String sender;
  final String senderNick;
  final String? recipient;
  final String payload;
  final int timestamp;
  int ttl;
  int hopCount;
  bool isSent;

  Message({
    required this.id,
    required this.type,
    required this.sender,
    this.senderNick = '',
    this.recipient,
    required this.payload,
    required this.timestamp,
    this.ttl = 16,
    this.hopCount = 0,
    this.isSent = false,
  });

  factory Message.fromJson(Map<String, dynamic> json) => Message(
        id: json['id'] as String,
        type: json['type'] as String,
        sender: json['sender'] as String,
        senderNick: json['sender_nick'] as String? ?? '',
        recipient: json['recipient'] as String?,
        payload: json['payload'] as String,
        timestamp: json['ts'] as int? ?? DateTime.now().millisecondsSinceEpoch,
        ttl: json['ttl'] as int? ?? 16,
        hopCount: json['hops'] as int? ?? 0,
        isSent: json['is_sent'] as bool? ?? false,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'type': type,
        'sender': sender,
        'sender_nick': senderNick,
        if (recipient != null) 'recipient': recipient,
        'payload': payload,
        'ts': timestamp,
        'ttl': ttl,
        'hops': hopCount,
        if (isSent) 'is_sent': true,
      };

  DateTime get dateTime =>
      DateTime.fromMillisecondsSinceEpoch(timestamp ~/ 1000000);

  bool get isChat => type == 'chat';
  bool get isFile => type == 'file';
  bool get isSos => type == 'sos';
  bool get isDeliveryAck => type == 'delivery_ack';
  bool get isIncoming => !isSent;

  String get shortId => id.length > 8 ? id.substring(0, 8) : id;
}

enum MessageStatus { sending, sent, delivered, failed }
