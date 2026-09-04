// Package sim is a deterministic event-driven simulator for the routing
// decision layer.
//
// Why a simulator? Because routing research questions are about mobility —
// who meets whom, when, and how often — and no amount of real-device
// testing can exercise that at scale. The ONE-style approach (a defined
// scenario, a contact model, injected message flows, measured outcomes) is
// the standard way DTN protocols are compared, and it is fully
// reproducible: with a fixed seed, the exact same schedule, contacts and
// delivery times come out — that is what makes the numbers publishable.
//
// It runs the SAME decision code as the live mesh will (pkg/routing), not
// a reimplementation: the Forwarder interface + EncounterStat updates are
// called directly. Mobility: random-waypoint in a square; two nodes are
// "in contact" while within radio range.
//
// Determinism contract: Run must be a pure function of its Config. No
// package-level mutable state, and every map iteration that feeds a
// decision is sorted — identical seed ⇒ identical outcome, and concurrent
// runs never interfere. Tests in sim_test.go assert both.
package sim

import (
	"fmt"
	"math"
	"math/rand"
	"sort"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/routing"
)

// Config describes one simulation scenario. Fields are automobiles for the
// bench CLI and tests; defaults via DefaultConfig.
type Config struct {
	Nodes         int     // number of nodes in the field
	AreaSize      float64 // square field side length (units)
	RadioRange    float64 // contact range (units): nodes within this distance meet
	MinSpeed      float64 // random-waypoint speed bounds (units per tick)
	MaxSpeed      float64
	Messages      int   // messages injected
	InjectionFrom int   // first tick of injection
	InjectionTo   int   // last tick of injection (inclusive)
	TTL           int64 // message network lifetime (ticks)
	Duration      int64 // total simulation length (ticks)
	Seed          int64 // PRNG seed; identical seed ⇒ identical run
	Forwarder     routing.Forwarder
}

// DefaultConfig returns a balanced medium-density scenario.
func DefaultConfig() Config {
	return Config{
		Nodes:         30,
		AreaSize:      25,
		RadioRange:    4.5,
		MinSpeed:      1,
		MaxSpeed:      3,
		Messages:      40,
		InjectionFrom: 10,
		InjectionTo:   400,
		TTL:           100,
		Duration:      500,
		Seed:          42,
	}
}

// Outcome aggregates the results of one simulation run.
type Outcome struct {
	Config    Config
	Delivered int
	Total     int
	Latency   []int64 // ticks to deliver, per delivered message (sorted by message ID)
	Hops      []int   // hops of the delivered copy, per delivered message (sorted by message ID)
	Copies    int     // total physical copies created across all messages
}

// DeliveredFraction is the delivery ratio.
func (o Outcome) DeliveredFraction() float64 {
	if o.Total == 0 {
		return 0
	}
	return float64(o.Delivered) / float64(o.Total)
}

// AvgLatency returns mean delivery latency over delivered messages (ticks).
func (o Outcome) AvgLatency() float64 {
	return meanInt64(o.Latency)
}

// AvgHops returns mean hop count over delivered messages.
func (o Outcome) AvgHops() float64 {
	return meanInt(o.Hops)
}

// AvgCopiesPerMessage is the overhead metric: physical copies per message.
func (o Outcome) AvgCopiesPerMessage() float64 {
	if o.Total == 0 {
		return 0
	}
	return float64(o.Copies) / float64(o.Total)
}

func meanInt64(xs []int64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s int64
	for _, x := range xs {
		s += x
	}
	return float64(s) / float64(len(xs))
}

func meanInt(xs []int) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s int
	for _, x := range xs {
		s += x
	}
	return float64(s) / float64(len(xs))
}

// ── internal state ─────────────────────────────────────────────────────

type pos struct{ x, y float64 }

type node struct {
	id     string
	pos    pos
	target pos
	speed  float64
	stats  map[string]routing.EncounterStat
	buf    []bufEntry
	has    map[string]bool // message IDs in buf — O(1) presence mirror
}

// bufEntry is one physical copy of a message held by a node. hops counts
// the hops THIS copy has taken — per-copy state, the thing
// routing.MessageState deliberately excludes.
type bufEntry struct {
	msg  *routing.MessageState
	hops int
}

// delivery records the outcome metrics for one delivered message, keyed by
// message ID inside a single Run — never package-level.
type delivery struct {
	latency int64
	hops    int
}

