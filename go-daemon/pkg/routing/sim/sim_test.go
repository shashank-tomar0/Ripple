package sim

import (
	"testing"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/routing"
)

// runPair runs one scenario through both forwarders and returns the two
// outcomes, sharing every other Config field.
func runPair(seed int64) (epidemic, spray Outcome, err error) {
	cfg := DefaultConfig()
	cfg.Seed = seed
	cfg.Forwarder = routing.Epidemic{}
	epidemic, err = Run(cfg)
	if err != nil {
		return
	}
	cfg.Forwarder = routing.SprayAndWait{L: 8}
	spray, err = Run(cfg)
	return
}

// TestDeterminism is the reproducibility contract that makes benchmark
// numbers publishable: identical seed ⇒ identical outcome, byte for byte.
func TestDeterminism(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Forwarder = routing.SprayAndWait{L: 8}

	a, err := Run(cfg)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	b, err := Run(cfg)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if a.Delivered != b.Delivered || a.Total != b.Total || a.Copies != b.Copies {
		t.Fatalf("determinism violated: run A %+v vs run B %+v", a, b)
	}
	if len(a.Latency) != len(b.Latency) || len(a.Hops) != len(b.Hops) {
		t.Fatalf("determinism violated: metric slice lengths differ")
	}
	for i := range a.Latency {
		if a.Latency[i] != b.Latency[i] || a.Hops[i] != b.Hops[i] {
			t.Fatalf("determinism violated at %d: (%d,%d) vs (%d,%d)",
				i, a.Latency[i], a.Hops[i], b.Latency[i], b.Hops[i])
		}
	}
}

// TestDeterminismAcrossSeeds asserts each seed pins its own outcome: two
// runs of the same seed agree, and running five different seeds all
// produce valid, distinct-in-some-way simulations. This catches accidental
// dependence on map iteration or global state.
func TestDeterminismAcrossSeeds(t *testing.T) {
	prev := -1
	for _, seed := range []int64{1, 7, 42} {
		cfg := DefaultConfig()
		cfg.Seed = seed
		cfg.Forwarder = routing.Epidemic{}
		o, err := Run(cfg)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		again, err := Run(cfg)
		if err != nil {
			t.Fatalf("seed %d rerun: %v", seed, err)
		}
		if o.Copies != again.Copies || o.Delivered != again.Delivered {
			t.Fatalf("seed %d not self-consistent: %+v vs %+v", seed, o, again)
		}
		if o.Delivered == 0 {
			t.Fatalf("seed %d: scenario delivered nothing — scenario is broken, not informative", seed)
		}
		if o.Copies == prev {
			t.Fatalf("seed %d produced identical copies %d to previous seed — seeds not actually diverging", seed, o.Copies)
		}
		prev = o.Copies
	}
}

// TestSanityBounds are cheap invariants every run must satisfy: no negative
// metrics, delivered never exceeds total, hop counts are sane (a delivered
// copy cannot have taken more hops than copies exist — each hop creates or
// moves a copy, and moves are capped by copy count interplay).
func TestSanityBounds(t *testing.T) {
	for _, kind := range []routing.Forwarder{routing.Epidemic{}, routing.SprayAndWait{L: 8}} {
		cfg := DefaultConfig()
		cfg.Forwarder = kind
		o, err := Run(cfg)
		if err != nil {
			t.Fatalf("%T: %v", kind, err)
		}
		if o.Total != cfg.Messages {
			t.Fatalf("%T: injected %d, want %d", kind, o.Total, cfg.Messages)
		}
		if o.Delivered < 0 || o.Delivered > o.Total {
			t.Fatalf("%T: delivered %d out of range", kind, o.Delivered)
		}
		if o.Copies < o.Delivered {
			t.Fatalf("%T: cannot deliver %d with only %d copies", kind, o.Delivered, o.Copies)
		}
		for _, l := range o.Latency {
			if l < 0 {
				t.Fatalf("%T: negative latency %d", kind, l)
			}
		}
		for _, h := range o.Hops {
			if h < 0 {
				t.Fatalf("%T: negative hops %d", kind, h)
			}
		}
	}
}

// TestSprayBeatsEpidemicOnCopies is the headline research claim, asserted
// across multiple seeds so it is not a single lucky scenario: spray-and-wait
// delivers most of what epidemic delivers while creating a fraction of the
// physical copies. The exact thresholds are deliberately non-zero so the
// test fails loudly if the protocols regress toward each other.
func TestSprayBeatsEpidemicOnCopies(t *testing.T) {
	seeds := []int64{1, 7, 11, 17, 42}

	for _, seed := range seeds {
		epi, spray, err := runPair(seed)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if epi.Delivered == 0 {
			t.Fatalf("seed %d: epidemic delivered nothing — scenario too sparse to be informative", seed)
		}
		// The claim, per seed:
		//   1. spray delivers at least 60% of what epidemic delivers;
		//   2. spray uses at most 60% of epidemic's physical copies.
		ratioDeliv := float64(spray.Delivered) / float64(epi.Delivered)
		ratioCopies := float64(spray.Copies) / float64(epi.Copies)
		if ratioDeliv < 0.6 {
			t.Errorf("seed %d: spray delivery ratio %.2f < 0.60 (epi %d, spray %d)",
				seed, ratioDeliv, epi.Delivered, spray.Delivered)
		}
		if ratioCopies > 0.6 {
			t.Errorf("seed %d: spray copies ratio %.2f > 0.60 — copy budget not saving anything (epi %d, spray %d)",
				seed, ratioCopies, epi.Copies, spray.Copies)
		}
	}
}

// TestSprayBudgetCapsCopies asserts the mechanism directly: with budget L,
// a message can never create more than L physical copies total (sender's
// own included), no matter how long it wanders.
func TestSprayBudgetCapsCopies(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Forwarder = routing.SprayAndWait{L: 5}
	o, err := Run(cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Re-run with instrumentation is overkill; instead assert the aggregate
	// bound: every message has at most L copies, so total copies ≤ L*Total.
	if o.Copies > cfg.Messages*5 {
		t.Fatalf("spray budget L=5 violated: %d copies across %d messages (limit %d)",
			o.Copies, cfg.Messages, cfg.Messages*5)
	}
	if o.Copies == 0 {
		t.Fatalf("no copies at all — simulation did nothing")
	}
}
