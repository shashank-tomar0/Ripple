// Package screens contains all Ripple UI screens.
library;

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../services/foreground_service.dart';
import '../services/local_storage_service.dart';

class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  bool _darkMode = true;
  bool _autoConnect = true;
  bool _debugLogging = false;
  bool _backgroundService = false;
  int _maxHops = 16;

  @override
  void initState() {
    super.initState();
    _loadPreferences();
  }

  @override
  void dispose() {
    super.dispose();
  }

  Future<void> _loadPreferences() async {
    final prefs = await SharedPreferences.getInstance();
    setState(() {
      _darkMode = prefs.getBool('dark_mode') ?? true;
      _autoConnect = prefs.getBool('auto_connect') ?? true;
      _debugLogging = prefs.getBool('debug_logging') ?? false;
      _backgroundService = prefs.getBool('background_service') ?? false;
      _maxHops = prefs.getInt('max_hops') ?? 16;
    });
  }

  Future<void> _savePreference(String key, dynamic value) async {
    final prefs = await SharedPreferences.getInstance();
    if (value is bool) {
      await prefs.setBool(key, value);
    } else if (value is int) {
      await prefs.setInt(key, value);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colorScheme = theme.colorScheme;

    return Scaffold(
      appBar: AppBar(
        title: const Text('Settings'),
        centerTitle: true,
      ),
      body: ListView(
        padding: const EdgeInsets.symmetric(vertical: 8),
        children: [
          // ── Identity Section ──
          _SectionHeader(title: 'Identity'),
          Card(
            margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: ListTile(
              leading: CircleAvatar(
                backgroundColor: colorScheme.primaryContainer,
                child: Icon(Icons.key, color: colorScheme.onPrimaryContainer),
              ),
              title: const Text('My Peer ID'),
              subtitle: Text(
                'Tap to copy to clipboard',
                style: theme.textTheme.bodySmall?.copyWith(
                  color: colorScheme.onSurfaceVariant,
                ),
              ),
              trailing: const Icon(Icons.copy),
              onTap: () {
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(content: Text('Peer ID copied!')),
                );
              },
            ),
          ),
          Card(
            margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: ListTile(
              leading: CircleAvatar(
                backgroundColor: colorScheme.secondaryContainer,
                child: Icon(Icons.badge, color: colorScheme.onSecondaryContainer),
              ),
              title: const Text('Nickname'),
              subtitle: const Text('How others see you in the mesh'),
              trailing: const Icon(Icons.edit),
              onTap: () => _showNicknameDialog(context),
            ),
          ),

          const SizedBox(height: 12),

          // ── Network Section ──
          _SectionHeader(title: 'Network'),
          Card(
            margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: Column(
              children: [
                SwitchListTile(
                  title: const Text('Auto-connect on start'),
                  subtitle: const Text('Automatically join the mesh on app launch'),
                  value: _autoConnect,
                  onChanged: (v) {
                    setState(() => _autoConnect = v);
                    _savePreference('auto_connect', v);
                  },
                ),
                const Divider(height: 1, indent: 16, endIndent: 16),
                SwitchListTile(
                  title: const Text('Background mesh relay'),
                  subtitle: const Text('Keep mesh active when app is minimized'),
                  value: _backgroundService,
                  onChanged: (v) async {
                    setState(() => _backgroundService = v);
                    if (v) {
                      await ForegroundService.start();
                    } else {
                      await ForegroundService.stop();
                    }
                    _savePreference('background_service', v);
                  },
                  secondary: Icon(Icons.battery_charging_full, color: colorScheme.onSurfaceVariant),
                ),
                const Divider(height: 1, indent: 16, endIndent: 16),
                ListTile(
                  leading: Icon(Icons.hub, color: colorScheme.onSurfaceVariant),
                  title: const Text('Max Mesh Hops'),
                  subtitle: Text('Messages travel up to $_maxHops hops (current)'),
                  trailing: SizedBox(
                    width: 160,
                    child: Slider(
                      value: _maxHops.toDouble(),
                      min: 4,
                      max: 32,
                      divisions: 7,
                      label: '$_maxHops',
                      onChanged: (v) {
                        setState(() => _maxHops = v.toInt());
                        _savePreference('max_hops', v.toInt());
                      },
                    ),
                  ),
                ),
              ],
            ),
          ),

          const SizedBox(height: 12),

          // ── Transport Section ──
          _SectionHeader(title: 'Transports'),
          Card(
            margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: Column(
              children: [
                SwitchListTile(
                  title: const Text('TCP / LAN'),
                  subtitle: const Text('Discover peers on the same WiFi network'),
                  value: true,
                  onChanged: null, // Always enabled
                  secondary: Icon(Icons.wifi, color: colorScheme.onSurfaceVariant),
                ),
              ],
            ),
          ),

          const SizedBox(height: 12),

          // ── Appearance Section ──
          _SectionHeader(title: 'Appearance'),
          Card(
            margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: SwitchListTile(
              title: const Text('Dark mode'),
              subtitle: const Text('Ripple looks better in the dark 🌙'),
              value: _darkMode,
              onChanged: (v) {
                setState(() => _darkMode = v);
                _savePreference('dark_mode', v);
              },
              secondary: Icon(
                _darkMode ? Icons.dark_mode : Icons.light_mode,
                color: colorScheme.onSurfaceVariant,
              ),
            ),
          ),

          const SizedBox(height: 12),

          // ── Debug Section ──
          _SectionHeader(title: 'Developer'),
          Card(
            margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: Column(
              children: [
                SwitchListTile(
                  title: const Text('Debug logging'),
                  subtitle: const Text('Verbose mesh and connection logs'),
                  value: _debugLogging,
                  onChanged: (v) {
                    setState(() => _debugLogging = v);
                    _savePreference('debug_logging', v);
                  },
                  secondary: Icon(Icons.bug_report, color: colorScheme.onSurfaceVariant),
                ),
                const Divider(height: 1, indent: 16, endIndent: 16),
                ListTile(
                  leading: Icon(Icons.info_outline, color: colorScheme.onSurfaceVariant),
                  title: const Text('About Ripple'),
                  subtitle: const Text('Version 0.1.0 — Phase 0'),
                  trailing: const Icon(Icons.chevron_right),
                  onTap: () => _showAboutDialog(context),
                ),
              ],
            ),
          ),

          const SizedBox(height: 24),

          // ── Danger Zone ──
          _SectionHeader(title: 'Data'),
          Card(
            margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: ListTile(
              leading: CircleAvatar(
                backgroundColor: colorScheme.errorContainer,
                child: Icon(Icons.delete_forever, color: colorScheme.onErrorContainer),
              ),
              title: const Text('Clear all data'),
              subtitle: const Text('Remove identity, messages, and contacts'),
              onTap: () => _showClearDataDialog(context),
            ),
          ),

          const SizedBox(height: 40),
        ],
      ),
    );
  }

  void _showNicknameDialog(BuildContext context) {
    final controller = TextEditingController(text: '');
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Change Nickname'),
        content: TextField(
          controller: controller,
          decoration: const InputDecoration(
            hintText: 'Enter a display name',
            border: OutlineInputBorder(),
          ),
          autofocus: true,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () {
              Navigator.pop(ctx);
              ScaffoldMessenger.of(context).showSnackBar(
                SnackBar(content: Text('Nickname changed to "${controller.text}"')),
              );
            },
            child: const Text('Save'),
          ),
        ],
      ),
    );
  }

  void _showClearDataDialog(BuildContext context) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Clear all data?'),
        content: const Text(
          'This will remove your identity key, all messages, and contacts. '
          'You will need to re-add contacts manually. This cannot be undone.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(context).colorScheme.error,
            ),
            onPressed: () async {
              await LocalStorageService.clearAll();
              if (ctx.mounted) Navigator.pop(ctx);
              if (context.mounted) {
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(content: Text('🧹 All data cleared')),
                );
              }
            },
            child: const Text('Delete Everything'),
          ),
        ],
      ),
    );
  }

  void _showAboutDialog(BuildContext context) {
    showAboutDialog(
      context: context,
      applicationName: 'Ripple',
      applicationVersion: '0.1.0 (Phase 0)',
      applicationIcon: Container(
        width: 48,
        height: 48,
        decoration: BoxDecoration(
          color: Theme.of(context).colorScheme.primaryContainer,
          borderRadius: BorderRadius.circular(12),
        ),
        child: Icon(
          Icons.waves,
          color: Theme.of(context).colorScheme.onPrimaryContainer,
          size: 28,
        ),
      ),
      children: [
        const SizedBox(height: 16),
        const Text(
          'Ripple is an offline-first mesh messenger. '
          'Messages travel device-to-device, no internet required.',
        ),
        const SizedBox(height: 8),
        const Text(
          'Built with Flutter + Go + libp2p',
          style: TextStyle(fontSize: 12, color: Colors.grey),
        ),
      ],
    );
  }
}

class _SectionHeader extends StatelessWidget {
  final String title;
  const _SectionHeader({required this.title});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 8, 20, 4),
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
