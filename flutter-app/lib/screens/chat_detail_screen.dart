// Package screens contains all Ripple UI screens.
library;

import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:intl/intl.dart';

import '../services/app_state.dart';
import '../services/file_picker_service.dart';
import '../models/message.dart';
import '../models/contact.dart';
import '../models/file_transfer.dart';
import '../models/delivery_receipt.dart';

/// Chat detail screen — displays messages with a single peer and provides
/// a text input bar for sending new messages.
///
/// Receives [peerId] to identify the conversation. Gets contact info and
/// messages from [AppState] via Provider.
class ChatDetailScreen extends StatefulWidget {
  final String peerId;

  const ChatDetailScreen({super.key, required this.peerId});

  @override
  State<ChatDetailScreen> createState() => _ChatDetailScreenState();
}

class _ChatDetailScreenState extends State<ChatDetailScreen> {
  final TextEditingController _textController = TextEditingController();
  final ScrollController _scrollController = ScrollController();
  final FilePickerService _filePicker = LocalFilePickerService();
  bool _isSending = false;
  int _lastMessageLength = 0;

  @override
  void initState() {
    super.initState();
    _textController.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _textController.dispose();
    _scrollController.dispose();
    super.dispose();
  }

  /// Retries sending a failed message.
  void _retrySend(Message message) {
    final appState = context.read<AppState>();
    // Update local status to sending
    message.status = DeliveryStatus.sending;
    setState(() {});

    // Send the message
    appState.sendMessage(message.payload, recipient: widget.peerId).then((ok) {
      if (ok && mounted) {
        message.status = DeliveryStatus.sent;
        setState(() {});
      }
    });
  }

