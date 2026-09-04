// Package models defines the Ripple application models.
// This mirrors the daemon's relay event frame:
//
//   { "type":"relay", "msg_id":"…", "msg_type":"chat",
//     "action":"received|forwarded|dropped|sent",
//     "from":"<peer that sent it to us>", "hops":1, "ttl":15, "ts":<nanos> }
//
// Relay events are factual records of a message's passage through the local
// mesh node. The topology map animates a hop for every event — nothing on
// screen is synthetic.
library;

class RelayEvent {
  final String messageId;
  final String messageType;
  final String action; // received | forwarded | dropped | sent
  final String? from; // peer this message arrived through (null = local)
  final int hops;
  final int ttl;
  final int ts; // Unix nanoseconds

  const RelayEvent({
    required this.messageId,
    required this.messageType,
    required this.action,
    this.from,
    required this.hops,
    required this.ttl,
    required this.ts,
  });

  factory RelayEvent.fromJson(Map<String, dynamic> json) => RelayEvent(
        messageId: json['msg_id'] as String? ?? '',
        messageType: json['msg_type'] as String? ?? 'chat',
        action: json['action'] as String? ?? 'received',
        from: json['from'] as String?,
        hops: json['hops'] as int? ?? 0,
        ttl: json['ttl'] as int? ?? 0,
        ts: json['ts'] as int? ?? 0,
      );

  /// Stable identity for deduplicating events in the UI.
  String get eventKey => '$messageId:$action:$ts';

  bool get isReceived => action == 'received';
  bool get isForwarded => action == 'forwarded';
  bool get isDropped => action == 'dropped';
  bool get isSent => action == 'sent';

  /// Human-readable one-liner for the audit strip under the map.
  String describe() {
    final who = (from == null || from!.isEmpty) ? 'you' : _short(from!);
    switch (action) {
      case 'received':
        return 'msg $_short(messageId) arrived from $who';
      case 'forwarded':
        return 'msg $_short(messageId) relayed onward (hops $hops, ttl $ttl)';
      case 'dropped':
        return 'msg $_short(messageId) stopped here ($_dropReason)';
      case 'sent':
        return 'you sent msg $_short(messageId)';
      default:
        return 'msg $_short(messageId) $action';
    }
  }

  String get _dropReason => ttl <= 0 ? 'ttl exhausted' : 'duplicate';

  String _short(String s) => s.length > 8 ? s.substring(0, 8) : s;
}
