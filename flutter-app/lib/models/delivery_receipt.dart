// Package models defines the data types shared across the Ripple Flutter app.
// DeliveryReceipt represents a delivery acknowledgment from the mesh network.
library;

import 'package:flutter/foundation.dart';

/// Delivery receipt status matching the Go daemon.
enum DeliveryStatus { sent, received, delivered, read, failed }

/// Delivery receipt received from the mesh network.
@immutable
class DeliveryReceipt {
  final String messageId;
  final DeliveryStatus status;
  final int hops;
  final int timestamp;
  final String? error;

  const DeliveryReceipt({
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