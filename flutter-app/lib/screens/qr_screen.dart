// Package screens contains all Ripple UI screens.
library;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
import 'package:qr_flutter/qr_flutter.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import '../services/app_state.dart';

/// QR screen with two modes toggled via a SegmentedButton:
///
///   **Show** — Displays your own QR code containing a `ripple:` URI
///   so nearby peers can discover and connect to you.
///
///   **Scan** — Uses the device camera to scan another peer's QR code,
///   parses the `ripple:` URI, and prompts the user to connect.
class QRScreen extends StatefulWidget {
  const QRScreen({super.key});

  @override
  State<QRScreen> createState() => _QRScreenState();
}

class _QRScreenState extends State<QRScreen> {
  // 0 = Show (My QR), 1 = Scan.
  int _selectedMode = 0;

  // ── Show mode data ──
  String _localPeerId = '';
  String _nickname = '';
  String _localPubKey = '';

  // ── Scan mode state ──
  MobileScannerController? _scannerController;
  bool _isScanning = true;
  bool _torchOn = false;

  @override
  void initState() {
    super.initState();
    final appState = context.read<AppState>();
    _localPeerId = appState.localPeerId;
    _nickname = appState.nickname;
    _localPubKey = appState.localPubKey;
  }

  @override
  void dispose() {
    _scannerController?.dispose();
    super.dispose();
  }

  // ── Scanner lifecycle helpers ──

  void _ensureScannerCreated() {
    if (_scannerController == null) {
      _scannerController = MobileScannerController();
      _isScanning = true;
    }
  }

  void _pauseScanner() {
    _scannerController?.stop();
  }

  void _resumeScanner() {
    if (_isScanning) {
      _scannerController?.start();
    }
  }

  // ── Barcode detection ──

  void _onBarcodeDetected(BarcodeCapture capture) {
    if (!_isScanning) return;

    final barcode = capture.barcodes.firstOrNull;
    final rawValue = barcode?.rawValue;
    if (rawValue == null || rawValue.isEmpty) return;

    // Prevent re-triggering while the dialog is open.
    setState(() => _isScanning = false);
    _pauseScanner();

    // Parse the ripple: URI.  Accept either:
    //   ripple:<peerId>?nick=<name>&pk=<pubkey>
    //   ripple://<peerId>?nick=<name>&pk=<pubkey>
    String peerId;
    final uri = Uri.tryParse(rawValue);
    final nicknameFromQr = uri?.queryParameters['nick'] ?? 'Unknown';
    final pubKeyFromQr = uri?.queryParameters['pk'] ?? '';

    if (rawValue.startsWith('ripple:')) {
      // Manual parse: strip scheme and query.
      final withoutScheme = rawValue.substring('ripple:'.length);
      final queryIndex = withoutScheme.indexOf('?');
      peerId = queryIndex >= 0
          ? withoutScheme.substring(0, queryIndex)
          : withoutScheme;
      // Strip leading "//" if present (ripple://peerId).
      if (peerId.startsWith('//')) peerId = peerId.substring(2);
    } else {
      peerId = rawValue;
    }

    _showConnectDialog(peerId.trim(), nicknameFromQr, pubKeyFromQr);
  }

  // ── Dialogs ──

