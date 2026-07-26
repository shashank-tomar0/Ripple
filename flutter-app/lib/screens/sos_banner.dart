// Package screens contains all Ripple UI screens.
library;

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../services/app_state.dart';
import '../models/sos_alert.dart';

/// A persistent banner shown at the top of the chat list when SOS alerts are active.
/// Displays the count of active alerts with a pulsing animation to draw attention.
class SOSBanner extends StatefulWidget {
  const SOSBanner({super.key});

  @override
  State<SOSBanner> createState() => _SOSBannerState();
}

class _SOSBannerState extends State<SOSBanner> with SingleTickerProviderStateMixin {
  late AnimationController _pulseController;
  late Animation<double> _pulseAnimation;

  @override
  void initState() {
    super.initState();
    _pulseController = AnimationController(
      duration: const Duration(milliseconds: 1000),
      vsync: this,
    )..repeat(reverse: true);
    _pulseAnimation = Tween<double>(begin: 0.7, end: 1.0).animate(
      CurvedAnimation(parent: _pulseController, curve: Curves.easeInOut),
    );
  }

  @override
  void dispose() {
    _pulseController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final appState = context.watch<AppState>();
    final alerts = appState.activeAlerts;

    if (alerts.isEmpty) return const SizedBox.shrink();

    // Find highest urgency for color
    SOSUrgency maxUrgency = SOSUrgency.low;
    for (final alert in alerts) {
      if (alert.urgency.index > maxUrgency.index) {
        maxUrgency = alert.urgency;
      }
    }

    final Color bannerColor = switch (maxUrgency) {
      SOSUrgency.low => Colors.orange,
      SOSUrgency.medium => Colors.deepOrange,
      SOSUrgency.high => Colors.red,
      SOSUrgency.critical => Colors.red[900]!,
    };

    return AnimatedBuilder(
      animation: _pulseAnimation,
      builder: (context, child) {
        return Container(
          width: double.infinity,
          color: bannerColor.withOpacity(0.9 * _pulseAnimation.value),
          padding: EdgeInsets.only(
            top: MediaQuery.of(context).padding.top + 8,
            bottom: 12,
            left: 16,
            right: 16,
          ),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              // Header row with icon and count
              Row(
                children: [
                  Icon(
                    Icons.warning_amber_rounded,
                    color: Colors.white,
                    size: 24,
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      '🚨 EMERGENCY ALERTS ACTIVE',
                      style: const TextStyle(
                        color: Colors.white,
                        fontWeight: FontWeight.bold,
                        fontSize: 14,
                      ),
                    ),
                  ),
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                    decoration: BoxDecoration(
                      color: Colors.white.withOpacity(0.2),
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: Text(
                      '${alerts.length}',
                      style: const TextStyle(
                        color: Colors.white,
                        fontWeight: FontWeight.bold,
                        fontSize: 16,
                      ),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 8),
              // Scrollable list of active alerts
              SizedBox(
                height: 80,
                child: ListView.builder(
                  scrollDirection: Axis.horizontal,
                  itemCount: alerts.length,
                  itemBuilder: (context, index) {
                    final alert = alerts[index];
                    return _AlertCard(alert: alert, onTap: () {
                      Navigator.pushNamed(
                        context,
                        '/sos/detail',
                        arguments: alert,
                      );
                    });
                  },
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

/// Individual alert card within the SOS banner.
class _AlertCard extends StatelessWidget {
  final SOSAlert alert;
  final VoidCallback onTap;

  const _AlertCard({required this.alert, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;

    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: 280,
        margin: const EdgeInsets.only(right: 12),
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: colorScheme.surface,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: alert.urgencyColor.withOpacity(0.5)),
          boxShadow: [
            BoxShadow(
              color: alert.urgencyColor.withOpacity(0.3),
              blurRadius: 8,
              offset: const Offset(0, 2),
            ),
          ],
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Row(
              children: [
                Icon(
                  Icons.emergency,
                  color: alert.urgencyColor,
                  size: 18,
                ),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    alert.urgencyLabel,
                    style: TextStyle(
                      color: alert.urgencyColor,
                      fontWeight: FontWeight.bold,
                      fontSize: 12,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                if (alert.isOwn)
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                    decoration: BoxDecoration(
                      color: Colors.blue.withOpacity(0.2),
                      borderRadius: BorderRadius.circular(8),
                    ),
                    child: const Text(
                      'YOU',
                      style: TextStyle(
                        color: Colors.blue,
                        fontSize: 9,
                        fontWeight: FontWeight.bold,
                      ),
                    ),
                  ),
              ],
            ),
            const SizedBox(height: 6),
            Text(
              alert.message,
              style: const TextStyle(
                color: Colors.white,
                fontSize: 13,
                fontWeight: FontWeight.w500,
              ),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
            const SizedBox(height: 4),
            Row(
              children: [
                Icon(
                  Icons.access_time,
                  size: 11,
                  color: colorScheme.onSurfaceVariant,
                ),
                const SizedBox(width: 4),
                Text(
                  _formatTimeRemaining(),
                  style: TextStyle(
                    fontSize: 11,
                    color: colorScheme.onSurfaceVariant,
                  ),
                ),
                if (alert.ackCount > 0) ...[
                  const SizedBox(width: 12),
                  Icon(
                    Icons.check_circle_outline,
                    size: 11,
                    color: Colors.greenAccent,
                  ),
                  const SizedBox(width: 4),
                  Text(
                    '${alert.ackCount} confirmed',
                    style: const TextStyle(
                      fontSize: 11,
                      color: Colors.greenAccent,
                    ),
                  ),
                ],
              ],
            ),
          ],
        ),
      ),
    );
  }

  String _formatTimeRemaining() {
    final remaining = alert.expiresAt.difference(DateTime.now());
    if (remaining.isNegative) return 'Expired';
    if (remaining.inMinutes < 1) return 'Less than 1 min';
    if (remaining.inHours < 1) return '${remaining.inMinutes} min left';
    return '${remaining.inHours}h ${remaining.inMinutes % 60}m left';
  }
}