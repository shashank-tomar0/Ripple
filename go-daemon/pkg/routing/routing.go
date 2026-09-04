// Package routing implements the store-and-forward decision layer for the
// Ripple mesh, independent of any particular transport.
//
// The mesh today floods (epidemic): every node re-broadcasts every new
// message while TTL remains. That guarantees delivery or nothing — it has
// no notion of a destination. This package replaces that with *destination-
// aware* forwarding, the standard DTN family, so Ripple can route, not
// just flood:
//
//   - Predictability (PROPHET): each node keeps, for every peer it has
//     encountered, a delivery predictability value P in [0,1] — how
//     likely that peer is to carry a message toward its destination.
//     It rises on every encounter, ages over time, and *transits* through
//     mutual acquaintances (if A often meets B and B often meets C, then
//     A's predictability for C rises).
//
//   - Spray-and-wait: each message starts with a finite copy budget L.
//     Phase "spray": hand copies to the first L distinct nodes met, so L
//     copies are in flight. Phase "wait": a node holding a copy keeps it
//     until it meets the destination — or, guided by predictability, a
//     node with a better chance (the PROPHET hybrid).
//
// Everything here is a pure function of observable state (encounter stats
// and message state), so the exact same code runs in two places: the live
// mesh node (deciding who to forward to over direct streams) and the
// deterministic simulator in pkg/routing/sim (measuring protocol behavior
// at scale under mobility). A feature "exists" here only if a test asserts
// it — see routing_test.go and sim/sim_test.go.
package routing

// Predictability kinetics — PROPHET (Lindgren, Doria, Schelén 2003), §2.
// PInit is the predictability granted by a first encounter; Beta dampens
// transitivity; Gamma and AgeUnits control how fast predictability decays
// when two nodes are apart. Standard published values; a deployment tunes
// them per mobility profile.
const (
	PInit      = 0.75
	Beta       = 0.25
	Gamma      = 0.98
	AgeUnits   = 1
	MaxPredict = 1.0
)

// EncounterStat is a node's memory of one other node: when they last met,
// how many times, and the current delivery predictability for that peer.
type EncounterStat struct {
	Peer           string
	LastSeen       int64 // time of the last encounter
	Encounters     int64
	Predictability float64
}

// UpdatePredictabilityOnEncounter applies the PROPHET encounter update:
//
//	P(a,b) = P(a,b)_old + (1 - P(a,b)_old) * P_init
//
// so repeated meetings asymptotically approach 1 but never reach it on
// encounters alone.
func (s *EncounterStat) UpdatePredictabilityOnEncounter() {
	s.Predictability += (1 - s.Predictability) * PInit
}

// AgePredictability decays predictability for a node that has NOT been in
// contact for "units" time units:
//
//	P(a,b) = P(a,b)_old * Gamma^units
func (s *EncounterStat) AgePredictability(units int64) {
	if units <= 0 {
		return
	}
	r := 1.0
	for i := int64(0); i < units; i++ {
		r *= Gamma
	}
	s.Predictability *= r
	if s.Predictability < 1e-9 {
		s.Predictability = 0
	}
}

// TransitGain returns the PROPHET transitivity increment for A's
// predictability for C when A meets B (with updated predictability pAB)
// and B holds predictability pBC for C:
//
//	gain = (1 - P_AC_old) * P_AB * P_BC * Beta
//
// The (1 - P_AC_old) dampener is what keeps the recurrence asymptotic:
// the closer A already is to C, the less a transit through B adds.
func TransitGain(pACold, pAB, pBC float64) float64 {
	return (1 - pACold) * pAB * pBC * Beta
}

// RecordEncounter mutates a node's stat for "peer" to reflect a meeting at
// time now, using the peer's own stats for third parties to apply the
// transitivity update. It returns the updated stat.
func (s *EncounterStat) RecordEncounter(now int64, peerStats map[string]EncounterStat) {
	s.Encounters++
	s.LastSeen = now

	// PROPHET order matters: first update P(a,b) from the encounter itself,
	// THEN use the *updated* P(a,b) for the transitivity update — the paper
	// applies P(a,c) += (1-P(a,c)) * P(a,b) * P(b,c) * Beta with the fresh
	// P(a,b). Using the stale pre-encounter value silently zeroes the
	// transitivity gain on first meetings.
	s.UpdatePredictabilityOnEncounter()

	// Transitivity: anything the peer is likely to reach, we are now a
	// little more likely to reach too. The P_AB factor uses the FRESH
	// predictability for the just-met peer, exactly as the paper does.
	pAB := s.Predictability
	for id, other := range peerStats {
		if id == s.Peer || other.Predictability <= 0 {
			continue
		}
		s.Predictability += TransitGain(s.Predictability, pAB, other.Predictability)
		if s.Predictability > MaxPredict {
			s.Predictability = MaxPredict
		}
	}
}

