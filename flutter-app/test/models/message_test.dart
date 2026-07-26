import 'package:flutter_test/flutter_test.dart';
import 'package:ripple/models/message.dart';

void main() {
  group('Message', () {
    test('fromJson parses correctly', () {
      final json = {
        'id': 'abc123',
        'type': 'chat',
        'sender': 'peer1',
        'sender_nick': 'Alice',
        'payload': 'Hello!',
        'ts': 1700000000000,
        'ttl': 16,
        'hops': 0,
      };

      final msg = Message.fromJson(json);
      expect(msg.id, 'abc123');
      expect(msg.type, 'chat');
      expect(msg.sender, 'peer1');
      expect(msg.senderNick, 'Alice');
      expect(msg.payload, 'Hello!');
      expect(msg.isChat, true);
      expect(msg.isFile, false);
      expect(msg.isSos, false);
    });

    test('toJson roundtrips', () {
      final original = Message(
        id: 'abc',
        type: 'chat',
        sender: 'peer1',
        senderNick: 'Alice',
        payload: 'Hello',
        timestamp: 1700000000000,
      );

      final json = original.toJson();
      final restored = Message.fromJson(json);

      expect(restored.id, original.id);
      expect(restored.type, original.type);
      expect(restored.payload, original.payload);
    });

    test('isIncoming returns correct value', () {
      final sent = Message(
        id: '1', type: 'chat', sender: 'me', payload: 'hi',
        timestamp: 1, isSent: true,
      );
      final received = Message(
        id: '2', type: 'chat', sender: 'peer', payload: 'hi',
        timestamp: 1, isSent: false,
      );

      expect(sent.isIncoming, false);
      expect(received.isIncoming, true);
    });

    test('shortId truncates long IDs', () {
      final msg = Message(
        id: 'abcdef1234567890', type: 'chat', sender: 'peer',
        payload: 'hi', timestamp: 1,
      );
      expect(msg.shortId, 'abcdef12');
    });

    test('isChat returns true for chat type', () {
      final msg = Message(id: '1', type: 'chat', sender: 'peer', payload: 'hi', timestamp: 1);
      expect(msg.isChat, true);
      expect(msg.isFile, false);
      expect(msg.isSos, false);
    });

    test('isFile returns true for file type', () {
      final msg = Message(id: '1', type: 'file', sender: 'peer', payload: '{}', timestamp: 1);
      expect(msg.isFile, true);
      expect(msg.isChat, false);
    });

    test('isSos returns true for sos type', () {
      final msg = Message(id: '1', type: 'sos', sender: 'peer', payload: '{}', timestamp: 1);
      expect(msg.isSos, true);
      expect(msg.isChat, false);
    });

    test('dateTime converts timestamp correctly', () {
      final msg = Message(
        id: '1', type: 'chat', sender: 'peer', payload: 'hi',
        timestamp: 1700000000000000, // nanoseconds
      );
      // Should convert to milliseconds
      final dt = msg.dateTime;
      expect(dt.millisecondsSinceEpoch, 1700000000000);
    });
  });
}