// Package models defines the data types shared across the Ripple Flutter app.
// FileTransfer tracks file transfer state between peers through the mesh network.
library;

import '../utils/time.dart';

/// The status of a file transfer operation.
enum FileTransferStatus {
  pending,
  sending,
  receiving,
  complete,
  failed,
  cancelled;

  bool get isTerminal => this == complete || this == failed || this == cancelled;
  bool get isActive => this == sending || this == receiving;
}

/// Tracks the state of a single file transfer.
///
/// Mirrors the Go daemon's file transfer model for seamless serialization.
/// Each transfer has a unique [fileId] tied to the corresponding Message id.
class FileTransfer {
  String fileId;
  String fileName;
  int fileSize;
  String mimeType;
  int chunkCount;
  String sender;
  String senderNick;
  String? recipient;
  final int timestamp;
  FileTransferStatus status;
  double progress;
  String? outputPath;
  String? error;
  bool isIncoming;

  FileTransfer({
    required this.fileId,
    required this.fileName,
    required this.fileSize,
    this.mimeType = 'application/octet-stream',
    this.chunkCount = 1,
    required this.sender,
    this.senderNick = '',
    this.recipient,
    required this.timestamp,
    this.status = FileTransferStatus.pending,
    this.progress = 0.0,
    this.outputPath,
    this.error,
    this.isIncoming = false,
  });

  factory FileTransfer.fromJson(Map<String, dynamic> json) =>
      FileTransfer.fromBridgeFrame(json);

  /// Parses a file frame exactly as the daemon's bridge emits it.
  ///
  /// The bridge sends ONE `file` frame type whose `status` field is
  /// started|progress|complete|failed, and the frames are deliberately
  /// sparse: `started` carries full metadata, `progress` carries only
  /// progress/chunk info, `complete` only the output path. Every field is
  /// therefore optional (missing values fall back to inert defaults — the
  /// AppState layer merges them into the tracked transfer).
  factory FileTransfer.fromBridgeFrame(Map<String, dynamic> json) {
    // The bridge uses `filename`; the older app-side payload used
    // `file_name`. Accept both.
    final name = (json['filename'] as String?) ??
        (json['file_name'] as String?) ??
        'Unknown file';
    final status = switch (json['status']) {
      'complete' => FileTransferStatus.complete,
      'failed' => FileTransferStatus.failed,
      'cancelled' => FileTransferStatus.cancelled,
      // 'started' or 'progress': not terminal; AppState keeps the tracked
      // transfer's direction (sending vs receiving).
      _ => FileTransferStatus.pending,
    };
    return FileTransfer(
      fileId: json['file_id'] as String,
      fileName: name,
      fileSize: json['file_size'] as int? ?? 0,
      mimeType: json['mime_type'] as String? ?? 'application/octet-stream',
      chunkCount: json['chunk_count'] as int? ?? 1,
      sender: json['sender'] as String? ?? '',
      senderNick: json['sender_nick'] as String? ?? '',
      recipient: json['recipient'] as String?,
      // Wire 'ts' is Unix nanoseconds (Go: Message.Timestamp = UnixNano).
      timestamp: json['ts'] as int? ?? unixNanosNow(),
      status: status,
      progress: (json['progress'] as num?)?.toDouble() ?? 0.0,
      outputPath: json['output_path'] as String?,
      error: json['error'] as String?,
      isIncoming: json['is_incoming'] as bool? ?? false,
    );
  }

  Map<String, dynamic> toJson() => {
        'file_id': fileId,
        'file_name': fileName,
        'file_size': fileSize,
        'mime_type': mimeType,
        'chunk_count': chunkCount,
        'sender': sender,
        'sender_nick': senderNick,
        if (recipient != null) 'recipient': recipient,
        'ts': timestamp,
        'status': status.name,
        'progress': progress,
        if (outputPath != null) 'output_path': outputPath,
        if (error != null) 'error': error,
        'is_incoming': isIncoming,
      };

  /// Human-readable file size string.
  ///
  /// Examples: "1.2 KB", "3.5 MB", "1.1 GB"
  String get formattedSize {
    if (fileSize < 1024) return '$fileSize B';
    if (fileSize < 1024 * 1024) {
      return '${(fileSize / 1024).toStringAsFixed(1)} KB';
    }
    if (fileSize < 1024 * 1024 * 1024) {
      return '${(fileSize / (1024 * 1024)).toStringAsFixed(1)} MB';
    }
    return '${(fileSize / (1024 * 1024 * 1024)).toStringAsFixed(1)} GB';
  }

  DateTime get dateTime =>
      DateTime.fromMillisecondsSinceEpoch(timestamp ~/ 1000000);

  bool get isComplete => status == FileTransferStatus.complete;
  bool get isFailed => status == FileTransferStatus.failed;
  bool get isCancelled => status == FileTransferStatus.cancelled;
  bool get isActive => status == FileTransferStatus.sending ||
      status == FileTransferStatus.receiving;

  /// Icon name matching the mime type category for UI display.
  String get iconCategory {
    if (mimeType.startsWith('image/')) return 'image';
    if (mimeType.startsWith('audio/')) return 'audio';
    if (mimeType.startsWith('video/')) return 'video';
    return 'file';
  }
}
