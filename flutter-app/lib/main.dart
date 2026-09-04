// Ripple — Offline mesh messenger.
// Main app entry point with Material 3 theme, dependency injection,
// bottom navigation shell, and route handling.

import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'services/daemon_service.dart';
import 'services/app_state.dart';
import 'services/foreground_service.dart';

// ============================================================
// Screen imports
// ============================================================

import 'screens/settings_screen.dart';
import 'screens/chat_list_screen.dart';
import 'screens/chat_detail_screen.dart';
import 'screens/contacts_screen.dart';
import 'screens/qr_screen.dart';
import 'screens/mesh_map_screen.dart';

// ============================================================
// App-theme notifier (persisted via SharedPreferences)
// ============================================================

/// Provides the current [ThemeMode] and persists changes.
class AppThemeNotifier extends ChangeNotifier {
  ThemeMode _themeMode = ThemeMode.dark;

  ThemeMode get themeMode => _themeMode;

  /// Load the user's saved preference (dark/light).
  Future<void> loadFromPrefs() async {
    final prefs = await SharedPreferences.getInstance();
    final darkMode = prefs.getBool('dark_mode') ?? true;
    _themeMode = darkMode ? ThemeMode.dark : ThemeMode.light;
    notifyListeners();
  }

  /// Set [mode] and persist to SharedPreferences.
  Future<void> setThemeMode(ThemeMode mode) async {
    if (mode == _themeMode) return;
    _themeMode = mode;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool('dark_mode', mode == ThemeMode.dark);
    notifyListeners();
  }

  /// Convenience toggle between dark and light.
  Future<void> toggle() async {
    await setThemeMode(
      _themeMode == ThemeMode.dark ? ThemeMode.light : ThemeMode.dark,
    );
  }
}

// ============================================================
// App entry point
// ============================================================

void main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // Talk to the real Ripple Go daemon over WebSocket. There is no fake
  // data mode: if the daemon is unreachable the app reports disconnected.
  // Point at a daemon on another host (e.g. the Android emulator reaches
  // the host machine at 10.0.2.2) with:
  //   flutter run --dart-define=DAEMON_HOST=10.0.2.2 --dart-define=DAEMON_PORT=9876
  final daemonService = WebSocketDaemonService(
    host: const String.fromEnvironment(
      'DAEMON_HOST',
      defaultValue: 'localhost',
    ),
    port: const int.fromEnvironment('DAEMON_PORT', defaultValue: 9876),
  );

  // Restore theme preference.
  final themeNotifier = AppThemeNotifier();
  await themeNotifier.loadFromPrefs();

  // Restore background service state and start if enabled.
  final prefs = await SharedPreferences.getInstance();
  final backgroundServiceEnabled = prefs.getBool('background_service') ?? false;
  if (backgroundServiceEnabled) {
    unawaited(ForegroundService.start());
  }

  // The connection attempt happens in AppState.init() (below), which owns
  // the daemon lifecycle and reads identity/peers once connected.

  runApp(
    MultiProvider(
      providers: [
        Provider<DaemonService>.value(value: daemonService),
        ChangeNotifierProvider<AppState>(
          create: (_) => AppState(daemon: daemonService)..init(),
        ),
        ChangeNotifierProvider<AppThemeNotifier>.value(value: themeNotifier),
      ],
      child: const RippleApp(),
    ),
  );
}

// ============================================================
// RippleApp — root widget
// ============================================================

class RippleApp extends StatefulWidget {
  const RippleApp({super.key});

  @override
  State<RippleApp> createState() => _RippleAppState();
}

