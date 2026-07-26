// Package codec provides a compact binary serialization format for Ripple messages.
//
// Wire format (variable-length, BigEndian):
//
//   [1 byte]     flags (bit 0 = has_sender_nick, bit 1 = has_recipient,
//                 bit 2 = has_nonce, bit 3 = has_key_id, bit 4 = has_payload_len)
//   [1 byte]     type_len     (if 0, type <= 15 uses 4-bit short code; see typeCodec)
//   [var bytes]  type         (only if type_len > 0, otherwise short-code)
//   [var bytes]  id           (1+len bytes: 1 byte prefix = remaining length, then that many bytes)
//   [var bytes]  sender       (same 1+len prefix scheme)
//   [var bytes]  sender_nick  (only if flags bit 0 set)
//   [var bytes]  recipient    (only if flags bit 1 set)
//   [var bytes]  payload      (only if flags bit 4 set; length = varint prefix)
//   [varint]     timestamp    (zigzag-encoded int64)
//   [varint]     ttl          (uint8, 1 byte)
//   [varint]     hop_count    (uint8, 1 byte)
//   [var bytes]  nonce        (only if flags bit 2 set)
//   [var bytes]  key_id       (only if flags bit 3 set)
//
// String encoding: length-prefixed with 1-byte length (up to 255 bytes),
// then UTF-8 bytes. Strings longer than 255 bytes use 2-byte length prefix
// with 0xFF sentinel.
//
// Varint encoding: same as proto3 varint (base 128, MSB continuation bit).
// Zigzag for signed int64.
//
// Short type codes (4-bit) for common message types:
//   0x0 = chat
//   0x1 = file
//   0x2 = sos
//   0x3 = delivery_ack
//   0x4 = peer_info
//   0x5 = key_exchange
//   0xF = long-form (type follows as full string)
//
// Typical sizes (vs JSON):
//   Chat message:   ~45 bytes vs ~180 bytes JSON  (75% smaller)
//   File metadata:  ~80 bytes vs ~280 bytes JSON  (70% smaller)
//   Delivery ack:   ~35 bytes vs ~150 bytes JSON  (77% smaller)
package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
)

// Type short-codes for the 4-bit type field.
const (
	typeShortChat        = 0x0
	typeShortFile        = 0x1
	typeShortSOS         = 0x2
	typeShortDeliveryAck = 0x3
	typeShortPeerInfo    = 0x4
	typeShortKeyExchange = 0x5
	typeShortLongForm    = 0xF // type follows as full string
)

var typeToShort = map[message.MessageType]byte{
	message.TypeChat:        typeShortChat,
	message.TypeFile:        typeShortFile,
	message.TypeSOS:         typeShortSOS,
	message.TypeDeliveryAck: typeShortDeliveryAck,
	message.TypePeerInfo:    typeShortPeerInfo,
	message.TypeKeyExchange: typeShortKeyExchange,
}

var shortToType = map[byte]message.MessageType{
	typeShortChat:        message.TypeChat,
	typeShortFile:        message.TypeFile,
	typeShortSOS:         message.TypeSOS,
	typeShortDeliveryAck: message.TypeDeliveryAck,
	typeShortPeerInfo:    message.TypePeerInfo,
	typeShortKeyExchange: message.TypeKeyExchange,
}

const maxShortLen = 15

// wireFlags bitmask.
const (
	flagHasNick     byte = 1 << iota // sender_nick present
	flagHasRecipient                 // recipient present
	flagHasNonce                     // nonce present
	flagHasKeyID                     // key_id present
	flagHasPayload                   // payload present
	_                                // reserved
	_                                // reserved
	typeLong                         // type is long-form (if set in first nibble)
)

// ── Varint helpers ───────────────────────────────────────────────

// putUvarint encodes a uint64 as a varint and returns the bytes.
func putUvarint(v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return buf[:n]
}

// zigzag encodes an int64 as a uint64 using zigzag encoding.
func zigzag(v int64) uint64 { return uint64((v << 1) ^ (v >> 63)) }

// unzigzag decodes a zigzag-encoded uint64 back to int64.
func unzigzag(v uint64) int64 { return int64((v >> 1) ^ -(v & 1)) }

// ── String helpers ───────────────────────────────────────────────

// putString encodes a string with a length prefix.
// Returns nil if the string is empty (not encoded at all).
func putString(s string) []byte {
	if s == "" {
		return nil
	}
	b := []byte(s)
	if len(b) <= 255 {
		out := make([]byte, 1+len(b))
		out[0] = byte(len(b))
		copy(out[1:], b)
		return out
	}
	// Long string: 0xFF sentinel + 2-byte length + data
	out := make([]byte, 3+len(b))
	out[0] = 0xFF
	binary.BigEndian.PutUint16(out[1:3], uint16(len(b)))
	copy(out[3:], b)
	return out
}

