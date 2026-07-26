// Package models defines the data types shared across the Ripple Flutter app.
// These mirror the Go daemon's message types for seamless serialization.
library;

/// Represents an active SOS emergency alert in the mesh network.
class SOSAlert {
  final String id;
  final String sender;
  final String senderNick;
  final String message;
  final SOSUrgency urgency;
  final double? latitude;
  final double? longitude;
  final double? accuracy;
  final DateTime receivedAt;
  final DateTime expiresAt;
  final int ackCount;
  final bool ackRequired;
  final bool isOwn; // True if we sent this alert

  SOSAlert({
    required this.id,
    required this.sender,
    required this.senderNick,
    required this.message,
    required this.urgency,
    this.latitude,
    this.longitude,
    this.accuracy,
    required this.receivedAt,
    required this.expiresAt,
    this.ackCount = 0,
    this.ackRequired = true,
    this.isOwn = false,
  });

  factory SOSAlert.fromJson(Map<String, dynamic> json) {
    return SOSAlert(
      id: json['id'] as String,
      sender: json['sender'] as String,
      senderNick: json['sender_nick'] as String? ?? '',
      message: json['message'] as String,
      urgency: SOSUrgency.fromString(json['urgency'] as String? ?? 'high'),
      latitude: (json['lat'] as num?)?.toDouble(),
      longitude: (json['lon'] as num?)?.toDouble(),
      accuracy: (json['accuracy'] as num?)?.toDouble(),
      receivedAt: DateTime.fromMillisecondsSinceEpoch(
          (json['ts'] as int? ?? DateTime.now().millisecondsSinceEpoch) ~/ 1000000),
      expiresAt: DateTime.now().add(const Duration(minutes: 10)), // Will be set from expire_min
      ackCount: json['ack_count'] as int? ?? 0,
      ackRequired: json['ack_required'] as bool? ?? true,
      isOwn: json['is_own'] as bool? ?? false,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      'sender': sender,
      'sender_nick': senderNick,
      'message': message,
      'urgency': urgency.value,
      if (latitude != null) 'lat': latitude,
      if (longitude != null) 'lon': longitude,
      if (accuracy != null) 'accuracy': accuracy,
      'ts': receivedAt.millisecondsSinceEpoch * 1000000,
      'ack_count': ackCount,
      'ack_required': ackRequired,
      'is_own': isOwn,
    };
  }

  bool get isExpired => DateTime.now().isAfter(expiresAt);

  Color get urgencyColor {
    switch (urgency) {
      case SOSUrgency.low:
        return Colors.orange;
      case SOSUrgency.medium:
        return Colors.deepOrange;
      case SOSUrgency.high:
        return Colors.red;
      case SOSUrgency.critical:
        return Colors.red[900]!;
    }
  }

  String get urgencyLabel {
    switch (urgency) {
      case SOSUrgency.low:
        return '⚠️ Help';
      case SOSUrgency.medium:
        return '🚨 Urgent';
      case SOSUrgency.high:
        return '🆘 Critical';
      case SOSUrgency.critical:
        return '☠️ Emergency';
    }
  }

  String get shortId => id.length > 8 ? id.substring(0, 8) : id;
}

/// SOS urgency levels matching the Go daemon.
enum SOSUrgency {
  low('low'),
  medium('medium'),
  high('high'),
  critical('critical');

  final String value;
  const SOSUrgency(this.value);

  static SOSUrgency fromString(String value) {
    return SOSUrgency.values.firstWhere(
      (e) => e.value == value,
      orElse: () => SOSUrgency.high,
    );
  }
}