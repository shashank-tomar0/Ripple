// Package models defines the data types shared across the Ripple Flutter app.
// FileTransfer tracks file transfer state between peers through the mesh network.
library;

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
  final String fileId;
  final String fileName;
  final int fileSize;
  final String mimeType;
  final int chunkCount;
  final String sender;
  final String senderNick;
  final String? recipient;
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

  factory FileTransfer.fromJson(Map<String, dynamic> json) => FileTransfer(
        fileId: json['file_id'] as String,
        fileName: json['file_name'] as String,
        fileSize: json['file_size'] as int,
        mimeType: json['mime_type'] as String? ?? 'application/octet-stream',
        chunkCount: json['chunk_count'] as int? ?? 1,
        sender: json['sender'] as String,
        senderNick: json['sender_nick'] as String? ?? '',
        recipient: json['recipient'] as String?,
        timestamp: json['ts'] as int? ?? DateTime.now().microsecondsSinceEpoch,
        status: FileTransferStatus.values.firstWhere(
          (e) => e.name == json['status'],
          orElse: () => FileTransferStatus.pending,
        ),
        progress: (json['progress'] as num?)?.toDouble() ?? 0.0,
        outputPath: json['output_path'] as String?,
        error: json['error'] as String?,
        isIncoming: json['is_incoming'] as bool? ?? false,
      );

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
