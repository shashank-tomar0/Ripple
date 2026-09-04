// Command routingbench compares routing protocols on the deterministic DTN
// simulator (pkg/routing/sim) across mobility scenarios and prints a
// reproducible comparison table.
//
// This is the research-honesty gate: the benchmark FAILS (non-zero exit) if
// spray-and-wait does not actually save copies in every scenario — a loss
// means the headline claim is false and the numbers must not be published.
// All seeds are pinned, so the table is identical on every machine: the
// "reproducibility" a paper needs.
//
// Usage: routingbench
package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/routing"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/routing/sim"
)

// scenario is one row of the benchmark. Fields override sim.DefaultConfig.
type scenario struct {
	name   string
	nodes  int
	area   float64
	range_ float64
	minV   float64
	maxV   float64
	msg    int
	ttl    int64
	dur    int64
	seed   int64
}

func main() {
	scenarios := []scenario{
		// Sparse: large field, short radio range, few nodes. Encounters are
		// rare — the regime where DTN protocols are supposed to matter.
		{"sparse", 18, 50, 4.0, 1, 3, 30, 35, 600, 42},
		// Medium: the balanced default regime.
		{"medium", 30, 25, 4.5, 1, 3, 40, 100, 500, 42},
		// Dense: crowded field, short radio. Contacts are frequent but still
		// intermittent — not a fully-connected clique (that would be a
		// degenerate 0-latency regime).
		{"dense", 35, 22, 5.0, 1, 3, 50, 100, 500, 42},
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "scenario\tprotocol\tdelivered\tdelivery%\tavg latency\tavg hops\tcopies/msg")
	fmt.Fprintln(tw, "--------\t--------\t---------\t---------\t-----------\t--------\t----------")

	failed := false
	for _, s := range scenarios {
		base := sim.DefaultConfig()
		base.Nodes = s.nodes
		base.AreaSize = s.area
		base.RadioRange = s.range_
		base.MinSpeed = s.minV
		base.MaxSpeed = s.maxV
		base.Messages = s.msg
		base.TTL = s.ttl
		base.Duration = s.dur
		base.Seed = s.seed

		base.Forwarder = routing.Epidemic{}
		epi, err := sim.Run(base)
		if err != nil {
			fmt.Fprintf(os.Stderr, "epidemic %s: %v\n", s.name, err)
			os.Exit(1)
		}
		base.Forwarder = routing.SprayAndWait{L: 8}
		spray, err := sim.Run(base)
		if err != nil {
			fmt.Fprintf(os.Stderr, "spray %s: %v\n", s.name, err)
			os.Exit(1)
		}

		fmt.Fprintf(tw, "%s\tepidemic\t%d/%d\t%.0f%%\t%v ticks\t%.1f\t%.1f\n",
			s.name, epi.Delivered, epi.Total, epi.DeliveredFraction()*100,
			epi.AvgLatency(), epi.AvgHops(), epi.AvgCopiesPerMessage())
		fmt.Fprintf(tw, "%s\tspray&wait\t%d/%d\t%.0f%%\t%v ticks\t%.1f\t%.1f\n",
			s.name, spray.Delivered, spray.Total, spray.DeliveredFraction()*100,
			spray.AvgLatency(), spray.AvgHops(), spray.AvgCopiesPerMessage())

		// The gate: spray must save copies AND not collapse delivery.
		savesCopies := spray.AvgCopiesPerMessage() < epi.AvgCopiesPerMessage()
		keepsDelivery := epi.Delivered == 0 ||
			float64(spray.Delivered)/float64(epi.Delivered) >= 0.6
		if !savesCopies {
			fmt.Fprintf(os.Stderr, "❌ %s: spray copies/msg %.1f ≥ epidemic %.1f — claim false\n",
				s.name, spray.AvgCopiesPerMessage(), epi.AvgCopiesPerMessage())
			failed = true
		}
		if !keepsDelivery {
			fmt.Fprintf(os.Stderr, "❌ %s: spray delivery collapsed to %d/%d vs epidemic %d/%d\n",
				s.name, spray.Delivered, spray.Total, epi.Delivered, epi.Total)
			failed = true
		}
	}
	tw.Flush()

	fmt.Println()
	if failed {
		fmt.Println("❌ BENCHMARK FAILED — claims not backed by the simulation. Fix before publishing.")
		os.Exit(1)
	}
	fmt.Println("✅ spray-and-wait saves copies in every scenario without collapsing delivery.")
	fmt.Println("   Fix everything above, do not cherry-pick: pinned seeds reproduce these numbers.")
}