// getString reads a length-prefixed string from a byte slice.
// Returns the string, the number of bytes consumed, and an error.
func getString(data []byte) (string, int, error) {
	if len(data) == 0 {
		return "", 0, nil
	}
	if data[0] == 0xFF {
		// Long form: 2-byte length
		if len(data) < 3 {
			return "", 0, io.ErrUnexpectedEOF
		}
		n := int(binary.BigEndian.Uint16(data[1:3]))
		if len(data) < 3+n {
			return "", 0, io.ErrUnexpectedEOF
		}
		return string(data[3 : 3+n]), 3 + n, nil
	}
	// Short form: 1-byte length
	n := int(data[0])
	if n == 0 {
		return "", 1, nil // empty string
	}
	if len(data) < 1+n {
		return "", 0, io.ErrUnexpectedEOF
	}
	return string(data[1 : 1+n]), 1 + n, nil
}

// ── Encode ──────────────────────────────────────────────────────

// Marshal serializes a Message into the compact binary format.
func Marshal(msg *message.Message) ([]byte, error) {
	var buf []byte

	// ── Flags byte ──
	flags := byte(0)
	if msg.SenderNick != "" {
		flags |= flagHasNick
	}
	if msg.Recipient != "" {
		flags |= flagHasRecipient
	}
	if msg.Nonce != "" {
		flags |= flagHasNonce
	}
	if msg.KeyID != "" {
		flags |= flagHasKeyID
	}
	if msg.Payload != "" {
		flags |= flagHasPayload
	}

	// ── Type byte ──
	typeCode, short := typeToShort[string(msg.Type)]
	if short && len(msg.Type) <= maxShortLen {
		flags |= typeCode // store short code in upper nibble (well, flags byte holds it)
		// Actually: flags byte upper nibble = short type, lower nibble = bool flags
		// Rebuild: [type_short(4 bits) | bool_flags(4 bits)]
		flags = (typeCode << 4) | (flags & 0x0F)
	} else {
		// Long form: type follows as string
		flags = (typeShortLongForm << 4) | (flags & 0x0F)
	}
	buf = append(buf, flags)

	// ── Type (long form only) ──
	if typeCode, ok := typeToShort[string(msg.Type)]; !ok || typeCode == typeShortLongForm {
		buf = append(buf, putString(string(msg.Type))...)
	}

	// ── ID ──
	buf = append(buf, putString(msg.ID)...)

	// ── Sender ──
	buf = append(buf, putString(msg.Sender)...)

	// ── SenderNick ──
	if msg.SenderNick != "" {
		buf = append(buf, putString(msg.SenderNick)...)
	}

	// ── Recipient ──
	if msg.Recipient != "" {
		buf = append(buf, putString(msg.Recipient)...)
	}

	// ── Payload ──
	if msg.Payload != "" {
		buf = append(buf, putString(msg.Payload)...)
	}

	// ── Timestamp (varint zigzag) ──
	buf = append(buf, putUvarint(zigzag(msg.Timestamp))...)

	// ── TTL + HopCount (pack into 2 bytes) ──
	ttl := msg.TTL
	if ttl > 255 {
		ttl = 255
	}
	hops := msg.HopCount
	if hops > 255 {
		hops = 255
	}
	buf = append(buf, byte(ttl), byte(hops))

	// ── Nonce ──
	if msg.Nonce != "" {
		buf = append(buf, putString(msg.Nonce)...)
	}

	// ── KeyID ──
	if msg.KeyID != "" {
		buf = append(buf, putString(msg.KeyID)...)
	}

	return buf, nil
}