// Run executes the scenario described by cfg and returns the outcome. It
// is a pure function of cfg: no shared state, deterministic iteration.
func Run(cfg Config) (Outcome, error) {
	if cfg.Nodes < 2 {
		return Outcome{}, fmt.Errorf("need at least 2 nodes, got %d", cfg.Nodes)
	}
	if cfg.InjectionTo <= cfg.InjectionFrom {
		return Outcome{}, fmt.Errorf("injection window invalid: %d..%d", cfg.InjectionFrom, cfg.InjectionTo)
	}
	if cfg.Forwarder == nil {
		return Outcome{}, fmt.Errorf("no forwarder configured")
	}

	rng := rand.New(rand.NewSource(cfg.Seed))

	nodes := make([]*node, cfg.Nodes)
	for i := range nodes {
		nodes[i] = &node{
			id:    fmt.Sprintf("n%02d", i),
			pos:   pos{rng.Float64() * cfg.AreaSize, rng.Float64() * cfg.AreaSize},
			stats: make(map[string]routing.EncounterStat),
			has:   make(map[string]bool),
		}
		newWaypoint(nodes[i], cfg, rng)
	}

	// Shared message states keyed by ID (one state per logical message;
	// physical copies live in per-node buffers).
	states := map[string]*routing.MessageState{}
	delivered := map[string]delivery{}

	injected := 0
	stride := 1
	if msgs := cfg.Messages; msgs > 1 {
		stride = maxInt(1, (cfg.InjectionTo-cfg.InjectionFrom)/(msgs-1))
	}
	msgSeq := 0

	for tick := int64(0); tick < cfg.Duration; tick++ {
		// ── inject ──
		if injected < cfg.Messages && tick >= int64(cfg.InjectionFrom) &&
			(tick-int64(cfg.InjectionFrom))%int64(stride) == 0 {
			src, dst := pickPair(rng, cfg.Nodes)
			id := fmt.Sprintf("m%03d", msgSeq)
			msgSeq++
			st := &routing.MessageState{
				ID:          id,
				Src:         nodes[src].id,
				Dst:         nodes[dst].id,
				Kind:        routing.KindChat,
				CreatedAt:   tick,
				TTL:         cfg.TTL,
				CopiesMade:  1, // the source's own copy
				DeliveredAt: 0,
			}
			states[id] = st
			nodes[src].has[id] = true
			nodes[src].buf = append(nodes[src].buf, bufEntry{msg: st, hops: 0})
			injected++
		}

		// ── move ──
		for _, n := range nodes {
			step(n, cfg, rng)
		}

		// ── contacts (deterministic order: ascending node index pairs) ──
		contacts := [][2]int{}
		for i := 0; i < cfg.Nodes; i++ {
			for j := i + 1; j < cfg.Nodes; j++ {
				if dist(nodes[i].pos, nodes[j].pos) <= cfg.RadioRange {
					contacts = append(contacts, [2]int{i, j})
				}
			}
		}

		inContact := make([]bool, cfg.Nodes)
		for _, c := range contacts {
			inContact[c[0]] = true
			inContact[c[1]] = true
		}

		// ── process meetings ──
		for _, c := range contacts {
			a, b := nodes[c[0]], nodes[c[1]]
			// Encounter bookkeeping first, both directions. updateEncounter
			// reads the OTHER node's table for transitivity; the call order
			// is fixed, so the values are deterministic.
			updateEncounter(a, b, tick)
			updateEncounter(b, a, tick)

			// Snapshot both queues at contact start: a copy handed over in
			// this contact is NOT acted upon in the same contact (ONE-style
			// convention — otherwise copies ping-pong: A→B and B hands a
			// fresh copy straight back while the budget allows).
			aSnap := snapshotBuf(a)
			bSnap := snapshotBuf(b)
			exchange(a, b, aSnap, tick, cfg.Forwarder, states, delivered)
			exchange(b, a, bSnap, tick, cfg.Forwarder, states, delivered)
		}

		// ── age predictability for nodes that met nobody this tick
		//    (deterministic order: ascending node index) ──
		for i, n := range nodes {
			if inContact[i] {
				continue
			}
			ids := make([]string, 0, len(n.stats))
			for id := range n.stats {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				st := n.stats[id]
				st.AgePredictability(routing.AgeUnits)
				n.stats[id] = st
			}
		}

		// ── sweep delivered/expired copies out of buffers ──
		for _, n := range nodes {
			kept := n.buf[:0]
			for _, e := range n.buf {
				if e.msg.DeliveredAt != 0 || !e.msg.Expired(tick) {
					kept = append(kept, e)
				}
			}
			n.buf = kept
			n.has = make(map[string]bool, len(kept))
			for _, e := range kept {
				n.has[e.msg.ID] = true
			}
		}
	}

	// ── accounting ──
	out := Outcome{Config: cfg, Total: injected}
	ids := make([]string, 0, len(states))
	for id := range states {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		st := states[id]
		out.Copies += st.CopiesMade
		if st.DeliveredAt > 0 {
			out.Delivered++
			d := delivered[id]
			out.Latency = append(out.Latency, d.latency)
			out.Hops = append(out.Hops, d.hops)
		}
	}
	return out, nil
}

