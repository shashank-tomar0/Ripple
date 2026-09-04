package routing

import (
	"math"
	"testing"
)

// assertClose is a tiny tolerance helper so predictability math never
// depends on exact float bits.
func assertClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestEncounterUpdate pins the PROPHET encounter recurrence.
func TestEncounterUpdate(t *testing.T) {
	var s EncounterStat
	s.RecordEncounter(0, nil)
	assertClose(t, s.Predictability, PInit) // first meeting: 0.75

	s.RecordEncounter(1, nil)
	// 0.75 + (1-0.75)*0.75 = 0.9375
	assertClose(t, s.Predictability, 0.9375)

	s.RecordEncounter(2, nil)
	// 0.9375 + (1-0.9375)*0.75 = 0.984375
	assertClose(t, s.Predictability, 0.984375)

	if s.Encounters != 3 {
		t.Fatalf("encounters = %d, want 3", s.Encounters)
	}
	if s.LastSeen != 2 {
		t.Fatalf("last seen = %d, want 2", s.LastSeen)
	}
}

// TestAging pins the PROPHET decay recurrence.
func TestAging(t *testing.T) {
	s := EncounterStat{Predictability: 0.9375, LastSeen: 10}
	s.AgePredictability(2)
	want := 0.9375 * Gamma * Gamma
	assertClose(t, s.Predictability, want)

	// Unchanged when nothing to age.
	s.AgePredictability(0)
	assertClose(t, s.Predictability, want)

	// Floor: predictability decays to ~0, not negative.
	fresh := EncounterStat{Predictability: 0.1, LastSeen: 0}
	fresh.AgePredictability(1_000_000)
	if fresh.Predictability < 0 || fresh.Predictability > 1.0 {
		t.Fatalf("floor decay produced %v", fresh.Predictability)
	}
}

// TestTransitivity pins the PROPHET transitivity recurrence, including the
// (1 - P_AC) dampener that keeps the value asymptotic (never overshoots 1).
func TestTransitivity(t *testing.T) {
	// gain = (1 - P_AC_old) * P_AB * P_BC * Beta
	gain := TransitGain(0.5, 0.75, 0.75)
	assertClose(t, gain, (1-0.5)*0.75*0.75*Beta)

	// When A is already certain about C, a transit adds almost nothing.
	small := TransitGain(0.999, 0.75, 0.75)
	assertClose(t, small, (1-0.999)*0.75*0.75*Beta)
	if small > gain {
		t.Fatalf("transit gain should shrink as P_AC approaches 1")
	}
}

// TestRecordEncounterTransitivity verifies that a meeting applies the
// transitive boost using the peer's own stats and the paper's ordering:
// update P(a,b) first, then transit with the fresh P(a,b).
func TestRecordEncounterTransitivity(t *testing.T) {
	var s EncounterStat // node a's stat for peer b
	peerStats := map[string]EncounterStat{
		"c": {Peer: "c", Predictability: 0.8},
		"d": {Peer: "d", Predictability: 0.5},
	}
	s.RecordEncounter(5, peerStats)

	// Derive the exact expectation from the recurrence.
	pAB := PInit // after own first-encounter update
	p := PInit
	p += TransitGain(p, pAB, 0.8) // c
	p += TransitGain(p, pAB, 0.5) // d

	assertClose(t, s.Predictability, p)
	if s.Encounters != 1 {
		t.Fatalf("encounters = %d, want 1", s.Encounters)
	}
	if s.LastSeen != 5 {
		t.Fatalf("last seen = %d, want 5", s.LastSeen)
	}
}

