// Package models defines the Ripple message model for Flutter.
// Provides binary codec for compact wire format (BLE-friendly, ~75% smaller than JSON).
library;

import 'dart:convert';
import 'dart:typed_data';
import 'message.dart';

/// Binary codec for compact message serialization.
///
/// Wire format mirrors the Go codec at go-daemon/pkg/codec/:
///   [1 byte]     flags (type in upper nibble, bool flags in lower)
///   [var bytes]  type string (only for long-form types)
///   [1+len]      id (1-byte length prefix + data)
///   [1+len]      sender
///   [1+len]      sender_nick (optional)
///   [1+len]      recipient (optional)
///   [1+len]      payload (optional)
///   [varint]     timestamp (zigzag)
///   [2 bytes]    ttl + hop_count
///   [1+len]      nonce (optional)
///   [1+len]      key_id (optional)
class RippleCodec {
  // Type short-codes (mirrors Go codec)
  static const int _typeShortChat = 0x0;
  static const int _typeShortFile = 0x1;
  static const int _typeShortSOS = 0x2;
  static const int _typeShortDeliveryAck = 0x3;
  static const int _typeShortPeerInfo = 0x4;
  static const int _typeShortKeyExchange = 0x5;
  static const int _typeShortLongForm = 0xF;

  // Bool flags
  static const int _flagHasNick = 1;
  static const int _flagHasRecipient = 2;
  static const int _flagHasNonce = 4;
  static const int _flagHasKeyId = 8;
  static const int _flagHasPayload = 16; // value in nibble, but used as bit

  static final Map<String, int> _typeToShort = {
    'chat': _typeShortChat,
    'file': _typeShortFile,
    'sos': _typeShortSOS,
    'delivery_ack': _typeShortDeliveryAck,
    'peer_info': _typeShortPeerInfo,
    'key_exchange': _typeShortKeyExchange,
  };

  static final Map<int, String> _shortToType = {
    _typeShortChat: 'chat',
    _typeShortFile: 'file',
    _typeShortSOS: 'sos',
    _typeShortDeliveryAck: 'delivery_ack',
    _typeShortPeerInfo: 'peer_info',
    _typeShortKeyExchange: 'key_exchange',
  };

  /// Encode a Message into compact binary format.
  static Uint8List encode(Message msg) {
    final writer = BytesWriter();

    // ── Flags byte ──
    int flags = 0;
    if (msg.senderNick.isNotEmpty) flags |= _flagHasNick;
    if (msg.recipient != null && msg.recipient!.isNotEmpty) flags |= _flagHasRecipient;
    if (msg.nonce.isNotEmpty) flags |= _flagHasNonce;
    if (msg.keyId.isNotEmpty) flags |= _flagHasKeyId;
    if (msg.payload.isNotEmpty) flags |= _flagHasPayload;

    final shortCode = _typeToShort[msg.type];
    if (shortCode != null && msg.type.length <= 15) {
      flags = (shortCode << 4) | (flags & 0x0F);
    } else {
      flags = (_typeShortLongForm << 4) | (flags & 0x0F);
    }
    writer.writeByte(flags);

    // ── Type (long form only) ──
    if (shortCode == null || shortCode == _typeShortLongForm) {
      writer.writeString(msg.type);
    }

    // ── ID ──
    writer.writeString(msg.id);

    // ── Sender ──
    writer.writeString(msg.sender);

    // ── SenderNick ──
    if (msg.senderNick.isNotEmpty) writer.writeString(msg.senderNick);

    // ── Recipient ──
    if (msg.recipient != null && msg.recipient!.isNotEmpty) writer.writeString(msg.recipient!);

    // ── Payload ──
    if (msg.payload.isNotEmpty) writer.writeString(msg.payload);

    // ── Timestamp (zigzag varint) ──
    writer.writeUVarint(_zigzag(msg.timestamp));

    // ── TTL + HopCount ──
    writer.writeByte(msg.ttl.clamp(0, 255));
    writer.writeByte(msg.hopCount.clamp(0, 255));

    // ── Nonce ──
    if (msg.nonce.isNotEmpty) writer.writeString(msg.nonce);

    // ── KeyID ──
    if (msg.keyId.isNotEmpty) writer.writeString(msg.keyId);

    return writer.toBytes();
  }

