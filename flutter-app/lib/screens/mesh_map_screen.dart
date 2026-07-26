// Package screens contains all Ripple UI screens.
library;

import 'dart:math';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../services/app_state.dart';
import '../models/contact.dart';

/// Visualises the mesh network topology using a CustomPainter.
///
/// The local device is shown at centre.  Directly-connected peers appear in
/// an inner ring, multi-hop peers in outer rings.  Tap a peer node to see
/// its details and jump to chat.
class MeshMapScreen extends StatefulWidget {
  const MeshMapScreen({super.key});

  @override
  State<MeshMapScreen> createState() => _MeshMapScreenState();
}

class _MeshMapScreenState extends State<MeshMapScreen>
    with TickerProviderStateMixin {
  late AnimationController _pulseController;
  final TransformationController _transformCtrl = TransformationController();

  // Cached node positions, rebuilt when peers change.
  Map<String, Offset> _nodePositions = {};
  List<_MeshNode> _nodes = [];

  @override
  void initState() {
    super.initState();
    _pulseController = AnimationController(
      vsync: this,
      duration: const Duration(seconds: 2),
    )..repeat(reverse: true);
  }

  @override
  void dispose() {
    _pulseController.dispose();
    _transformCtrl.dispose();
    super.dispose();
  }

  /// Build or rebuild the node layout from the current peer list.
  void _buildLayout(AppState state) {
    final peers = state.peers;
    final you = _MeshNode(
      peerId: state.localPeerId,
      label: state.nickname.isNotEmpty ? state.nickname : 'You',
      isYou: true,
      isOnline: state.connected,
      hopCount: 0,
    );

    final nodes = <_MeshNode>[you];

    // Group peers by hop count.
    final maxHop = peers.fold(0, (int m, Contact c) => c.hopCount > m ? c.hopCount : m);
    final byHop = <int, List<Contact>>{};
    for (final p in peers) {
      byHop.putIfAbsent(p.hopCount, () => []).add(p);
    }

    for (final hop in byHop.keys.toList()..sort()) {
      for (final p in byHop[hop]!) {
        nodes.add(_MeshNode(
          peerId: p.peerId,
          label: p.nickname.isNotEmpty ? p.nickname : p.shortId,
          isYou: false,
          isOnline: p.isOnline,
          hopCount: p.hopCount,
        ));
      }
    }

    _nodes = nodes;
    _layoutNodes(Size(400, 400));
  }

  /// Arrange nodes in concentric rings.  "You" is always at centre.
  void _layoutNodes(Size area) {
    final positions = <String, Offset>{};
    if (_nodes.isEmpty) return;

    final cx = area.width / 2;
    final cy = area.height / 2;

    // Centre node.
    positions[_nodes.first.peerId] = Offset(cx, cy);

    final maxHop = _nodes.fold(0, (int m, n) => n.hopCount > m ? n.hopCount : m);
    const baseRadius = 60.0;
    final ringStep = maxHop > 0 ? (area.shortestSide / 2 - 40) / maxHop : baseRadius;

    // Group by hop and place each group on a ring.
    final byHop = <int, List<_MeshNode>>{};
    for (final n in _nodes.where((n) => !n.isYou)) {
      byHop.putIfAbsent(n.hopCount, () => []).add(n);
    }

    for (final hop in byHop.keys.toList()..sort()) {
      final group = byHop[hop]!;
      final radius = baseRadius + hop * ringStep;
      final angleStep = (2 * pi) / group.length;
      // Rotate each ring slightly so nodes don't line up radially.
      final offset = hop * 0.3;

      for (var i = 0; i < group.length; i++) {
        final angle = offset + i * angleStep;
        positions[group[i].peerId] = Offset(
          cx + radius * cos(angle),
          cy + radius * sin(angle),
        );
      }
    }

    _nodePositions = positions;
  }

  /// Find which node (if any) was tapped.
  _MeshNode? _nodeAtPoint(Offset point) {
    for (final n in _nodes) {
      final pos = _nodePositions[n.peerId];
      if (pos == null) continue;
      if ((point - pos).distance < 22) return n;
    }
    return null;
  }

  void _showPeerInfo(BuildContext context, _MeshNode node, AppState state) {
    final theme = Theme.of(context);
    final colorScheme = theme.colorScheme;

    // Find the Contact object if it exists.
    final contact = node.isYou ? null : state.contactForPeerId(node.peerId);

    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Row(
          children: [
            CircleAvatar(
              backgroundColor: colorScheme.primary.withOpacity(0.2),
              child: Text(
                node.label.isNotEmpty ? node.label[0].toUpperCase() : '?',
                style: TextStyle(color: colorScheme.primary),
              ),
            ),
            const SizedBox(width: 12),
            Text(node.label),
          ],
        ),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            _infoRow('Status', node.isOnline ? '🟢 Online' : '⚫ Offline'),
            _infoRow('Peer ID', node.peerId.length > 24
                ? '${node.peerId.substring(0, 24)}…'
                : node.peerId),
            _infoRow('Distance', node.isYou
                ? 'You'
                : '${node.hopCount} ${node.hopCount == 1 ? 'hop' : 'hops'}'),
            if (contact != null)
              _infoRow('Last seen', _timeAgo(contact.lastSeen)),
          ],
        ),
        actions: node.isYou
            ? []
            : [
                FilledButton.icon(
                  icon: const Icon(Icons.chat_outlined, size: 18),
                  label: const Text('Send Message'),
                  onPressed: () {
                    Navigator.pop(ctx);
                    Navigator.pushNamed(context, '/chat/${node.peerId}');
                  },
                ),
              ],
      ),
    );
  }

  Widget _infoRow(String label, String value) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 80,
            child: Text(
              label,
              style: const TextStyle(
                fontWeight: FontWeight.w500,
                fontSize: 13,
                color: Colors.grey,
              ),
            ),
          ),
          Expanded(
            child: Text(
              value,
              style: const TextStyle(fontSize: 13),
            ),
          ),
        ],
      ),
    );
  }

  String _timeAgo(DateTime dt) {
    final diff = DateTime.now().difference(dt);
    if (diff.inSeconds < 60) return 'just now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    if (diff.inHours < 24) return '${diff.inHours}h ago';
    if (diff.inDays < 7) return '${diff.inDays}d ago';
    return '${diff.inDays}d ago';
  }

  @override
  Widget build(BuildContext context) {
    final appState = context.watch<AppState>();
    final theme = Theme.of(context);
    final colorScheme = theme.colorScheme;
    final peers = appState.peers;

    // Rebuild layout when peers change (lazy).
    if (_nodes.isEmpty || _nodes.length != peers.length + 1) {
      _buildLayout(appState);
    }

    // ── Empty state ──
    if (peers.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(Icons.hub_outlined, size: 72,
                  color: colorScheme.primary.withOpacity(0.3)),
              const SizedBox(height: 16),
              Text('Your mesh is empty',
                  style: theme.textTheme.titleLarge?.copyWith(
                      color: colorScheme.onSurfaceVariant)),
              const SizedBox(height: 8),
              Text(
                'Start Ripple on nearby devices to discover peers.\n'
                'Messages ripple outward — no internet needed.',
                textAlign: TextAlign.center,
                style: theme.textTheme.bodyMedium?.copyWith(
                    color: colorScheme.onSurfaceVariant.withOpacity(0.6)),
              ),
            ],
          ),
        ),
      );
    }

    // ── Loading state ──
    if (appState.loading) {
      return const Center(child: CircularProgressIndicator());
    }

    return Column(
      children: [
        // ── Info bar ──
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          child: Row(
            children: [
              Icon(Icons.circle, size: 8,
                  color: appState.connected ? Colors.greenAccent : Colors.redAccent),
              const SizedBox(width: 6),
              Text(
                appState.connected ? 'Mesh active' : 'Disconnected',
                style: theme.textTheme.labelMedium?.copyWith(
                    color: colorScheme.onSurfaceVariant),
              ),
              const Spacer(),
              Text(
                '${appState.peerCount} peer${appState.peerCount == 1 ? '' : 's'}',
                style: theme.textTheme.labelMedium?.copyWith(
                    color: colorScheme.onSurfaceVariant),
              ),
              const SizedBox(width: 16),
              Text(
                '${appState.peers.where((p) => p.isOnline).length} online',
                style: theme.textTheme.labelMedium?.copyWith(
                    color: Colors.greenAccent.withOpacity(0.7)),
              ),
            ],
          ),
        ),

        // ── Interactive mesh canvas ──
        Expanded(
          child: LayoutBuilder(
            builder: (context, constraints) {
              final size = Size(constraints.maxWidth, constraints.maxHeight);
              if (_nodes.isNotEmpty) _layoutNodes(size);

              return GestureDetector(
                onTapUp: (details) {
                  // Account for the transformation matrix.
                  final matrix = _transformCtrl.value;
                  final inverted = Matrix4.tryInvert(matrix);
                  if (inverted == null) return;
                  final local = MatrixUtils.transformPoint(inverted, details.localPosition);
                  final tapped = _nodeAtPoint(local);
                  if (tapped != null) {
                    _showPeerInfo(context, tapped, appState);
                  }
                },
                child: InteractiveViewer(
                  transformationController: _transformCtrl,
                  minScale: 0.5,
                  maxScale: 3.0,
                  boundaryMargin: const EdgeInsets.all(100),
                  child: AnimatedBuilder(
                    animation: _pulseController,
                    builder: (context, _) {
                      return CustomPaint(
                        size: size,
                        painter: _MeshPainter(
                          nodes: _nodes,
                          positions: _nodePositions,
                          pulseValue: _pulseController.value,
                          colorScheme: colorScheme,
                          brightness: theme.brightness,
                        ),
                      );
                    },
                  ),
                ),
              );
            },
          ),
        ),

        // ── Legend ──
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          decoration: BoxDecoration(
            color: colorScheme.surfaceVariant.withOpacity(0.3),
          ),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              _legendDot(colorScheme.primary, 'You', colorScheme),
              const SizedBox(width: 16),
              _legendDot(Colors.greenAccent, 'Online', colorScheme),
              const SizedBox(width: 16),
              _legendDot(Colors.grey, 'Offline', colorScheme),
              const SizedBox(width: 16),
              _legendDot(colorScheme.tertiary, 'Multi-hop', colorScheme),
            ],
          ),
        ),
      ],
    );
  }

  Widget _legendDot(Color color, String label, ColorScheme cs) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 8, height: 8,
          decoration: BoxDecoration(color: color, shape: BoxShape.circle),
        ),
        const SizedBox(width: 4),
        Text(label, style: TextStyle(fontSize: 11, color: cs.onSurfaceVariant)),
      ],
    );
  }
}

