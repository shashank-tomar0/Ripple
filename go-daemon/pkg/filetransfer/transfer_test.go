package filetransfer

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/mesh"
)

// newHost builds a real libp2p mesh node to act as the file transfer host.
func newHost(t *testing.T) *mesh.Node {
	t.Helper()
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	n, err := mesh.NewNode(context.Background(), priv, 0, mesh.WithMDNS(false))
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	t.Cleanup(n.Close)
	return n
}

// connect makes dst dial src and waits until both sides know each other.
func connect(t *testing.T, src, dst *mesh.Node) {
	t.Helper()
	if err := dst.ConnectToPeer(src.MultiaddrString()); err != nil {
		t.Fatalf("dial %s → %s: %v", dst.PeerID.ShortString(), src.PeerID.ShortString(), err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if hasPeer(src.KnownPeers(), dst.PeerID) && hasPeer(dst.KnownPeers(), src.PeerID) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("peers never learned each other")
}

func hasPeer(peers []peer.ID, want peer.ID) bool {
	for _, p := range peers {
		if p == want {
			return true
		}
	}
	return false
}

// TestFileTransferRoundTrip sends a 150KB file (3 chunks of 64KB) between
// two real nodes and asserts the receiver reassembles it byte-identically
// with the correct completion status and progress reaching 1.0.
func TestFileTransferRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// 150KB source — spans 3 chunks of the 64KB default.
	data := make([]byte, 150*1024)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("generate payload: %v", err)
	}
	srcPath := filepath.Join(dir, "source.bin")
	if err := os.WriteFile(srcPath, data, 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	a, b := newHost(t), newHost(t)
	connect(t, a, b)

	ma := NewManager(a.Host, filepath.Join(dir, "out-a"))
	mb := NewManager(b.Host, filepath.Join(dir, "out-b"))
	ma.Start(ctx)
	mb.Start(ctx)

	type completion struct {
		path   string
		status TransferStatus
		prog   float64
	}
	done := make(chan completion, 4)
	mb.OnTransferComplete = func(tr *Transfer) {
		tr.mu.RLock()
		defer tr.mu.RUnlock()
		done <- completion{path: tr.OutputPath, status: tr.Status, prog: tr.Progress}
	}

	tr, err := ma.SendFile(srcPath, b.Host.ID().String(), "alice", "")
	if err != nil {
		t.Fatalf("SendFile: %v", err)
	}
	if tr.Metadata.ChunkCount != 3 {
		t.Fatalf("chunk count = %d, want 3 (150KB / 64KB)", tr.Metadata.ChunkCount)
	}

	select {
	case c := <-done:
		if c.status != StatusComplete {
			t.Fatalf("receiver status = %s, want complete", c.status)
		}
		if c.prog != 1.0 {
			t.Fatalf("receiver progress = %f, want 1.0", c.prog)
		}
		got, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("read reassembled file: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("reassembled file differs from source (%d vs %d bytes)", len(got), len(data))
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for transfer completion")
	}

	// Sender side must also report complete at progress 1.0.
	if tr.Metadata.Size != int64(len(data)) {
		t.Fatalf("sender metadata size = %d, want %d", tr.Metadata.Size, len(data))
	}
	tr.mu.RLock()
	sendStatus := tr.Status
	tr.mu.RUnlock()
	if sendStatus != StatusComplete {
		t.Fatalf("sender status = %s, want complete", sendStatus)
	}
}

// TestFileTransferRejectsPathTraversal verifies that a remote peer cannot
// use a crafted filename to write outside the incoming directory.
func TestFileTransferRejectsPathTraversal(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	a, b := newHost(t), newHost(t)
	connect(t, a, b)

	incomingDir := filepath.Join(dir, "incoming")
	mb := NewManager(b.Host, filepath.Join(dir, "recv"))
	mb.Start(ctx)

	// Open a real stream to the receiver and send a meta packet with a
	// malicious filename. If the manager trusted it, the file would land
	// outside `incomingDir` (or fail the join entirely on a bad separator).
	streamCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	s, err := a.Host.NewStream(streamCtx, b.Host.ID(), ProtocolFileTransfer)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	evil := &streamPacket{
		Type: "meta",
		Metadata: &FileMetadata{
			FileID:     "deadbeef",
			Name:       "../../evil.txt",
			Size:       4,
			ChunkCount: 1,
			Sender:     a.Host.ID().String(),
			Recipient:  b.Host.ID().String(),
		},
	}
	if err := writePacket(s, evil); err != nil {
		t.Fatalf("write evil meta: %v", err)
	}
	s.Close()

	// Give the receiver a moment to process (or reject) the stream.
	time.Sleep(500 * time.Millisecond)

	// Nothing may be written outside the incoming directory...
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
		t.Fatal("path traversal succeeded: evil.txt written at parent dir")
	}
	// ...and the transfer must not be tracked.
	if tr := mb.GetTransfer("deadbeef"); tr != nil {
		t.Fatalf("transfer with traversal filename was accepted: %+v", tr.Metadata)
	}
	// The incoming dir itself must not contain the traversal-named file.
	entries, _ := os.ReadDir(incomingDir)
	for _, e := range entries {
		if e.Name() != "deadbeef_evil.txt" && e.Name() != "deadbeef_.." {
			continue
		}
		t.Fatalf("unexpected file created in incoming dir: %s", e.Name())
	}
}