// Aging returns how many time units have passed since the stat's last
// encounter, from the perspective of time now.
func (s *EncounterStat) Aging(now int64) int64 {
	if now <= s.LastSeen {
		return 0
	}
	return (now - s.LastSeen) / AgeUnits
}

// MsgKind distinguishes routing-relevant message kinds.
type MsgKind int

const (
	KindChat MsgKind = iota
	KindAck
	KindSOS
)

// MessageState is the routing-relevant state shared by all physical copies
// of one message. It is transport-agnostic: the simulator builds it
// directly, and the live mesh will project it from the wire message.
// Per-copy state (hops taken by THIS copy) is deliberately not here — it
// belongs to the carrier, not to the message.
type MessageState struct {
	ID          string
	Src         string
	Dst         string
	Kind        MsgKind
	CreatedAt   int64 // time of creation
	TTL         int64 // network lifetime in time units
	CopiesMade  int   // physical copies created so far (overhead metric)
	DeliveredAt int64 // 0 until delivered
}

// Expired reports whether the message's network lifetime has elapsed at
// time now.
func (m *MessageState) Expired(now int64) bool {
	return m.DeliveredAt != 0 || now-m.CreatedAt >= m.TTL
}

// SprayAction is the decision a forwarder returns when a node holding a
// copy of a message meets another node.
type SprayAction int

const (
	// ActionDeliver: the peer IS the destination — hand the copy over.
	ActionDeliver SprayAction = iota
	// ActionSpray: hand a new copy; budget not spent, peer is a fresh
	// relay.
	ActionSpray
	// ActionHandoff: hand the WHOLE copy (budget spent) to a peer with
	// strictly higher predictability for the destination.
	ActionHandoff
	// ActionKeep: hold the copy — no better relay in view.
	ActionKeep
	// ActionDup: the peer already holds a live copy; handing another would
	// be waste.
	ActionDup
)

// Forwarder is the routing decision interface. Both the simulator and the
// live mesh call it with purely local knowledge; the caller owns bookkeeping
// (which peers hold copies, per-copy hop counts, budget accounting).
type Forwarder interface {
	// Decide returns the action when "me" (holding a copy of msg) meets
	// "peer". myStat is our predictability for msg.Dst; peerStat is the
	// peer's (zero-valued if the peer never met the destination).
	// peerHoldsCopy reports whether the peer already carries a live copy.
	Decide(msg *MessageState, me, peer string, myStat, peerStat EncounterStat, peerHoldsCopy bool) SprayAction
}

// SprayAndWait is the default forwarder: PROPHET-guided spray-and-wait.
// L is the spray budget (total physical copies handed out, sender's own
// initial copy included).
type SprayAndWait struct {
	L int
}

// Decide implements the policy:
//   - the destination always receives;
//   - while fewer than L copies exist, hand a new copy to any fresh peer;
//   - once L copies exist, wait — hand our copy on only to a peer with
//     strictly higher predictability for the destination.
func (f SprayAndWait) Decide(msg *MessageState, me, peer string, myStat, peerStat EncounterStat, peerHoldsCopy bool) SprayAction {
	if peer == msg.Dst {
		return ActionDeliver
	}
	if peerHoldsCopy {
		return ActionDup
	}
	if msg.CopiesMade < f.L {
		return ActionSpray
	}
	if peerStat.Predictability > myStat.Predictability {
		return ActionHandoff
	}
	return ActionKeep
}

// Epidemic is the baseline forwarder, faithful to the mesh's current flood:
// every fresh contact gets a copy. Copies are unbounded; only the network
// lifetime (TTL) bounds it.
type Epidemic struct{}

// Decide implements unconstrained flooding to any fresh peer.
func (Epidemic) Decide(msg *MessageState, me, peer string, myStat, peerStat EncounterStat, peerHoldsCopy bool) SprayAction {
	if peer == msg.Dst {
		return ActionDeliver
	}
	if peerHoldsCopy {
		return ActionDup
	}
	return ActionSpray
}

// Stats reports whether the action creates a brand-new copy (as opposed to
// moving an existing one or creating nothing).
func CreatesCopy(action SprayAction) bool {
	return action == ActionSpray
}

// MovesCopy reports whether the action hands the carrier's own copy over
// (releasing the carrier).
func MovesCopy(action SprayAction) bool {
	return action == ActionHandoff || action == ActionDeliver
}

// KeepsCopy reports whether the carrier retains its copy.
func KeepsCopy(action SprayAction) bool {
	return action == ActionKeep || action == ActionSpray || action == ActionDup
}