// ═══════════════════════════════════════════════════════════════════
// Data model for mesh graph nodes
// ═══════════════════════════════════════════════════════════════════

class _MeshNode {
  final String peerId;
  final String label;
  final bool isYou;
  final bool isOnline;
  final int hopCount;

  const _MeshNode({
    required this.peerId,
    required this.label,
    required this.isYou,
    required this.isOnline,
    required this.hopCount,
  });
}

// ═══════════════════════════════════════════════════════════════════
// CustomPainter — draws nodes, edges, labels
// ═══════════════════════════════════════════════════════════════════

class _MeshPainter extends CustomPainter {
  final List<_MeshNode> nodes;
  final Map<String, Offset> positions;
  final double pulseValue;
  final ColorScheme colorScheme;
  final Brightness brightness;

  _MeshPainter({
    required this.nodes,
    required this.positions,
    required this.pulseValue,
    required this.colorScheme,
    required this.brightness,
  });

  @override
  void paint(Canvas canvas, Size size) {
    _drawEdges(canvas);
    _drawNodes(canvas);
  }

  void _drawEdges(Canvas canvas) {
    final youPos = positions[nodes.first.peerId];
    if (youPos == null) return;

    for (final n in nodes.where((n) => !n.isYou)) {
      final pos = positions[n.peerId];
      if (pos == null) continue;

      final paint = Paint()
        ..color = n.isOnline
            ? colorScheme.primary.withOpacity(0.3)
            : Colors.grey.withOpacity(0.15)
        ..strokeWidth = n.isOnline ? 1.5 : 1.0
        ..style = PaintingStyle.stroke;

      // Multi-hop: dashed line.
      if (n.hopCount > 1) {
        final dashPaint = Paint()
          ..color = colorScheme.tertiary.withOpacity(0.25)
          ..strokeWidth = 1.0
          ..style = PaintingStyle.stroke;
        _drawDashedLine(canvas, youPos, pos, dashPaint);
      } else {
        canvas.drawLine(youPos, pos, paint);
      }

      // Small pulse dot along the edge for active connections.
      if (n.isOnline) {
        final pulseT = pulseValue;
        final mid = Offset.lerp(youPos, pos, pulseT)!;
        final pulsePaint = Paint()
          ..color = colorScheme.primary.withOpacity(0.4 * (1 - pulseT))
          ..style = PaintingStyle.fill;
        canvas.drawCircle(mid, 2 + pulseT * 2, pulsePaint);
      }
    }
  }

