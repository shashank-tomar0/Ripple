// Package models defines the data types shared across the Ripple Flutter app.
// These mirror the Go daemon's message types for seamless serialization.
//
// Canonical homes for shared types:
//   - SOSAlert / SOSUrgency   -> sos_alert.dart (re-exported below)
//   - DeliveryReceipt / DeliveryStatus -> delivery_receipt.dart (re-exported below)
// Duplicating them here previously caused ambiguous-import compile errors.
library;

import '../utils/time.dart';
import 'delivery_receipt.dart';
import 'sos_alert.dart';

export 'delivery_receipt.dart' show DeliveryReceipt, DeliveryStatus;
export 'sos_alert.dart' show SOSAlert, SOSUrgency;

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
  final bool encrypted;
  final String nonce;
  final String keyId;

  /// Delivery status for sent messages (sending -> sent -> delivered -> read)
  DeliveryStatus _status = DeliveryStatus.sent;

  DeliveryStatus get status => _status;
  set status(DeliveryStatus v) => _status = v;

  /// Number of hops the message traveled (from delivery receipt)
  int _deliveryHops = 0;
  int get deliveryHops => _deliveryHops;
  set deliveryHops(int v) => _deliveryHops = v;

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
    this.encrypted = false,
    this.nonce = '',
    this.keyId = '',
  });

  factory Message.fromJson(Map<String, dynamic> json) => Message(
        id: json['id'] as String,
        type: json['type'] as String,
        sender: json['sender'] as String,
        senderNick: json['sender_nick'] as String? ?? '',
        recipient: json['recipient'] as String?,
        payload: json['payload'] as String,
        // Wire 'ts' is Unix nanoseconds (Go: time.Now().UnixNano()).
        timestamp: json['ts'] as int? ?? unixNanosNow(),
        ttl: json['ttl'] as int? ?? 16,
        hopCount: json['hops'] as int? ?? 0,
        isSent: json['is_sent'] as bool? ?? false,
        encrypted: json['encrypted'] as bool? ?? false,
        nonce: json['nonce'] as String? ?? '',
        keyId: json['key_id'] as String? ?? '',
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
        if (encrypted) 'encrypted': true,
        if (nonce.isNotEmpty) 'nonce': nonce,
        if (keyId.isNotEmpty) 'key_id': keyId,
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