  /// Scrolls the message list to the bottom (newest message).
  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollController.hasClients) {
        _scrollController.animateTo(
          0,
          duration: const Duration(milliseconds: 250),
          curve: Curves.easeOut,
        );
      }
    });
  }

  /// Sends the current text as a message to [widget.peerId].
  Future<void> _sendMessage() async {
    final text = _textController.text.trim();
    if (text.isEmpty || _isSending) return;

    _isSending = true;
    final appState = context.read<AppState>();
    final ok = await appState.sendMessage(text, recipient: widget.peerId);
    _isSending = false;

    if (!mounted) return;

    if (ok) {
      _textController.clear();
      _scrollToBottom();
    } else {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Failed to send message')),
      );
    }
  }

  /// Shows the attachment options bottom sheet.
  void _showAttachmentSheet(BuildContext context) {
    showModalBottomSheet(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const SizedBox(height: 8),
            Container(
              width: 32,
              height: 4,
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.onSurfaceVariant.withOpacity(0.3),
                borderRadius: BorderRadius.circular(2),
              ),
            ),
            const SizedBox(height: 12),
            ListTile(
              leading: const Icon(Icons.camera_alt_outlined),
              title: const Text('Camera'),
              subtitle: const Text('Take a photo'),
              onTap: () {
                Navigator.pop(ctx);
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(content: Text('Camera capture coming in next update')),
                );
              },
            ),
            ListTile(
              leading: const Icon(Icons.photo_library_outlined),
              title: const Text('Gallery'),
              subtitle: const Text('Choose from your photos'),
              onTap: () {
                Navigator.pop(ctx);
                _pickAndSendFile(context, isImage: true);
              },
            ),
            ListTile(
              leading: const Icon(Icons.insert_drive_file_outlined),
              title: const Text('File'),
              subtitle: const Text('Send any file type'),
              onTap: () {
                Navigator.pop(ctx);
                _pickAndSendFile(context, isImage: false);
              },
            ),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
  }

  /// Picks a file (or image) and sends it as a file message.
  Future<void> _pickAndSendFile(BuildContext context, {required bool isImage}) async {
    final result = isImage
        ? await _filePicker.pickImage()
        : await _filePicker.pickFile();

    if (!mounted || result == null) return;

    final appState = context.read<AppState>();
    final ft = await appState.sendFile(
      fileName: result.name,
      fileSize: result.size,
      mimeType: result.mimeType,
      filePath: result.path,
      recipient: widget.peerId,
    );

    if (!mounted) return;

    if (ft == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Failed to send file')),
      );
    } else {
      _scrollToBottom();
    }
  }

  @override
  Widget build(BuildContext context) {
    final appState = context.watch<AppState>();
    final theme = Theme.of(context);
    final colorScheme = theme.colorScheme;
    final contact = appState.contactForPeerId(widget.peerId);

    // Fetch and sort messages for this contact (newest first).
    final messages = appState.messagesForContact(widget.peerId);
    messages.sort((a, b) => b.timestamp.compareTo(a.timestamp));

    // Auto-scroll when a new message arrives.
    if (messages.length > _lastMessageLength && messages.isNotEmpty) {
      _lastMessageLength = messages.length;
      _scrollToBottom();
    }

    // Build AppBar title with encryption status
    final List<Widget> appBarTitles = [
      Text(contact?.displayName ?? 'Unknown'),
    ];
    if (contact != null && contact.hopCount > 0) {
      appBarTitles.add(
        Text(
          '${contact.hopCount} hop${contact.hopCount > 1 ? 's' : ''} away',
          style: theme.textTheme.bodySmall?.copyWith(
            color: colorScheme.onSurfaceVariant,
            fontSize: 12,
          ),
        ),
      );
    }
    // Encryption status
    final isEncrypted = _hasEncryptionKey(contact);
    appBarTitles.add(
      isEncrypted
          ? Row(
              children: [
                Icon(Icons.lock, size: 12, color: Colors.greenAccent),
                SizedBox(width: 4),
                Text('E2E encrypted', style: TextStyle(fontSize: 11, color: Colors.greenAccent)),
              ],
            )
          : Row(
              children: [
                Icon(Icons.lock_open, size: 12, color: Colors.grey),
                SizedBox(width: 4),
                Text('Not encrypted', style: TextStyle(fontSize: 11, color: Colors.grey)),
              ],
            ),
    );

    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: appBarTitles,
        ),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back),
          onPressed: () => Navigator.pop(context),
        ),
      ),
      body: Column(
        children: [
          // Message list
          Expanded(
            child: messages.isEmpty
                ? _buildEmptyState(theme, colorScheme)
                : ListView.builder(
                    controller: _scrollController,
                    reverse: true,
                    padding: const EdgeInsets.symmetric(
                      horizontal: 12,
                      vertical: 8,
                    ),
                    itemCount: messages.length,
                    itemBuilder: (context, index) {
                      final message = messages[index];
                      final transfer = message.isFile
                          ? appState.fileTransferForId(message.id)
                          : null;
                      return _MessageBubble(
                        message: message,
                        transfer: transfer,
                        onRetrySend: () => _retrySend(message),
                      );
                    },
                  ),
          ),
          // Input bar
          _buildInputBar(theme, colorScheme),
        ],
      ),
    );
  }

  /// Determines if we have the peer's E2E encryption key.
  bool _hasEncryptionKey(Contact? contact) {
    if (contact == null) return false;
    // Check if this conversation is encrypted
    final isEncrypted = false; // Will be set from daemon state when crypto handshake completes
    return isEncrypted;
  }

  /// Stream that emits encryption status changes.
  Stream<bool> _encryptionStatusStream(Contact? contact) {
    // For now, just return a static value. In the future, this could
    // listen to the AppState for key exchange events.
    return Stream.value(_hasEncryptionKey(contact));
  }

  /// Empty-state widget shown when no messages exist yet.
  Widget _buildEmptyState(ThemeData theme, ColorScheme colorScheme) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(
            Icons.chat_outlined,
            size: 48,
            color: colorScheme.primary.withOpacity(0.4),
          ),
          const SizedBox(height: 12),
          Text(
            'No messages yet',
            style: theme.textTheme.bodyLarge?.copyWith(
              color: colorScheme.onSurfaceVariant,
            ),
          ),
          const SizedBox(height: 4),
          Text(
            'Send a message to start the conversation',
            style: theme.textTheme.bodySmall?.copyWith(
              color: colorScheme.onSurfaceVariant.withOpacity(0.6),
            ),
          ),
        ],
      ),
    );
  }

  /// Bottom input bar with attachment button, text field, and send button.
  Widget _buildInputBar(ThemeData theme, ColorScheme colorScheme) {
    final hasText = _textController.text.trim().isNotEmpty;

    return Container(
      padding: EdgeInsets.only(
        left: 4,
        right: 8,
        top: 10,
        bottom: MediaQuery.of(context).padding.bottom + 10,
      ),
      decoration: BoxDecoration(
        color: colorScheme.surfaceVariant,
        border: Border(
          top: BorderSide(color: colorScheme.outline.withOpacity(0.3)),
        ),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          IconButton(
            icon: const Icon(Icons.attach_file_outlined),
            tooltip: 'Attach file',
            iconSize: 22,
            color: colorScheme.onSurfaceVariant,
            onPressed: () => _showAttachmentSheet(context),
            style: IconButton.styleFrom(
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(12),
              ),
            ),
          ),
          const SizedBox(width: 2),
          Expanded(
            child: TextField(
              controller: _textController,
              textCapitalization: TextCapitalization.sentences,
              decoration: InputDecoration(
                hintText: 'Type a message...',
                filled: true,
                fillColor: colorScheme.surface,
                contentPadding: const EdgeInsets.symmetric(
                  horizontal: 16,
                  vertical: 10,
                ),
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(24),
                  borderSide: BorderSide.none,
                ),
                focusedBorder: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(24),
                  borderSide: BorderSide(
                    color: colorScheme.primary,
                    width: 1.5,
                  ),
                ),
              ),
              maxLines: 4,
              minLines: 1,
              textInputAction: TextInputAction.send,
              onSubmitted: hasText ? (_) => _sendMessage() : null,
            ),
          ),
          const SizedBox(width: 6),
          IconButton(
            icon: const Icon(Icons.send_rounded),
            iconSize: 22,
            color: hasText ? colorScheme.primary : colorScheme.onSurfaceVariant.withOpacity(0.4),
            onPressed: hasText ? _sendMessage : null,
            style: IconButton.styleFrom(
              backgroundColor: hasText
                  ? colorScheme.primary.withOpacity(0.12)
                  : Colors.transparent,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(12),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// A single message bubble in the chat.
///
/// Sent messages appear right-aligned with teal background; received messages
/// appear left-aligned with a darker surface background.
class _MessageBubble extends StatelessWidget {
  final Message message;
  final FileTransfer? transfer;

  /// Invoked when the user taps a failed sent message to retry.
  final VoidCallback? onRetrySend;

  const _MessageBubble({
    required this.message,
    this.transfer,
    this.onRetrySend,
  });

  @override
  Widget build(BuildContext context) {
    final isOwn = message.isSent;
    final theme = Theme.of(context);
    final colorScheme = theme.colorScheme;
    final timeStr = DateFormat('HH:mm').format(message.dateTime);

    final alignment = isOwn ? CrossAxisAlignment.end : CrossAxisAlignment.start;
    final bgColor = isOwn ? colorScheme.primary : colorScheme.surfaceVariant;
    final textColor = isOwn ? colorScheme.onPrimary : colorScheme.onSurface;
    final timeColor = isOwn
        ? colorScheme.onPrimary.withOpacity(0.7)
        : colorScheme.onSurfaceVariant;

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Column(
        crossAxisAlignment: alignment,
        children: [
          Container(
            constraints: BoxConstraints(
              maxWidth: MediaQuery.of(context).size.width * 0.78,
            ),
            padding: message.isFile
                ? EdgeInsets.zero
                : const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
            decoration: BoxDecoration(
              color: bgColor,
              borderRadius: BorderRadius.only(
                topLeft: const Radius.circular(18),
                topRight: const Radius.circular(18),
                bottomLeft: isOwn
                    ? const Radius.circular(18)
                    : const Radius.circular(4),
                bottomRight: isOwn
                    ? const Radius.circular(4)
                    : const Radius.circular(18),
              ),
            ),
            child: message.isFile
                ? _buildFileContent(context, timeStr, timeColor, textColor)
                : _buildTextContent(
                    timeStr, textColor, timeColor, isOwn, colorScheme),
          ),
        ],
      ),
    );
  }

  /// Renders a regular chat message bubble.
  Widget _buildTextContent(
    String timeStr,
    Color textColor,
    Color timeColor,
    bool isOwn,
    ColorScheme colorScheme,
  ) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Flexible(
              child: Text(
                message.payload,
                style: TextStyle(color: textColor, fontSize: 15),
              ),
            ),
            if (message.encrypted) ...[
              const SizedBox(width: 6),
              Icon(
                Icons.lock,
                size: 14,
                color: isOwn
                    ? colorScheme.onPrimary.withOpacity(0.7)
                    : colorScheme.onSurfaceVariant.withOpacity(0.7),
              ),
            ],
          ],
        ),
        const SizedBox(height: 4),
        Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              timeStr,
              style: TextStyle(fontSize: 11, color: timeColor),
            ),
            if (isOwn) ...[
              const SizedBox(width: 4),
              _buildStatusIcon(message, timeColor, colorScheme,
                  onRetry: onRetrySend),
            ],
            if (!message.encrypted && !isOwn) ...[
              const SizedBox(width: 6),
              Icon(
                Icons.lock_open,
                size: 12,
                color: colorScheme.onSurfaceVariant.withOpacity(0.4),
              ),
            ],
          ],
        ),
      ],
    );
  }

  /// Builds the status icon based on message delivery status.
  Widget _buildStatusIcon(
    Message message,
    Color timeColor,
    ColorScheme colorScheme, {
    VoidCallback? onRetry,
  }) {
    if (!message.isSent) {
      // This shouldn't happen for received messages, but handle gracefully
      return const SizedBox.shrink();
    }

    switch (message.status) {
      case DeliveryStatus.sending:
        return SizedBox(
          width: 12,
          height: 12,
          child: CircularProgressIndicator(
            strokeWidth: 1.5,
            valueColor: AlwaysStoppedAnimation<Color>(timeColor),
          ),
        );
      case DeliveryStatus.sent:
        return Icon(
          Icons.check,
          size: 14,
          color: timeColor,
        );
      case DeliveryStatus.delivered:
        return Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.check, size: 14, color: colorScheme.primary),
            const SizedBox(width: 2),
            Icon(Icons.check, size: 14, color: colorScheme.primary),
          ],
        );
      case DeliveryStatus.read:
        return Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.check, size: 14, color: colorScheme.primary),
            const SizedBox(width: 2),
            Icon(Icons.check, size: 14, color: colorScheme.primary),
          ],
        );
      case DeliveryStatus.failed:
        return GestureDetector(
          onTap: () => onRetry?.call(),
          child: Icon(
            Icons.error_outline,
            size: 14,
            color: colorScheme.error,
          ),
        );
      default:
        return Icon(
          Icons.check,
          size: 14,
          color: timeColor,
        );
    }
  }


  /// Renders a file message bubble with icon, name, size, progress bar, and status.
  Widget _buildFileContent(
    BuildContext context,
    String timeStr,
    Color timeColor,
    Color textColor,
  ) {
    final isOwn = message.isSent;
    final colorScheme = Theme.of(context).colorScheme;

    // Parse file metadata from the JSON payload.
    String fileName;
    int fileSize;
    String mimeType;
    try {
      final meta = jsonDecode(message.payload);
      fileName = meta['file_name'] as String? ?? 'Unknown file';
      fileSize = meta['file_size'] as int? ?? 0;
      mimeType = meta['mime_type'] as String? ?? '';
    } catch (_) {
      fileName = message.payload;
      fileSize = 0;
      mimeType = '';
    }

    // Determine icon based on mime type.
    IconData icon;
    Color iconColor;
    if (mimeType.startsWith('image/')) {
      icon = Icons.image_outlined;
      iconColor = Colors.amber;
    } else if (mimeType.startsWith('audio/')) {
      icon = Icons.audiotrack_outlined;
      iconColor = Colors.orange;
    } else if (mimeType.startsWith('video/')) {
      icon = Icons.videocam_outlined;
      iconColor = Colors.redAccent;
    } else {
      icon = Icons.insert_drive_file_outlined;
      iconColor = isOwn ? colorScheme.onPrimary : colorScheme.primary;
    }

    final transferStatus = transfer?.status;
    final progress = transfer?.progress ?? (message.isSent ? 1.0 : 0.0);
    final isTransferActive = transferStatus == FileTransferStatus.sending ||
        transferStatus == FileTransferStatus.receiving;

    // Determine action icon for incoming files.
    final Widget? actionIcon = message.isIncoming
        ? _buildDownloadIcon(colorScheme)
        : null;

    return Padding(
      padding: const EdgeInsets.all(12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          // Top row: icon + file info
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                width: 40,
                height: 40,
                decoration: BoxDecoration(
                  color: isOwn
                      ? colorScheme.onPrimary.withOpacity(0.12)
                      : colorScheme.primary.withOpacity(0.1),
                  borderRadius: BorderRadius.circular(10),
                ),
                child: Icon(icon, color: iconColor, size: 22),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      fileName,
                      style: TextStyle(
                        color: textColor,
                        fontSize: 14,
                        fontWeight: FontWeight.w500,
                      ),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                    const SizedBox(height: 2),
                    Row(
                      children: [
                        Text(
                          _formatFileSize(fileSize),
                          style: TextStyle(
                            fontSize: 11,
                            color: timeColor,
                          ),
                        ),
                        if (transferStatus != null) ...[
                          const SizedBox(width: 6),
                          Text(
                            _statusLabel(transferStatus),
                            style: TextStyle(
                              fontSize: 11,
                              color: _statusColor(transferStatus, colorScheme),
                              fontWeight: FontWeight.w500,
                            ),
                          ),
                        ],
                      ],
                    ),
                  ],
                ),
              ),
              if (actionIcon != null) actionIcon,
            ],
          ),

          // Progress bar for active transfers
          if (isTransferActive) ...[
            const SizedBox(height: 8),
            ClipRRect(
              borderRadius: BorderRadius.circular(4),
              child: LinearProgressIndicator(
                value: progress,
                minHeight: 6,
                backgroundColor: isOwn
                    ? colorScheme.onPrimary.withOpacity(0.2)
                    : colorScheme.primary.withOpacity(0.15),
                valueColor: AlwaysStoppedAnimation<Color>(
                  isOwn ? colorScheme.onPrimary : colorScheme.primary,
                ),
              ),
            ),
          ],

          // Status checkmark for complete
          if (transferStatus == FileTransferStatus.complete && message.isSent) ...[
            const SizedBox(height: 6),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  Icons.check_circle,
                  size: 14,
                  color: isOwn ? colorScheme.onPrimary : colorScheme.primary,
                ),
                const SizedBox(width: 4),
                Text(
                  'Sent',
                  style: TextStyle(
                    fontSize: 11,
                    color: timeColor,
                  ),
                ),
              ],
            ),
          ],

          // Error display
          if (transferStatus == FileTransferStatus.failed) ...[
            const SizedBox(height: 6),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  Icons.error_outline,
                  size: 14,
                  color: colorScheme.error,
                ),
                const SizedBox(width: 4),
                Text(
                  transfer?.error ?? 'Transfer failed',
                  style: TextStyle(
                    fontSize: 11,
                    color: colorScheme.error,
                  ),
                ),
              ],
            ),
          ],

          // Timestamp
          const SizedBox(height: 6),
          Text(
            timeStr,
            style: TextStyle(fontSize: 11, color: timeColor),
          ),
        ],
      ),
    );
  }

  /// Builds a download icon button for received files.
  Widget _buildDownloadIcon(ColorScheme colorScheme) {
    final isComplete = transfer?.status == FileTransferStatus.complete;
    final isFailed = transfer?.status == FileTransferStatus.failed;

    return Container(
      width: 36,
      height: 36,
      decoration: BoxDecoration(
        color: isComplete
            ? Colors.green.withOpacity(0.15)
            : isFailed
                ? colorScheme.error.withOpacity(0.15)
                : colorScheme.primary.withOpacity(0.1),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Icon(
        isComplete
            ? Icons.check_circle
            : isFailed
                ? Icons.refresh
                : Icons.download_outlined,
        size: 20,
        color: isComplete
            ? Colors.green
            : isFailed
                ? colorScheme.error
                : colorScheme.primary,
      ),
    );
  }

  /// Formats bytes into a human-readable file size string.
  String _formatFileSize(int bytes) {
    if (bytes < 1024) return '$bytes B';
    if (bytes < 1024 * 1024) {
      return '${(bytes / 1024).toStringAsFixed(1)} KB';
    }
    if (bytes < 1024 * 1024 * 1024) {
      return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
    }
    return '${(bytes / (1024 * 1024 * 1024)).toStringAsFixed(1)} GB';
  }

  /// Returns a short label for the transfer status.
  String _statusLabel(FileTransferStatus status) {
    switch (status) {
      case FileTransferStatus.pending:
        return 'Pending';
      case FileTransferStatus.sending:
        return 'Sending...';
      case FileTransferStatus.receiving:
        return 'Receiving...';
      case FileTransferStatus.complete:
        return 'Complete';
      case FileTransferStatus.failed:
        return 'Failed';
      case FileTransferStatus.cancelled:
        return 'Cancelled';
    }
  }

  /// Returns a color for the transfer status label.
  Color _statusColor(FileTransferStatus status, ColorScheme colorScheme) {
    switch (status) {
      case FileTransferStatus.sending:
      case FileTransferStatus.receiving:
        return colorScheme.primary;
      case FileTransferStatus.complete:
        return Colors.green;
      case FileTransferStatus.failed:
        return colorScheme.error;
      case FileTransferStatus.cancelled:
        return colorScheme.onSurfaceVariant;
      case FileTransferStatus.pending:
        return colorScheme.onSurfaceVariant;
    }
  }
}
