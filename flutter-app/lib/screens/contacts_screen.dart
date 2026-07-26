// Package screens contains all Ripple UI screens.
library;

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:intl/intl.dart';

import '../models/contact.dart';
import '../services/app_state.dart';

/// Displays all discovered peers in the mesh network.
///
/// Features a search/filter bar, online/offline section headers,
/// pull-to-refresh, and an empty state when no peers are found.
class ContactsScreen extends StatefulWidget {
  const ContactsScreen({super.key});

  @override
  State<ContactsScreen> createState() => _ContactsScreenState();
}

class _ContactsScreenState extends State<ContactsScreen> {
  final TextEditingController _searchController = TextEditingController();
  String _searchQuery = '';

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  /// Returns a human-readable relative time string like "2m ago" or "3h ago".
  String _timeAgo(DateTime dateTime) {
    final now = DateTime.now();
    final diff = now.difference(dateTime);

    if (diff.isNegative) return 'just now';
    if (diff.inSeconds < 60) return 'just now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    if (diff.inHours < 24) return '${diff.inHours}h ago';
    if (diff.inDays < 7) return '${diff.inDays}d ago';
    return DateFormat('MMM d').format(dateTime);
  }

  @override
  Widget build(BuildContext context) {
    final appState = context.watch<AppState>();
    final peers = appState.peers;
    final theme = Theme.of(context);
    final colorScheme = theme.colorScheme;

    // Filter peers by search query (name or peerId).
    final query = _searchQuery.toLowerCase().trim();
    final filteredPeers = query.isEmpty
        ? peers
        : peers.where((p) {
            return p.nickname.toLowerCase().contains(query) ||
                p.peerId.toLowerCase().contains(query);
          }).toList();

    final onlinePeers = filteredPeers.where((p) => p.isOnline).toList();
    final offlinePeers = filteredPeers.where((p) => !p.isOnline).toList();

    return Scaffold(
      body: Column(
        children: [
          // ── Search / filter bar ──
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
            child: TextField(
              controller: _searchController,
              decoration: InputDecoration(
                hintText: 'Search peers by name or ID...',
                prefixIcon: const Icon(Icons.search),
                suffixIcon: _searchQuery.isNotEmpty
                    ? IconButton(
                        icon: const Icon(Icons.clear),
                        onPressed: () {
                          _searchController.clear();
                          setState(() => _searchQuery = '');
                        },
                      )
                    : null,
              ),
              onChanged: (v) => setState(() => _searchQuery = v),
            ),
          ),

          // ── Peer list ──
          Expanded(
            child: peers.isEmpty
                ? _buildEmptyState(theme, colorScheme)
                : RefreshIndicator(
                    onRefresh: () => appState.refresh(),
                    child: ListView(
                      padding: const EdgeInsets.only(bottom: 16),
                      children: [
                        if (onlinePeers.isNotEmpty) ...[
                          _SectionHeader(
                            title: 'Online — ${onlinePeers.length}',
                          ),
                          ...onlinePeers.map(
                            (p) => _PeerTile(
                              contact: p,
                              timeAgo: _timeAgo(p.lastSeen),
                              onTap: () => Navigator.pushNamed(
                                context,
                                '/chat/${p.peerId}',
                              ),
                            ),
                          ),
                        ],

                        if (offlinePeers.isNotEmpty) ...[
                          const SizedBox(height: 8),
                          _SectionHeader(
                            title: 'Offline — ${offlinePeers.length}',
                          ),
                          ...offlinePeers.map(
                            (p) => _PeerTile(
                              contact: p,
                              timeAgo: _timeAgo(p.lastSeen),
                              onTap: () => Navigator.pushNamed(
                                context,
                                '/chat/${p.peerId}',
                              ),
                            ),
                          ),
                        ],
                      ],
                    ),
                  ),
          ),
        ],
      ),
    );
  }

  Widget _buildEmptyState(ThemeData theme, ColorScheme colorScheme) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.people_outline,
              size: 72,
              color: colorScheme.primary.withOpacity(0.3),
            ),
            const SizedBox(height: 16),
            Text(
              'No peers in mesh',
              style: theme.textTheme.titleLarge?.copyWith(
                color: colorScheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              'Peers discovered on the mesh network will appear here.\n'
              'Make sure your device is connected to the mesh or ask a\n'
              'nearby peer to share their QR code.',
              textAlign: TextAlign.center,
              style: theme.textTheme.bodyMedium?.copyWith(
                color: colorScheme.onSurfaceVariant.withOpacity(0.6),
              ),
            ),
            const SizedBox(height: 24),
            FilledButton.icon(
              icon: const Icon(Icons.qr_code_scanner),
              label: const Text('Scan QR Code'),
              onPressed: () => Navigator.pushNamed(context, '/qr'),
            ),
          ],
        ),
      ),
    );
  }
}