class _RippleAppState extends State<RippleApp> with WidgetsBindingObserver {
  bool _isConnected = false;
  StreamSubscription<bool>? _connectionSub;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _setupConnectionListener();
  }

  void _setupConnectionListener() {
    final daemon = context.read<DaemonService>();
    _isConnected = daemon.isConnected;
    _connectionSub = daemon.onConnectionState.listen((connected) {
      if (mounted) setState(() => _isConnected = connected);
    });
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _connectionSub?.cancel();
    context.read<DaemonService>().disconnect();
    super.dispose();
  }

  // ------------------------------------------------------------------
  // Theme definitions (Material 3)
  // ------------------------------------------------------------------

  ThemeData _buildDarkTheme() {
    const colorScheme = ColorScheme.dark(
      primary: Color(0xFF0D9488),          // deep teal / cyan
      onPrimary: Color(0xFFFFFFFF),
      primaryContainer: Color(0xFF14B8A6),
      onPrimaryContainer: Color(0xFF003731),
      secondary: Color(0xFFFFB74D),         // amber
      onSecondary: Color(0xFF1B1300),
      secondaryContainer: Color(0xFF452F00),
      onSecondaryContainer: Color(0xFFFFDEB3),
      surface: Color(0xFF121212),
      onSurface: Color(0xFFE0E0E0),
      surfaceVariant: Color(0xFF1E1E1E),
      onSurfaceVariant: Color(0xFFB3B3B3),
      background: Color(0xFF0A0A0A),
      onBackground: Color(0xFFE0E0E0),
      error: Color(0xFFEF5350),
      onError: Color(0xFFFFFFFF),
      errorContainer: Color(0xFF93000A),
      onErrorContainer: Color(0xFFFFDAD6),
      outline: Color(0xFF444444),
    );

    return ThemeData(
      useMaterial3: true,
      brightness: Brightness.dark,
      colorScheme: colorScheme,
      scaffoldBackgroundColor: const Color(0xFF0A0A0A),
      appBarTheme: AppBarTheme(
        centerTitle: false,
        elevation: 0,
        scrolledUnderElevation: 1,
        backgroundColor: const Color(0xFF121212),
        foregroundColor: const Color(0xFFE0E0E0),
        titleTextStyle: TextStyle(
          fontSize: 20,
          fontWeight: FontWeight.w600,
          color: const Color(0xFFE0E0E0),
        ),
      ),
      navigationBarTheme: NavigationBarThemeData(
        backgroundColor: const Color(0xFF1A1A1A),
        indicatorColor: const Color(0xFF0D9488).withOpacity(0.25),
        iconTheme: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.selected)) {
            return const IconThemeData(color: Color(0xFF0D9488));
          }
          return const IconThemeData(color: Color(0xFF9E9E9E));
        }),
        labelTextStyle: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.selected)) {
            return TextStyle(
              fontSize: 12,
              fontWeight: FontWeight.w600,
              color: const Color(0xFF0D9488),
            );
          }
          return TextStyle(
            fontSize: 12,
            fontWeight: FontWeight.w400,
            color: const Color(0xFF9E9E9E),
          );
        }),
      ),
      bottomNavigationBarTheme: const BottomNavigationBarThemeData(
        backgroundColor: Color(0xFF1A1A1A),
        selectedItemColor: Color(0xFF0D9488),
        unselectedItemColor: Color(0xFF9E9E9E),
        type: BottomNavigationBarType.fixed,
        elevation: 8,
      ),
      cardTheme: CardThemeData(
        color: const Color(0xFF1E1E1E),
        elevation: 0,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
        ),
      ),
      floatingActionButtonTheme: FloatingActionButtonThemeData(
        backgroundColor: const Color(0xFF0D9488),
        foregroundColor: const Color(0xFFFFFFFF),
        elevation: 4,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(16),
        ),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: const Color(0xFF1E1E1E),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide.none,
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: const BorderSide(color: Color(0xFF0D9488), width: 2),
        ),
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      ),
      dividerTheme: DividerThemeData(
        color: const Color(0xFF2A2A2A),
        thickness: 0.5,
      ),
      dialogTheme: DialogThemeData(
        backgroundColor: const Color(0xFF1E1E1E),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(16),
        ),
      ),
      snackBarTheme: SnackBarThemeData(
        behavior: SnackBarBehavior.floating,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
        ),
      ),
      pageTransitionsTheme: const PageTransitionsTheme(
        builders: {
          TargetPlatform.android: CupertinoPageTransitionsBuilder(),
          TargetPlatform.iOS: CupertinoPageTransitionsBuilder(),
          TargetPlatform.windows: CupertinoPageTransitionsBuilder(),
          TargetPlatform.macOS: CupertinoPageTransitionsBuilder(),
          TargetPlatform.linux: CupertinoPageTransitionsBuilder(),
        },
      ),
      typography: Typography.material2021(),
      textTheme: const TextTheme(
        displayLarge: TextStyle(fontSize: 32, fontWeight: FontWeight.bold, letterSpacing: -0.5),
        displayMedium: TextStyle(fontSize: 28, fontWeight: FontWeight.bold, letterSpacing: -0.25),
        headlineLarge: TextStyle(fontSize: 24, fontWeight: FontWeight.w600),
        headlineMedium: TextStyle(fontSize: 20, fontWeight: FontWeight.w600),
        titleLarge: TextStyle(fontSize: 18, fontWeight: FontWeight.w600),
        titleMedium: TextStyle(fontSize: 16, fontWeight: FontWeight.w500),
        bodyLarge: TextStyle(fontSize: 16, fontWeight: FontWeight.normal),
        bodyMedium: TextStyle(fontSize: 14, fontWeight: FontWeight.normal),
        labelLarge: TextStyle(fontSize: 14, fontWeight: FontWeight.w500, letterSpacing: 0.5),
        labelSmall: TextStyle(fontSize: 11, fontWeight: FontWeight.w500, letterSpacing: 0.5),
      ),
    );
  }

  ThemeData _buildLightTheme() {
    const colorScheme = ColorScheme.light(
      primary: Color(0xFF0D9488),
      onPrimary: Color(0xFFFFFFFF),
      primaryContainer: Color(0xFF5EEAD4),
      onPrimaryContainer: Color(0xFF00201D),
      secondary: Color(0xFFFF8A65),
      onSecondary: Color(0xFFFFFFFF),
      secondaryContainer: Color(0xFFFFCCBC),
      onSecondaryContainer: Color(0xFF2D1500),
      surface: Color(0xFFF5F5F5),
      onSurface: Color(0xFF1B1B1F),
      surfaceVariant: Color(0xFFE8E8E8),
      onSurfaceVariant: Color(0xFF49454F),
      background: Color(0xFFFFFFFF),
      onBackground: Color(0xFF1B1B1F),
      error: Color(0xFFD32F2F),
      onError: Color(0xFFFFFFFF),
      errorContainer: Color(0xFFFFDAD6),
      onErrorContainer: Color(0xFF410002),
      outline: Color(0xFFCAC4D0),
    );

    return ThemeData(
      useMaterial3: true,
      brightness: Brightness.light,
      colorScheme: colorScheme,
      scaffoldBackgroundColor: const Color(0xFFF5F5F5),
      appBarTheme: AppBarTheme(
        centerTitle: false,
        elevation: 0,
        scrolledUnderElevation: 1,
        backgroundColor: const Color(0xFFF5F5F5),
        foregroundColor: const Color(0xFF1B1B1F),
        titleTextStyle: TextStyle(
          fontSize: 20,
          fontWeight: FontWeight.w600,
          color: const Color(0xFF1B1B1F),
        ),
      ),
      navigationBarTheme: NavigationBarThemeData(
        backgroundColor: const Color(0xFFFFFFFF),
        indicatorColor: const Color(0xFF0D9488).withOpacity(0.15),
        iconTheme: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.selected)) {
            return const IconThemeData(color: Color(0xFF0D9488));
          }
          return const IconThemeData(color: Color(0xFF6B7280));
        }),
        labelTextStyle: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.selected)) {
            return TextStyle(
              fontSize: 12,
              fontWeight: FontWeight.w600,
              color: const Color(0xFF0D9488),
            );
          }
          return TextStyle(
            fontSize: 12,
            fontWeight: FontWeight.w400,
            color: const Color(0xFF6B7280),
          );
        }),
      ),
      bottomNavigationBarTheme: const BottomNavigationBarThemeData(
        backgroundColor: Color(0xFFFFFFFF),
        selectedItemColor: Color(0xFF0D9488),
        unselectedItemColor: Color(0xFF6B7280),
        type: BottomNavigationBarType.fixed,
        elevation: 8,
      ),
      cardTheme: CardThemeData(
        color: const Color(0xFFFFFFFF),
        elevation: 1,
        shadowColor: Colors.black12,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
        ),
      ),
      floatingActionButtonTheme: FloatingActionButtonThemeData(
        backgroundColor: const Color(0xFF0D9488),
        foregroundColor: const Color(0xFFFFFFFF),
        elevation: 4,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(16),
        ),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: const Color(0xFFF0F0F0),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide.none,
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: const BorderSide(color: Color(0xFF0D9488), width: 2),
        ),
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      ),
      dividerTheme: DividerThemeData(
        color: const Color(0xFFE0E0E0),
        thickness: 0.5,
      ),
      dialogTheme: DialogThemeData(
        backgroundColor: const Color(0xFFFFFFFF),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(16),
        ),
      ),
      snackBarTheme: SnackBarThemeData(
        behavior: SnackBarBehavior.floating,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
        ),
      ),
      pageTransitionsTheme: const PageTransitionsTheme(
        builders: {
          TargetPlatform.android: CupertinoPageTransitionsBuilder(),
          TargetPlatform.iOS: CupertinoPageTransitionsBuilder(),
          TargetPlatform.windows: CupertinoPageTransitionsBuilder(),
          TargetPlatform.macOS: CupertinoPageTransitionsBuilder(),
          TargetPlatform.linux: CupertinoPageTransitionsBuilder(),
        },
      ),
      typography: Typography.material2021(),
      textTheme: const TextTheme(
        displayLarge: TextStyle(fontSize: 32, fontWeight: FontWeight.bold, letterSpacing: -0.5),
        displayMedium: TextStyle(fontSize: 28, fontWeight: FontWeight.bold, letterSpacing: -0.25),
        headlineLarge: TextStyle(fontSize: 24, fontWeight: FontWeight.w600),
        headlineMedium: TextStyle(fontSize: 20, fontWeight: FontWeight.w600),
        titleLarge: TextStyle(fontSize: 18, fontWeight: FontWeight.w600),
        titleMedium: TextStyle(fontSize: 16, fontWeight: FontWeight.w500),
        bodyLarge: TextStyle(fontSize: 16, fontWeight: FontWeight.normal),
        bodyMedium: TextStyle(fontSize: 14, fontWeight: FontWeight.normal),
        labelLarge: TextStyle(fontSize: 14, fontWeight: FontWeight.w500, letterSpacing: 0.5),
        labelSmall: TextStyle(fontSize: 11, fontWeight: FontWeight.w500, letterSpacing: 0.5),
      ),
    );
  }

  // ------------------------------------------------------------------
  // Build
  // ------------------------------------------------------------------

  @override
  Widget build(BuildContext context) {
    final themeNotifier = context.watch<AppThemeNotifier>();

    return MaterialApp(
      title: 'Ripple',
      debugShowCheckedModeBanner: false,
      themeMode: themeNotifier.themeMode,
      theme: _buildLightTheme(),
      darkTheme: _buildDarkTheme(),

      // ---- Route handling ----
      onGenerateRoute: (settings) {
        final uri = Uri.parse(settings.name ?? '/');
        final segments = uri.pathSegments;

        // /chat/:peerId
        if (segments.length == 2 && segments[0] == 'chat') {
          final peerId = segments[1];
          return _buildPageRoute(
            settings: settings,
            builder: (_) => ChatDetailScreen(peerId: peerId),
          );
        }

        return null; // fall through to the routes map below
      },

      routes: {
        '/': (_) => const HomeScreen(),
        '/settings': (_) => const SettingsScreen(),
        '/qr': (_) => const QRScreen(),
        '/mesh': (_) => const MeshMapScreen(),

        // When ChatDetailScreen gets its own file, register it here too:
        // '/chat/:peerId' is handled by onGenerateRoute above; the routes
        // map is for fixed paths only.
      },
    );
  }

  PageRoute _buildPageRoute({
    required RouteSettings settings,
    required WidgetBuilder builder,
  }) {
    return MaterialPageRoute(
      settings: settings,
      builder: builder,
    );
  }
}

