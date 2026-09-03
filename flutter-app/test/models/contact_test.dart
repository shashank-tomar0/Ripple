import 'package:flutter_test/flutter_test.dart';
import 'package:ripple/models/contact.dart';

void main() {
  group('Contact', () {
    test('fromJson parses correctly', () {
      final json = {
        'peer_id': '12D3KooW9a',
        'nickname': 'Alice',
        'is_online': true,
        'hop_count': 2,
        'last_seen': 1700000000000,
      };

      final contact = Contact.fromJson(json);
      expect(contact.peerId, '12D3KooW9a');
      expect(contact.nickname, 'Alice');
      expect(contact.isOnline, true);
      expect(contact.hopCount, 2);
    });

    test('displayName falls back to shortId', () {
      final contact = Contact(
        peerId: '12D3KooW9a',
        nickname: '',
      );
      expect(contact.displayName, '12D3KooW'); // 8-char short ID
    });

    test('shortId truncates long peer IDs', () {
      final contact = Contact(
        peerId: '12D3KooW9aABCDEF1234567890',
        nickname: 'Alice',
      );
      expect(contact.shortId, '12D3KooW'); // 8-char short ID
    });

    test('toJson roundtrips', () {
      final original = Contact(
        peerId: '12D3KooW9a',
        nickname: 'Alice',
        publicKey: 'abc123',
        isOnline: true,
        lastSeen: DateTime.fromMillisecondsSinceEpoch(1700000000000),
        hopCount: 1,
      );

      final json = original.toJson();
      final restored = Contact.fromJson(json);

      expect(restored.peerId, original.peerId);
      expect(restored.nickname, original.nickname);
      expect(restored.publicKey, original.publicKey);
      expect(restored.isOnline, original.isOnline);
      expect(restored.hopCount, original.hopCount);
    });

    test('fromJson handles missing optional fields', () {
      final json = {
        'peer_id': '12D3KooW9a',
      };

      final contact = Contact.fromJson(json);
      expect(contact.peerId, '12D3KooW9a');
      expect(contact.nickname, '12D3KooW'); // shortId fallback
      expect(contact.isOnline, false);
      expect(contact.hopCount, 0);
      expect(contact.publicKey, null);
    });
  });
}