  void _showConnectDialog(String peerId, String nickname, String peerPubKey) {
    final theme = Theme.of(context);
    final colorScheme = theme.colorScheme;
    final shortId = peerId.length > 16
        ? '${peerId.substring(0, 16)}...'
        : peerId;

    showDialog(
      context: context,
      barrierDismissible: false,
      builder: (ctx) => AlertDialog(
        title: Text('Connect to $nickname?'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                CircleAvatar(
                  backgroundColor: colorScheme.primary.withOpacity(0.2),
                  child: Text(
                    nickname.isNotEmpty ? nickname[0].toUpperCase() : '?',
                    style: TextStyle(color: colorScheme.primary),
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        nickname,
                        style: theme.textTheme.titleMedium,
                      ),
                      Text(
                        'Peer ID: $shortId',
                        style: theme.textTheme.bodySmall?.copyWith(
                          fontFamily: 'RobotoMono',
                          color: colorScheme.onSurfaceVariant,
                        ),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ],
                  ),
                ),
              ],
            ),
            if (peerPubKey.isNotEmpty) ...[
              const SizedBox(height: 12),
              Text(
                'E2E Key Exchange: Will share your Curve25519 public key',
                style: theme.textTheme.bodySmall?.copyWith(
                  color: colorScheme.primary,
                ),
              ),
            ],
          ],
        ),
        actions: [
          TextButton(
            onPressed: () {
              Navigator.pop(ctx);
              // Resume scanning (user cancelled).
              setState(() => _isScanning = true);
              _resumeScanner();
            },
            child: const Text('Cancel'),
          ),
          FilledButton.icon(
            icon: const Icon(Icons.person_add),
            label: const Text('Connect'),
            onPressed: () {
              Navigator.pop(ctx);
              ScaffoldMessenger.of(context).showSnackBar(
                SnackBar(content: Text('Connected to $nickname!')),
              );
              // TODO: Send key exchange message with our public key
              // For now just pop back to the previous screen.
              Navigator.pop(context, {'peerId': peerId, 'pubKey': peerPubKey});
            },
          ),
        ],
      ),
    );
  }

  void _showErrorDialog(String rawValue) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Invalid QR Code'),
        content: Text(
          'This QR code is not a valid Ripple connection code.\n\n'
          'Scanned value:\n$rawValue',
        ),
        actions: [
          FilledButton(
            onPressed: () {
              Navigator.pop(ctx);
              setState(() => _isScanning = true);
              _resumeScanner();
            },
            child: const Text('Scan Again'),
          ),
        ],
      ),
    );
  }

  // ── Torch / flashlight ──

  Future<void> _toggleTorch() async {
    if (_scannerController == null) return;
    try {
      await _scannerController!.toggleTorch();
      setState(() => _torchOn = !_torchOn);
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Flashlight not available on this device')),
        );
      }
    }
  }

  // ── Build ──

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colorScheme = theme.colorScheme;

    return Scaffold(
      appBar: AppBar(
        title: Text(_selectedMode == 0 ? 'My QR Code' : 'Scan QR'),
        actions: [
          if (_selectedMode == 1)
            IconButton(
              icon: Icon(_torchOn ? Icons.flash_on : Icons.flash_off),
              tooltip: 'Toggle flashlight',
              onPressed: _toggleTorch,
            ),
        ],
      ),
      body: Column(
        children: [
          // ── Mode switcher ──
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: SegmentedButton<int>(
              segments: const [
                ButtonSegment(
                  value: 0,
                  label: Text('My QR'),
                  icon: Icon(Icons.qr_code),
                ),
                ButtonSegment(
                  value: 1,
                  label: Text('Scan'),
                  icon: Icon(Icons.qr_code_scanner),
                ),
              ],
              selected: {_selectedMode},
              onSelectionChanged: (Set<int> selection) {
                final newMode = selection.first;

                // Pause scanner when switching away from scan mode.
                if (_selectedMode == 1 && newMode != 1) {
                  _pauseScanner();
                }

                setState(() => _selectedMode = newMode);
              },
            ),
          ),

          const Divider(height: 1),

          Expanded(
            child: _selectedMode == 0
                ? _buildShowMode(theme, colorScheme)
                : _buildScanMode(theme, colorScheme),
          ),
        ],
      ),
    );
  }

  // ── Mode 1: Show (My QR) ──

  Widget _buildShowMode(ThemeData theme, ColorScheme colorScheme) {
    final qrData =
        'ripple://$_localPeerId?nick=${Uri.encodeComponent(_nickname)}&pk=${Uri.encodeComponent(_localPubKey)}';

    return SingleChildScrollView(
      padding: const EdgeInsets.all(24),
      child: Column(
        children: [
          const SizedBox(height: 16),

          // QR code rendered on a white background for contrast.
          Container(
            padding: const EdgeInsets.all(16),
            decoration: BoxDecoration(
              color: Colors.white,
              borderRadius: BorderRadius.circular(16),
            ),
            child: QrImageView(
              data: qrData,
              version: QrVersions.auto,
              size: 260,
              eyeStyle: const QrEyeStyle(
                eyeShape: QrEyeShape.square,
                color: Colors.black,
              ),
              dataModuleStyle: const QrDataModuleStyle(
                dataModuleShape: QrDataModuleShape.square,
                color: Colors.black,
              ),
            ),
          ),
          const SizedBox(height: 24),

          // Nickname
          Text(
            _nickname,
            style: theme.textTheme.headlineMedium?.copyWith(
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 12),

          // Peer ID in a monospace chip
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
            decoration: BoxDecoration(
              color: colorScheme.surfaceVariant.withOpacity(0.5),
              borderRadius: BorderRadius.circular(10),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Flexible(
                  child: Text(
                    _localPeerId,
                    style: theme.textTheme.bodySmall?.copyWith(
                      fontFamily: 'RobotoMono',
                      color: colorScheme.onSurfaceVariant,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(height: 12),

          // Hint text
          Text(
            'Share this QR code with nearby peers to connect',
            textAlign: TextAlign.center,
            style: theme.textTheme.bodySmall?.copyWith(
              color: colorScheme.onSurfaceVariant.withOpacity(0.7),
            ),
          ),
          const SizedBox(height: 32),

          // Action buttons
          Row(
            children: [
              Expanded(
                child: FilledButton.icon(
                  icon: const Icon(Icons.share),
                  label: const Text('Share'),
                  onPressed: () {
                    Clipboard.setData(ClipboardData(text: qrData));
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(
                        content: Text('Connection string copied to clipboard!'),
                      ),
                    );
                  },
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: OutlinedButton.icon(
                  icon: const Icon(Icons.copy),
                  label: const Text('Copy Peer ID'),
                  onPressed: () {
                    Clipboard.setData(
                      ClipboardData(text: _localPeerId),
                    );
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(
                        content: Text('Peer ID copied to clipboard!'),
                      ),
                    );
                  },
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  // ── Mode 2: Scan ──

  Widget _buildScanMode(ThemeData theme, ColorScheme colorScheme) {
    _ensureScannerCreated();

    return Stack(
      children: [
        // Camera preview with QR detection.
        MobileScanner(
          controller: _scannerController,
          onDetect: _onBarcodeDetected,
          fit: BoxFit.cover,
          errorBuilder: (context, error, child) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(32),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(
                      Icons.error_outline,
                      size: 64,
                      color: colorScheme.error,
                    ),
                    const SizedBox(height: 16),
                    Text(
                      'Camera error',
                      style: theme.textTheme.titleMedium?.copyWith(
                        color: colorScheme.error,
                      ),
                    ),
                    const SizedBox(height: 8),
                    Text(
                      'Please check camera permissions and try again.',
                      textAlign: TextAlign.center,
                      style: theme.textTheme.bodyMedium?.copyWith(
                        color: colorScheme.onSurfaceVariant,
                      ),
                    ),
                    const SizedBox(height: 24),
                    FilledButton(
                      onPressed: () {
                        // Dispose and re-create the controller to retry.
                        _scannerController?.dispose();
                        _scannerController = MobileScannerController();
                        _isScanning = true;
                        setState(() {});
                      },
                      child: const Text('Retry'),
                    ),
                  ],
                ),
              ),
            );
          },
        ),

        // Scanning overlay frame.
        IgnorePointer(
          child: Center(
            child: Container(
              width: 250,
              height: 250,
              decoration: BoxDecoration(
                border: Border.all(
                  color: colorScheme.primary.withOpacity(0.6),
                  width: 2,
                ),
                borderRadius: BorderRadius.circular(12),
              ),
            ),
          ),
        ),

        // Bottom hint text.
        Positioned(
          left: 0,
          right: 0,
          bottom: 32,
          child: Center(
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
              decoration: BoxDecoration(
                color: Colors.black54,
                borderRadius: BorderRadius.circular(8),
              ),
              child: Text(
                'Point your camera at a Ripple QR code',
                style: TextStyle(
                  color: Colors.white.withOpacity(0.9),
                  fontSize: 13,
                ),
              ),
            ),
          ),
        ),
      ],
    );
  }
}