// TestSprayAndWaitDecisionMatrix pins the full decision table.
func TestSprayAndWaitDecisionMatrix(t *testing.T) {
	f := SprayAndWait{L: 3}

	mk := func(copies int) *MessageState {
		return &MessageState{ID: "m", Src: "a", Dst: "dst", CopiesMade: copies}
	}

	// The destination is always delivered, even with no budget.
	if got := f.Decide(mk(99), "me", "dst", EncounterStat{}, EncounterStat{}, true); got != ActionDeliver {
		t.Fatalf("dst should be ActionDeliver, got %v", got)
	}

	// A peer already holding a copy is never handed another.
	if got := f.Decide(mk(0), "me", "p", EncounterStat{}, EncounterStat{}, true); got != ActionDup {
		t.Fatalf("peer with copy should be ActionDup, got %v", got)
	}

	// Spray phase: any fresh peer gets a copy while budget remains.
	if got := f.Decide(mk(0), "me", "p", EncounterStat{}, EncounterStat{}, false); got != ActionSpray {
		t.Fatalf("budget left should be ActionSpray, got %v", got)
	}
	if got := f.Decide(mk(2), "me", "p", EncounterStat{}, EncounterStat{}, false); got != ActionSpray {
		t.Fatalf("budget left (2<3) should be ActionSpray, got %v", got)
	}

	// Wait phase: budget spent, peer no better → keep.
	if got := f.Decide(mk(3), "me", "p",
		EncounterStat{Predictability: 0.7}, EncounterStat{Predictability: 0.7}, false); got != ActionKeep {
		t.Fatalf("equal predictability should be ActionKeep, got %v", got)
	}
	if got := f.Decide(mk(3), "me", "p",
		EncounterStat{Predictability: 0.8}, EncounterStat{Predictability: 0.2}, false); got != ActionKeep {
		t.Fatalf("peer worse should be ActionKeep, got %v", got)
	}

	// Wait phase: peer strictly better → handoff.
	if got := f.Decide(mk(3), "me", "p",
		EncounterStat{Predictability: 0.2}, EncounterStat{Predictability: 0.8}, false); got != ActionHandoff {
		t.Fatalf("peer better should be ActionHandoff, got %v", got)
	}
}

// TestEpidemicDecisionMatrix pins the baseline flood.
func TestEpidemicDecisionMatrix(t *testing.T) {
	var f Epidemic
	// Destination delivered; dedup respected; everything else sprayed —
	// regardless of copy count or predictability.
	msg := &MessageState{ID: "m", Src: "a", Dst: "dst"}
	if got := f.Decide(msg, "me", "dst", EncounterStat{}, EncounterStat{}, true); got != ActionDeliver {
		t.Fatalf("dst should be ActionDeliver, got %v", got)
	}
	if got := f.Decide(msg, "me", "p", EncounterStat{}, EncounterStat{}, true); got != ActionDup {
		t.Fatalf("peer with copy should be ActionDup, got %v", got)
	}
	if got := f.Decide(msg, "me", "p", EncounterStat{}, EncounterStat{}, false); got != ActionSpray {
		t.Fatalf("fresh peer should be ActionSpray, got %v", got)
	}
}

// TestActionClassifiers pins the copy bookkeeping semantics.
func TestActionClassifiers(t *testing.T) {
	cases := []struct {
		action  SprayAction
		creates bool
		moves   bool
		keeps   bool
	}{
		{ActionDeliver, false, true, false},
		{ActionSpray, true, false, true},
		{ActionHandoff, false, true, false},
		{ActionKeep, false, false, true},
		{ActionDup, false, false, true},
	}
	for _, tc := range cases {
		if got := CreatesCopy(tc.action); got != tc.creates {
			t.Errorf("%v: CreatesCopy = %v, want %v", tc.action, got, tc.creates)
		}
		if got := MovesCopy(tc.action); got != tc.moves {
			t.Errorf("%v: MovesCopy = %v, want %v", tc.action, got, tc.moves)
		}
		if got := KeepsCopy(tc.action); got != tc.keeps {
			t.Errorf("%v: KeepsCopy = %v, want %v", tc.action, got, tc.keeps)
		}
	}
}