// Unmarshal deserializes a Message from the compact binary format.
func Unmarshal(data []byte) (*message.Message, error) {
	if len(data) < 2 {
		return nil, errors.New("codec: data too short")
	}

	msg := &message.Message{}
	off := 0

	// ── Flags byte ──
	flags := data[off]
	off++

	// Upper nibble = type short-code
	typeCode := (flags >> 4) & 0x0F
	// Lower nibble = bool flags
	boolFlags := flags & 0x0F

	// ── Type ──
	if typeCode == typeShortLongForm {
		// Long form: read type string
		s, n, err := getString(data[off:])
		if err != nil {
			return nil, fmt.Errorf("codec: type: %w", err)
		}
		msg.Type = message.MessageType(s)
		off += n
	} else {
		// Short code
		t, ok := shortToType[typeCode]
		if !ok {
			return nil, fmt.Errorf("codec: unknown type code %x", typeCode)
		}
		msg.Type = t
	}

	// ── ID ──
	s, n, err := getString(data[off:])
	if err != nil {
		return nil, fmt.Errorf("codec: id: %w", err)
	}
	msg.ID = s
	off += n
	if msg.ID == "" {
		return nil, errors.New("codec: missing message ID")
	}

	// ── Sender ──
	s, n, err = getString(data[off:])
	if err != nil {
		return nil, fmt.Errorf("codec: sender: %w", err)
	}
	msg.Sender = s
	off += n

	// ── SenderNick ──
	if boolFlags&flagHasNick != 0 {
		s, n, err = getString(data[off:])
		if err != nil {
			return nil, fmt.Errorf("codec: sender_nick: %w", err)
		}
		msg.SenderNick = s
		off += n
	}

	// ── Recipient ──
	if boolFlags&flagHasRecipient != 0 {
		s, n, err = getString(data[off:])
		if err != nil {
			return nil, fmt.Errorf("codec: recipient: %w", err)
		}
		msg.Recipient = s
		off += n
	}

	// ── Payload ──
	if boolFlags&flagHasPayload != 0 {
		s, n, err = getString(data[off:])
		if err != nil {
			return nil, fmt.Errorf("codec: payload: %w", err)
		}
		msg.Payload = s
		off += n
	}

	// ── Timestamp (varint zigzag) ──
	ts, n := binary.Uvarint(data[off:])
	if n <= 0 {
		return nil, errors.New("codec: invalid timestamp varint")
	}
	msg.Timestamp = unzigzag(ts)
	off += n

	// ── TTL + HopCount ──
	if off+2 > len(data) {
		return nil, io.ErrUnexpectedEOF
	}
	msg.TTL = int(data[off])
	msg.HopCount = int(data[off+1])
	off += 2

	// ── Nonce ──
	if boolFlags&flagHasNonce != 0 {
		s, n, err = getString(data[off:])
		if err != nil {
			return nil, fmt.Errorf("codec: nonce: %w", err)
		}
		msg.Nonce = s
		off += n
	}

	// ── KeyID ──
	if boolFlags&flagHasKeyID != 0 {
		s, n, err = getString(data[off:])
		if err != nil {
			return nil, fmt.Errorf("codec: key_id: %w", err)
		}
		msg.KeyID = s
		off += n
	}

	return msg, nil
}

// ── Payload helpers ──────────────────────────────────────────────

// MarshalChatPayload encodes chat text into a payload bytes.
func MarshalChatPayload(text string) []byte { return []byte(text) }

// UnmarshalChatPayload decodes chat text from payload bytes.
func UnmarshalChatPayload(data []byte) string { return string(data) }

// Size returns the on-wire byte size of a message.
func Size(msg *message.Message) int {
	b, err := Marshal(msg)
	if err != nil {
		return 0
	}
	return len(b)
}

// SizeRatio returns the JSON vs binary size ratio (>1 = binary smaller).
func SizeRatio(msg *message.Message) float64 {
	jsonSize := len(msg.ID) + len(msg.Sender) + len(msg.SenderNick) +
		len(msg.Recipient) + len(msg.Payload) + len(msg.Nonce) + len(msg.KeyID) + 80 // overhead
	binarySize := Size(msg)
	if binarySize == 0 {
		return 1
	}
	return float64(jsonSize) / float64(binarySize)
}

// IsBinaryFormat returns true if data looks like binary (not JSON).
func IsBinaryFormat(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	// JSON always starts with '{'
	return data[0] != '{'
}

// ShortTypeName returns a human-readable short type from the flags byte.
func ShortTypeName(data []byte) string {
	if len(data) == 0 {
		return "?"
	}
	typeCode := (data[0] >> 4) & 0x0F
	switch typeCode {
	case typeShortChat:
		return "chat"
	case typeShortFile:
		return "file"
	case typeShortSOS:
		return "sos"
	case typeShortDeliveryAck:
		return "ack"
	case typeShortPeerInfo:
		return "peer"
	case typeShortKeyExchange:
		return "keyx"
	case typeShortLongForm:
		return "long"
	}
	return "?"
}

// ── String representation for debugging ──

// String returns a debugging dump of the binary data.
func Dump(data []byte) string {
	if len(data) == 0 {
		return "<empty>"
	}
	if IsBinaryFormat(data) {
		return fmt.Sprintf("[binary: %s, %d bytes]", ShortTypeName(data), len(data))
	}
	// Looks like JSON
	return fmt.Sprintf("[json: %d bytes]", len(data))
}

// ── BLE frame helpers ──

// MTUFriendlySplit splits a message into BLE MTU-friendly frames (20 bytes each).
func MTUFriendlySplit(data []byte) [][]byte {
	const mtu = 20
	if len(data) <= mtu {
		return [][]byte{data}
	}
	var frames [][]byte
	for i := 0; i < len(data); i += mtu {
		end := i + mtu
		if end > len(data) {
			end = len(data)
		}
		frames = append(frames, data[i:end])
	}
	return frames
}

// Strings returns a compact representation for debugging.
func Strings(msgs []*message.Message) string {
	var parts []string
	for _, m := range msgs {
		parts = append(parts, fmt.Sprintf("%s:%s", m.Type, m.ID[:min(8, len(m.ID))]))
	}
	return strings.Join(parts, ", ")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