  /// Decode compact binary format into a Message.
  static Message decode(Uint8List data) {
    final reader = BytesReader(data);
    var offset = 0;

    // ── Flags byte ──
    final flags = reader.readByte();
    final typeCode = (flags >> 4) & 0x0F;
    final boolFlags = flags & 0x0F;

    // ── Type ──
    String type;
    if (typeCode == _typeShortLongForm) {
      type = reader.readString();
    } else {
      type = _shortToType[typeCode] ?? 'unknown';
    }

    // ── ID ──
    final id = reader.readString();

    // ── Sender ──
    final sender = reader.readString();

    // ── SenderNick ──
    String senderNick = '';
    if (boolFlags & _flagHasNick != 0) senderNick = reader.readString();

    // ── Recipient ──
    String? recipient;
    if (boolFlags & _flagHasRecipient != 0) recipient = reader.readString();

    // ── Payload ──
    String payload = '';
    if (boolFlags & _flagHasPayload != 0) payload = reader.readString();

    // ── Timestamp ──
    final timestamp = _unzigzag(reader.readUVarint());

    // ── TTL + HopCount ──
    final ttl = reader.readByte();
    final hopCount = reader.readByte();

    // ── Nonce ──
    String nonce = '';
    if (boolFlags & _flagHasNonce != 0) nonce = reader.readString();

    // ── KeyID ──
    String keyId = '';
    if (boolFlags & _flagHasKeyId != 0) keyId = reader.readString();

    return Message(
      id: id,
      type: type,
      sender: sender,
      senderNick: senderNick,
      recipient: recipient,
      payload: payload,
      timestamp: timestamp,
      ttl: ttl,
      hopCount: hopCount,
      nonce: nonce,
      keyId: keyId,
    );
  }

  /// Check if data is binary format (not JSON).
  static bool isBinary(Uint8List data) {
    if (data.isEmpty) return false;
    return data[0] != 0x7B; // '{' = 0x7B
  }

  /// Get short type name from binary header (for debugging).
  static String shortTypeName(Uint8List data) {
    if (data.isEmpty) return '?';
    final typeCode = (data[0] >> 4) & 0x0F;
    return _shortToType[typeCode] ?? '?';
  }

  /// Split data into BLE MTU-friendly frames (20 bytes).
  static List<Uint8List> mtuSplit(Uint8List data, {int mtu = 20}) {
    final frames = <Uint8List>[];
    for (var i = 0; i < data.length; i += mtu) {
      final end = (i + mtu > data.length) ? data.length : i + mtu;
      frames.add(data.sublist(i, end));
    }
    return frames;
  }

  static int _zigzag(int v) => (v << 1) ^ (v >> 63);
  static int _unzigzag(int v) => (v >> 1) ^ -(v & 1);
}

/// Helper to write bytes in the codec format.
class BytesWriter {
  final List<int> _bytes = [];

  void writeByte(int b) => _bytes.add(b & 0xFF);

  void writeUVarint(int v) {
    var value = v;
    while (value >= 0x80) {
      _bytes.add((value & 0x7F) | 0x80);
      value >>= 7;
    }
    _bytes.add(value & 0x7F);
  }

  void writeString(String s) {
    final encoded = utf8.encode(s);
    if (encoded.length <= 255) {
      _bytes.add(encoded.length);
    } else {
      _bytes.add(0xFF);
      _bytes.add((encoded.length >> 8) & 0xFF);
      _bytes.add(encoded.length & 0xFF);
    }
    _bytes.addAll(encoded);
  }

  Uint8List toBytes() => Uint8List.fromList(_bytes);
}

/// Helper to read bytes in the codec format.
class BytesReader {
  final Uint8List _data;
  int _offset = 0;

  BytesReader(this._data);

  void _ensure(int n) {
    if (_offset + n > _data.length) {
      throw FormatException('Unexpected end of data at offset $_offset');
    }
  }

  int readByte() {
    _ensure(1);
    return _data[_offset++];
  }

  int readUVarint() {
    var value = 0;
    var shift = 0;
    while (true) {
      _ensure(1);
      final byte = _data[_offset++];
      value |= (byte & 0x7F) << shift;
      if ((byte & 0x80) == 0) return value;
      shift += 7;
      if (shift > 63) throw FormatException('Varint too long');
    }
  }

  String readString() {
    _ensure(1);
    final first = _data[_offset++];
    int len;
    if (first == 0xFF) {
      _ensure(2);
      len = (_data[_offset] << 8) | _data[_offset + 1];
      _offset += 2;
      if (len == 0) return '';
    } else if (first == 0) {
      return '';
    } else {
      len = first;
    }
    _ensure(len);
    final result = utf8.decode(_data.sublist(_offset, _offset + len));
    _offset += len;
    return result;
  }

  int get offset => _offset;
  int get remaining => _data.length - _offset;
  bool get hasMore => _offset < _data.length;
}
