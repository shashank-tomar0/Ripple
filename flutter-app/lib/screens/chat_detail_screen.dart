// Package screens contains all Ripple UI screens.
library;

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:intl/intl.dart';

import '../services/app_state.dart';
import '../models/message.dart';
import '../models/contact.dart';

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

    // Build hop-count subtitle for the AppBar.
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
                      return _MessageBubble(message: message);
                    },
                  ),
          ),
          // Input bar
          _buildInputBar(theme, colorScheme),
        ],
      ),
    );
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

  /// Bottom input bar with text field and send button.
  Widget _buildInputBar(ThemeData theme, ColorScheme colorScheme) {
    final hasText = _textController.text.trim().isNotEmpty;

    return Container(
      padding: EdgeInsets.only(
        left: 12,
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
          Expanded(
            child: TextField(
              controller: _textController,
              textCapitalization: TextCapitalization.sentences,
              decoration: InputDecoration(
                hintText: 'Type a message…',
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

  const _MessageBubble({required this.message});

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
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
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
                ? _buildFileContent(timeStr, timeColor)
                : _buildTextContent(timeStr, textColor, timeColor, isOwn),
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
  ) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          message.payload,
          style: TextStyle(color: textColor, fontSize: 15),
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
              Icon(
                Icons.check,
                size: 14,
                color: timeColor,
              ),
            ],
          ],
        ),
      ],
    );
  }

  /// Renders a file message bubble with an icon and filename.
  Widget _buildFileContent(String timeStr, Color timeColor) {
    final theme = Theme.of(context);
    final isOwn = message.isSent;
    final textColor =
        isOwn ? theme.colorScheme.onPrimary : theme.colorScheme.onSurface;

    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(
          Icons.insert_drive_file_outlined,
          color: textColor,
          size: 24,
        ),
        const SizedBox(width: 10),
        Flexible(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                message.payload,
                style: TextStyle(
                  color: textColor,
                  fontSize: 14,
                  fontWeight: FontWeight.w500,
                ),
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
              const SizedBox(height: 2),
              Text(
                timeStr,
                style: TextStyle(fontSize: 11, color: timeColor),
              ),
            ],
          ),
        ),
      ],
    );
  }
}