// ============================================================
// HomeScreen — Bottom-navigation shell
// ============================================================

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  int _selectedIndex = 0;

  static const _titles = ['Chats', 'Contacts', 'Mesh Map'];

  static const _icons = [
    Icons.chat_bubble_outline,
    Icons.people_outline,
    Icons.hub_outlined,
  ];

  final List<Widget> _pages = const [
    ChatListScreen(),
    ContactsScreen(),
    MeshMapScreen(),
  ];

  void _onTabSelected(int index) {
    setState(() => _selectedIndex = index);
  }

  @override
  Widget build(BuildContext context) {
    final isConnected = context.watch<_RippleAppState>()._isConnected;
    final colorScheme = Theme.of(context).colorScheme;

    return Scaffold(
      appBar: AppBar(
        leading: Padding(
          padding: const EdgeInsets.only(left: 14),
          child: Icon(
            Icons.circle,
            size: 10,
            color: isConnected ? Colors.greenAccent : Colors.redAccent,
          ),
        ),
        title: Text(_titles[_selectedIndex]),
        actions: [
          IconButton(
            icon: const Icon(Icons.qr_code_scanner_outlined),
            tooltip: 'QR Scanner',
            onPressed: () => Navigator.pushNamed(context, '/qr'),
          ),
          IconButton(
            icon: Icon(Icons.settings_outlined),
            tooltip: 'Settings',
            onPressed: () => Navigator.pushNamed(context, '/settings'),
          ),
        ],
      ),
      body: IndexedStack(
        index: _selectedIndex,
        children: _pages,
      ),
      bottomNavigationBar: BottomNavigationBar(
        currentIndex: _selectedIndex,
        onTap: _onTabSelected,
        items: List.generate(3, (i) {
          return BottomNavigationBarItem(
            icon: Icon(_icons[i]),
            activeIcon: Icon(_icons[i]),
            label: _titles[i],
          );
        }),
      ),
    );
  }
}