// ============================================================
// Section header widget
// ============================================================

/// A section header label rendered as small uppercase text with the
/// primary accent colour, matching the design of SettingsScreen's headers.
class _SectionHeader extends StatelessWidget {
  final String title;
  const _SectionHeader({required this.title});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 12, 20, 4),
      child: Text(
        title.toUpperCase(),
        style: Theme.of(context).textTheme.labelSmall?.copyWith(
              color: Theme.of(context).colorScheme.primary,
              fontWeight: FontWeight.w600,
              letterSpacing: 1.2,
            ),
      ),
    );
  }
}

// ============================================================
// Individual peer tile
// ============================================================

/// A single peer entry in the contacts list, showing avatar, name,
/// short peer ID, online status, hop distance, and last-seen time.
class _PeerTile extends StatelessWidget {
  final Contact contact;
  final String timeAgo;
  final VoidCallback onTap;

  const _PeerTile({
    required this.contact,
    required this.timeAgo,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colorScheme = theme.colorScheme;

    return Card(
      margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      child: ListTile(
        // ── Avatar with online indicator dot ──
        leading: Stack(
          children: [
            CircleAvatar(
              backgroundColor: colorScheme.primary.withOpacity(0.2),
              child: Text(
                contact.nickname.isNotEmpty
                    ? contact.nickname[0].toUpperCase()
                    : '?',
                style: TextStyle(
                  color: colorScheme.primary,
                  fontWeight: FontWeight.w600,
                  fontSize: 18,
                ),
              ),
            ),
            Positioned(
              right: 0,
              bottom: 0,
              child: Container(
                width: 12,
                height: 12,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  color: contact.isOnline ? Colors.green : Colors.grey,
                  border: Border.all(
                    color: colorScheme.surface,
                    width: 2,
                  ),
                ),
              ),
            ),
          ],
        ),

        // ── Name and subtitle row ──
        title: Text(
          contact.displayName,
          style: theme.textTheme.titleMedium?.copyWith(
            fontWeight: FontWeight.w600,
          ),
        ),

        subtitle: Row(
          children: [
            // Short peer ID in monospace
            Flexible(
              child: Text(
                contact.shortId,
                overflow: TextOverflow.ellipsis,
                style: theme.textTheme.bodySmall?.copyWith(
                  fontFamily: 'RobotoMono',
                  color: colorScheme.onSurfaceVariant.withOpacity(0.7),
                ),
              ),
            ),

            // Hop count badge
            if (contact.hopCount > 0) ...[
              const SizedBox(width: 8),
              Icon(
                Icons.location_on_outlined,
                size: 12,
                color: colorScheme.onSurfaceVariant,
              ),
              const SizedBox(width: 2),
              Text(
                '${contact.hopCount} ${contact.hopCount == 1 ? 'hop' : 'hops'} away',
                style: theme.textTheme.labelSmall?.copyWith(
                  color: colorScheme.onSurfaceVariant,
                ),
              ),
            ],
          ],
        ),

        // ── Last seen time ──
        trailing: Text(
          timeAgo,
          style: theme.textTheme.bodySmall?.copyWith(
            color: colorScheme.onSurfaceVariant.withOpacity(0.6),
          ),
        ),

        onTap: onTap,
      ),
    );
  }
}