// exchange processes one direction of a contact: the copies node a held at
// the moment the contact began (snapshot) are offered to node b. The
// forwarder's decision is applied to a's and b's LIVE buffers.
func exchange(a, b *node, snapshot []bufEntry, tick int64, f routing.Forwarder, states map[string]*routing.MessageState, delivered map[string]delivery) {
	// Forwarding decisions use the predictability tables as they were at
	// contact start (aSnap/bSnap in Run's caller): a meeting cannot
	// bootstrap its own forwarding decision. These snapshots were taken
	// before encounter bookkeeping mutated the stats.
	aSnap := snapshotStats(a)
	bSnap := snapshotStats(b)

	for _, entry := range snapshot {
		msg := entry.msg
		if msg.DeliveredAt != 0 {
			continue
		}
		myStat := statFor(aSnap, msg.Dst)
		peerStat := statFor(bSnap, msg.Dst)
		action := f.Decide(msg, a.id, b.id, myStat, peerStat, b.hasCopy(msg.ID))

		switch {
		case action == routing.ActionDeliver:
			// Destination reached: record the win; the copy is consumed.
			msg.DeliveredAt = tick
			delivered[msg.ID] = delivery{latency: tick - msg.CreatedAt, hops: entry.hops}
			dropFrom(a, msg.ID)
		case action == routing.ActionSpray:
			// New copy at the peer; sender keeps its own.
			msg.CopiesMade++
			give(b, bufEntry{msg: msg, hops: entry.hops + 1})
		case action == routing.ActionHandoff:
			// Copy moves to the peer.
			give(b, bufEntry{msg: msg, hops: entry.hops + 1})
			dropFrom(a, msg.ID)
		}
	}
}

// snapshotBuf captures a node's queue at a point in time; decisions use it
// so copies created mid-contact cannot be acted on in the same contact.
func snapshotBuf(n *node) []bufEntry {
	s := make([]bufEntry, len(n.buf))
	copy(s, n.buf)
	return s
}

// updateEncounter records that a met b at tick and applies PROPHET
// transitivity from b's current table.
func updateEncounter(a, b *node, tick int64) {
	st := a.stats[b.id]
	peerStats := make(map[string]routing.EncounterStat, len(b.stats))
	for id := range b.stats {
		peerStats[id] = b.stats[id]
	}
	st.RecordEncounter(tick, peerStats)
	a.stats[b.id] = st
}

func snapshotStats(n *node) map[string]routing.EncounterStat {
	m := make(map[string]routing.EncounterStat, len(n.stats))
	for id, st := range n.stats {
		m[id] = st
	}
	return m
}

func statFor(snap map[string]routing.EncounterStat, dst string) routing.EncounterStat {
	if st, ok := snap[dst]; ok {
		return st
	}
	return routing.EncounterStat{Peer: dst}
}

// give appends a copy to n's buffer and mirrors it in the O(1) presence
// map (a node never holds more than one copy of a given message).
func give(n *node, e bufEntry) {
	n.has[e.msg.ID] = true
	n.buf = append(n.buf, e)
}

// hasCopy is O(1) — called for every offered copy of every contact, so a
// linear scan here makes the whole simulator quadratic.
func (n *node) hasCopy(id string) bool {
	return n.has[id]
}

func dropFrom(n *node, id string) {
	for i, e := range n.buf {
		if e.msg.ID == id {
			n.buf = append(n.buf[:i], n.buf[i+1:]...)
			delete(n.has, id)
			return
		}
	}
}

func newWaypoint(n *node, cfg Config, rng *rand.Rand) {
	n.target = pos{rng.Float64() * cfg.AreaSize, rng.Float64() * cfg.AreaSize}
	n.speed = cfg.MinSpeed + rng.Float64()*(cfg.MaxSpeed-cfg.MinSpeed)
}

func step(n *node, cfg Config, rng *rand.Rand) {
	dx := n.target.x - n.pos.x
	dy := n.target.y - n.pos.y
	d := math.Hypot(dx, dy)
	if d < 1e-9 {
		newWaypoint(n, cfg, rng)
		return
	}
	move := n.speed
	if move > d {
		move = d
	}
	n.pos.x += dx / d * move
	n.pos.y += dy / d * move
}

func dist(p, q pos) float64 {
	return math.Hypot(p.x-q.x, p.y-q.y)
}

func pickPair(rng *rand.Rand, n int) (int, int) {
	a := rng.Intn(n)
	b := rng.Intn(n - 1)
	if b >= a {
		b++
	}
	return a, b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// String summarizes the outcome.
func (o Outcome) String() string {
	return fmt.Sprintf("delivered %d/%d (%.0f%%)  avg latency %v ticks  avg hops %.1f  copies/message %.1f",
		o.Delivered, o.Total, o.DeliveredFraction()*100,
		o.AvgLatency(), o.AvgHops(), o.AvgCopiesPerMessage())
}