  void _drawDashedLine(Canvas canvas, Offset a, Offset b, Paint paint) {
    final dx = b.dx - a.dx;
    final dy = b.dy - a.dy;
    final dist = sqrt(dx * dx + dy * dy);
    if (dist == 0) return;
    final unit = Offset(dx / dist, dy / dy);
    const dashLen = 6.0;
    const gapLen = 4.0;
    var travelled = 0.0;
    while (travelled < dist) {
      final start = Offset(a.dx + unit.dx * travelled, a.dy + unit.dy * travelled);
      final endLen = min(dashLen, dist - travelled);
      final end = Offset(
        a.dx + unit.dx * (travelled + endLen),
        a.dy + unit.dy * (travelled + endLen),
      );
      canvas.drawLine(start, end, paint);
      travelled += dashLen + gapLen;
    }
  }

  void _drawNodes(Canvas canvas) {
    final youNode = nodes.first;
    final youPos = positions[youNode.peerId];
    if (youPos == null) return;

    // Draw "You" centre node with pulsing glow.
    final glowPaint = Paint()
      ..color = colorScheme.primary.withOpacity(0.15 * (1 - pulseValue * 0.5))
      ..style = PaintingStyle.fill;
    canvas.drawCircle(youPos, 28 + pulseValue * 6, glowPaint);

    final nodePaint = Paint()
      ..color = colorScheme.primary
      ..style = PaintingStyle.fill;
    canvas.drawCircle(youPos, 24, nodePaint);

    // "You" label.
    _drawLabel(canvas, youPos, 'You', colorScheme.primary, 13, true);

    // Draw peer nodes.
    for (final n in nodes.where((n) => !n.isYou)) {
      final pos = positions[n.peerId];
      if (pos == null) continue;

      final color = n.isOnline ? Colors.greenAccent : Colors.grey;
      final radius = n.isOnline ? 16.0 : 14.0;

      // Glow for online peers.
      if (n.isOnline) {
        final glow = Paint()
          ..color = Colors.greenAccent.withOpacity(0.15)
          ..style = PaintingStyle.fill;
        canvas.drawCircle(pos, radius + 4, glow);
      }

      // Node circle.
      final np = Paint()
        ..color = color.withOpacity(n.isOnline ? 0.9 : 0.4)
        ..style = PaintingStyle.fill;
      canvas.drawCircle(pos, radius, np);

      // Border.
      final border = Paint()
        ..color = color.withOpacity(0.6)
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1.5;
      canvas.drawCircle(pos, radius, border);

      // Initial letter.
      final letter = n.label.isNotEmpty ? n.label[0].toUpperCase() : '?';
      _drawLabel(canvas, pos, letter, Colors.white, 12, false);

      // Name label below node.
      _drawLabel(canvas, pos + Offset(0, radius + 14), n.label,
          colorScheme.onSurfaceVariant, 10, true);

      // Hop badge for multi-hop.
      if (n.hopCount > 1) {
        final badgePos = pos + Offset(radius * 0.7, -radius * 0.7);
        final badgePaint = Paint()
          ..color = colorScheme.tertiary
          ..style = PaintingStyle.fill;
        canvas.drawCircle(badgePos, 8, badgePaint);
        _drawLabel(canvas, badgePos, '${n.hopCount}',
            colorScheme.onTertiary, 8, false);
      }
    }
  }

  void _drawLabel(Canvas canvas, Offset center, String text,
      Color color, double size, bool shadow) {
    final tp = TextPainter(
      text: TextSpan(
        text: text,
        style: TextStyle(
          color: color,
          fontSize: size,
          fontWeight: FontWeight.w600,
        ),
      ),
      textDirection: TextDirection.ltr,
    )..layout();

    if (shadow) {
      // Subtle background for readability.
      final bgPaint = Paint()
        ..color = brightness == Brightness.dark
            ? Colors.black38
            : Colors.white70
        ..style = PaintingStyle.fill;
      final rect = Rect.fromCenter(
        center: center + Offset(0, tp.height / 2),
        width: tp.width + 8,
        height: tp.height + 4,
      );
      canvas.drawRRect(
        RRect.fromRectAndRadius(rect, const Radius.circular(4)),
        bgPaint,
      );
    }

    tp.paint(canvas, center - Offset(tp.width / 2, tp.height / 2));
  }

  @override
  bool shouldRepaint(_MeshPainter old) =>
      old.pulseValue != pulseValue ||
      old.nodes != nodes ||
      old.positions != positions;
}
