// Package local_storage_service provides local persistence for the Ripple
// Flutter app using SharedPreferences. This stores messages and contacts
// so the last session's data is available immediately on app launch before
// the daemon connection is established.
//
// For Phase 0-1 message volumes (hundreds, not millions), SharedPreferences
// is sufficient. Future phases may migrate to a more robust local database
// (such as Drift or Isar) for larger offline queues and full-text search.
library;

import 'dart:convert';
import 'package:shared_preferences/shared_preferences.dart';
import '../models/message.dart';
import '../models/contact.dart';

/// Local persistence using SharedPreferences.
///
/// All methods are static for lightweight use. Messages and contacts are
/// serialized as JSON arrays and stored under well-known keys.
class LocalStorageService {
  static const _messagesKey = 'ripple_messages';
  static const _contactsKey = 'ripple_contacts';

  // ── Messages ──

  /// Persists the full list of messages as a JSON string.
  static Future<void> saveMessages(List<Message> messages) async {
    final prefs = await SharedPreferences.getInstance();
    final json = jsonEncode(messages.map((m) => m.toJson()).toList());
    await prefs.setString(_messagesKey, json);
  }

  /// Loads previously persisted messages, or an empty list if none exist.
  static Future<List<Message>> loadMessages() async {
    final prefs = await SharedPreferences.getInstance();
    final json = prefs.getString(_messagesKey);
    if (json == null || json.isEmpty) return [];
    final list = jsonDecode(json) as List;
    return list
        .map((e) => Message.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  // ── Contacts ──

  /// Persists the contact/peer list as a JSON string.
  static Future<void> saveContacts(List<Contact> contacts) async {
    final prefs = await SharedPreferences.getInstance();
    final json = jsonEncode(contacts.map((c) => c.toJson()).toList());
    await prefs.setString(_contactsKey, json);
  }

  /// Loads previously persisted contacts, or an empty list if none exist.
  static Future<List<Contact>> loadContacts() async {
    final prefs = await SharedPreferences.getInstance();
    final json = prefs.getString(_contactsKey);
    if (json == null || json.isEmpty) return [];
    final list = jsonDecode(json) as List;
    return list
        .map((e) => Contact.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  // ── Lifecycle ──

  /// Clears all persisted data (messages and contacts).
  static Future<void> clearAll() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_messagesKey);
    await prefs.remove(_contactsKey);
  }
}
