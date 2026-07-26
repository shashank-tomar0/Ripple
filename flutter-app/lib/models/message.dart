// Package models defines the data types shared across the Ripple Flutter app.
// These mirror the Go daemon's message types for seamless serialization.
library;

/// SOS urgency levels matching the Go daemon.
enum SOSUrgency { low, medium, high, critical }

/// SOS payload structure matching the Go daemon.
class SOSPayload {
  final SOSUrgency urgency;
  final String message;
  final double? latitude;
  final double? longitude;
  final double? accuracy;
  final int timestamp;
  final int expireMinutes;
  final bool ackRequired;

  SOSPayload({
    required this.urgency,
    required this.message,
    this.latitude,
    this.longitude,
    this.accuracy,
    required this.timestamp,
    required this.expireMinutes,
    required this.ackRequired,
  });

  factory SOSPayload.fromJson(Map<String, dynamic> json) => SOSPayload(
        urgency: _parseUrgency(json['urgency'] as String? ?? 'high'),
        message: json['message'] as String? ?? '',
        latitude: (json['lat'] as num?)?.toDouble(),
        longitude: (json['lon'] as num?)?.toDouble(),
        accuracy: (json['accuracy'] as num?)?.toDouble(),
        timestamp: json['ts'] as int? ?? 0,
        expireMinutes: json['expire_minutes'] as int? ?? 60,
        ackRequired: json['ack_required'] as bool? ?? true,
      );

  Map<String, dynamic> toJson() => {
        'urgency': urgency.name,
        'message': message,
        if (latitude != null) 'lat': latitude,
        if (longitude != null) 'lon': longitude,
        if (accuracy != null) 'accuracy': accuracy,
        'ts': timestamp,
        'expire_minutes': expireMinutes,
        'ack_required': ackRequired,
      };

  static SOSUrgency _parseUrgency(String s) {
    switch (s.toLowerCase()) {
      case 'low':
        return SOSUrgency.low;
      case 'medium':
        return SOSUrgency.medium;
      case 'critical':
        return SOSUrgency.critical;
      default:
        return SOSUrgency.high;
    }
  }
}

/// Active SOS alert for UI display.
class SOSAlert {
  final String id;
  final String sender;
  final String senderNick;
  final SOSPayload payload;
  final DateTime receivedAt;
  final int ttl;
  final int hopCount;

  SOSAlert({
    required this.id,
    required this.sender,
    required this.senderNick,
    required this.payload,
    required this.receivedAt,
    required this.ttl,
    required this.hopCount,
  });

  factory SOSAlert.fromJson(Map<String, dynamic> json) => SOSAlert(
        id: json['id'] as String,
        sender: json['sender'] as String,
        senderNick: json['sender_nick'] as String? ?? '',
        payload: SOSPayload.fromJson(json),
        receivedAt: DateTime.now(),
        ttl: json['ttl'] as int? ?? 64,
        hopCount: json['hops'] as int? ?? 0,
      );

  bool get isExpired {
    final expireTime = receivedAt.add(Duration(minutes: payload.expireMinutes));
    return DateTime.now().isAfter(expireTime);
  }

  String get shortSender => sender.length > 8 ? sender.substring(0, 8) : sender;

  String get formattedTime {
    final diff = DateTime.now().difference(receivedAt);
    if (diff.inMinutes < 1) return 'now';
    if (diff.inHours < 1) return '${diff.inMinutes}m ago';
    if (diff.inDays < 1) return '${diff.inHours}h ago';
    return '${diff.inDays}d ago';
  }
}

/// Delivery receipt status matching the Go daemon.
enum DeliveryStatus { sent, received, delivered, read, failed }

/// Delivery receipt received from the mesh network.
class DeliveryReceipt {
  final String messageId;
  final DeliveryStatus status;
  final int hops;
  final int timestamp;
  final String? error;

  DeliveryReceipt({
    required this.messageId,
    required this.status,
    this.hops = 0,
    required this.timestamp,
    this.error,
  });

  factory DeliveryReceipt.fromJson(Map<String, dynamic> json) => DeliveryReceipt(
        messageId: json['msg_id'] as String,
        status: _parseStatus(json['status'] as String? ?? 'sent'),
        hops: json['hops'] as int? ?? 0,
        timestamp: json['ts'] as int? ?? DateTime.now().millisecondsSinceEpoch,
        error: json['error'] as String?,
      );

  static DeliveryStatus _parseStatus(String s) {
    switch (s) {
      case 'received':
        return DeliveryStatus.received;
      case 'delivered':
        return DeliveryStatus.delivered;
      case 'read':
        return DeliveryStatus.read;
      case 'failed':
        return DeliveryStatus.failed;
      default:
        return DeliveryStatus.sent;
    }
  }

  Map<String, dynamic> toJson() => {
        'msg_id': messageId,
        'status': status.name,
        'hops': hops,
        'ts': timestamp,
        if (error != null) 'error': error,
      };
}

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
        encrypted: json['encrypted'] as bool? ?? false,
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
