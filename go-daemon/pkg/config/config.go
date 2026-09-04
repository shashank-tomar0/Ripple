// Package config handles CLI flags and configuration for Ripple daemon.
package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultPort       = 0    // 0 = random port (OS assigns)
	DefaultWSPort     = 9876 // WebSocket bridge port
	DefaultRendezvous = "ripple-chat-v1"
	DefaultDataDir    = ".ripple"
)

// Config holds all configuration for a Ripple daemon instance.
type Config struct {
	// ListenPort is the port for the libp2p TCP transport. 0 = random.
	ListenPort int

	// WSPort is the port for the WebSocket bridge server.
	WSPort int

	// Rendezvous is the DHT rendezvous string for peer discovery.
	Rendezvous string

	// RelayPeers are multiaddrs of known peers to connect to on startup.
	RelayPeers []string

	// DataDir is the directory for storing identity and message data.
	DataDir string

	// IdentityPath is the path to the identity PEM file.
	IdentityPath string

	// Nickname is a human-readable display name.
	Nickname string

	// DBPath is the base path for persistent message storage (JSON backup file).
	// If empty, the store runs in pure in-memory mode (no persistence across restarts).
	DBPath string

	// Debug enables verbose logging.
	Debug bool

	// NoMDNS disables mDNS peer discovery. Useful for deterministic
	// topologies (tests, relay chains) where peers must only connect
	// through explicitly configured bootstrap addresses.
	NoMDNS bool

	// ExportSeed prints the identity's BIP39 backup phrase to stdout and
	// exits without starting the daemon.
	ExportSeed bool

	// ImportSeed restores the identity file from a BIP39 backup phrase and
	// exits without starting the daemon. The restored keypair is identical
	// to the one on the device that produced the phrase.
	ImportSeed string

	// RoutingMode selects the message-forwarding policy: "epidemic" (the
	// default flood) or "spray" (destination-aware spray-and-wait with a
	// bounded copy budget). Broadcasts always flood.
	RoutingMode string

	// SprayBudget is the copy budget L for spray routing: at most L
	// physical copies of an addressed message. Ignored in epidemic mode.
	SprayBudget int
}

// Parse parses CLI flags and returns a Config.
func Parse() *Config {
	c := &Config{}

	flag.IntVar(&c.ListenPort, "port", DefaultPort, "TCP listen port (0 = random)")
	flag.IntVar(&c.WSPort, "wsport", DefaultWSPort, "WebSocket bridge port")
	flag.StringVar(&c.Rendezvous, "rdv", DefaultRendezvous, "Rendezvous string for peer discovery")
	flag.StringVar(&c.Nickname, "nick", "", "Display nickname")
	flag.StringVar(&c.DBPath, "db", "", "Persistent store path (JSON backup; default: ~/.ripple/ripple.db)")
	flag.BoolVar(&c.Debug, "debug", false, "Enable debug logging")
	flag.BoolVar(&c.NoMDNS, "nomdns", false, "Disable mDNS discovery (explicit bootstrap only)")
	flag.BoolVar(&c.ExportSeed, "export-seed", false, "Print the identity's 24-word BIP39 backup phrase and exit")
	flag.StringVar(&c.ImportSeed, "import-seed", "", "Restore identity from a BIP39 backup phrase and exit")
	flag.StringVar(&c.RoutingMode, "routing", "epidemic", "Forwarding policy: epidemic (flood) or spray (bounded-copy routing)")
	flag.IntVar(&c.SprayBudget, "spray-budget", 4, "Copy budget L for spray routing (addressed messages only)")

	var dataDir string
	flag.StringVar(&dataDir, "data", "", "Data directory (default: ~/.ripple)")

	relayPeers := flag.String("peers", "", "Comma-separated peer multiaddrs to connect to")

	flag.Parse()

	// Resolve data directory
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot find home dir: %v\n", err)
			dataDir = DefaultDataDir
		} else {
			dataDir = filepath.Join(home, DefaultDataDir)
		}
	}
	c.DataDir = dataDir
	c.IdentityPath = filepath.Join(dataDir, "identity.pem")

	// Default persistence path inside the data directory
	if c.DBPath == "" {
		c.DBPath = filepath.Join(dataDir, "ripple.db")
	}

	if *relayPeers != "" {
		c.RelayPeers = splitCSV(*relayPeers)
	}

	// Default nickname from hostname
	if c.Nickname == "" {
		host, _ := os.Hostname()
		c.Nickname = host
	}

	return c
}

// splitCSV splits a comma-separated string, trimming whitespace.
func splitCSV(s string) []string {
	var result []string
	var current []byte
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			if len(current) > 0 {
				result = append(result, string(current))
				current = nil
			}
		} else if s[i] != ' ' {
			current = append(current, s[i])
		}
	}
	if len(current) > 0 {
		result = append(result, string(current))
	}
	return result
}
