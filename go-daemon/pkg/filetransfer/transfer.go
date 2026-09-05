// Package filetransfer provides chunked file transfer over libp2p direct streams.
//
// Files are split into 64KB chunks and sent over a dedicated direct stream
// (not GossipSub, which is reserved for small messages). Metadata and progress
// are relayed to the WS bridge for Flutter UI updates.
package filetransfer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
)

const (
	// ProtocolFileTransfer is the libp2p protocol ID for file transfers.
	ProtocolFileTransfer = protocol.ID("/ripple/filetransfer/1.0.0")

	// DefaultChunkSize is the size of each file chunk (64KB).
	DefaultChunkSize = 64 * 1024

	// TransferTimeout is the maximum time allowed for a complete file transfer.
	TransferTimeout = 5 * time.Minute

	// readTimeout is the timeout for reading a single packet from the stream.
	readTimeout = 30 * time.Second
)

// FileMetadata describes a file being transferred.
type FileMetadata struct {
	FileID     string `json:"file_id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	MimeType   string `json:"mime_type"`
	ChunkCount int    `json:"chunk_count"`
	Sender     string `json:"sender"`
	SenderNick string `json:"sender_nick"`
	Recipient  string `json:"recipient"` // empty = broadcast
	Timestamp  int64  `json:"ts"`
}

// FileChunk is a single chunk of a file transfer.
type FileChunk struct {
	FileID     string `json:"file_id"`
	ChunkIdx   int    `json:"chunk_idx"`
	ChunkCount int    `json:"chunk_count"`
	Data       []byte `json:"data"` // base64 encoded when serialized
	Size       int    `json:"size"`
}

// TransferStatus is the current state of a file transfer.
type TransferStatus string

const (
	StatusPending   TransferStatus = "pending"
	StatusSending   TransferStatus = "sending"
	StatusReceiving TransferStatus = "receiving"
	StatusComplete  TransferStatus = "complete"
	StatusFailed    TransferStatus = "failed"
	StatusCancelled TransferStatus = "cancelled"
)

// Transfer tracks the state of a single file transfer.
type Transfer struct {
	Metadata   FileMetadata
	Status     TransferStatus
	Progress   float64 // 0.0 to 1.0
	Received   int64
	Chunks     map[int]*FileChunk
	OutputPath string
	mu         sync.RWMutex
	startedAt  time.Time
}

// streamPacket wraps metadata or chunk for transmission over the direct stream.
type streamPacket struct {
	Type     string        `json:"type"`               // "meta" or "chunk"
	Metadata *FileMetadata `json:"metadata,omitempty"`
	Chunk    *FileChunk    `json:"chunk,omitempty"`
}

// Manager manages active file transfers.
type Manager struct {
	host        host.Host
	transfers   map[string]*Transfer
	mu          sync.RWMutex
	incomingDir string
	outgoingDir string

	// Callbacks
	OnTransferStarted  func(transfer *Transfer)
	OnTransferProgress func(transfer *Transfer, progress float64)
	OnTransferComplete func(transfer *Transfer)
	OnTransferFailed   func(transfer *Transfer, err error)

	// To notify Flutter via bridge
	OnFileNotification func(msg *message.Message)
}

// NewManager creates a new file transfer manager.
// dataDir is the Ripple data directory (e.g., ~/.ripple); subdirectories
// files/incoming and files/outgoing are created automatically.
func NewManager(h host.Host, dataDir string) *Manager {
	incomingDir := filepath.Join(dataDir, "files", "incoming")
	outgoingDir := filepath.Join(dataDir, "files", "outgoing")

	// Create directories if they don't exist
	os.MkdirAll(incomingDir, 0755)
	os.MkdirAll(outgoingDir, 0755)

	return &Manager{
		host:         h,
		transfers:    make(map[string]*Transfer),
		incomingDir:  incomingDir,
		outgoingDir:  outgoingDir,
	}
}

// Start registers the stream handler and returns.
func (m *Manager) Start(ctx context.Context) {
	m.host.SetStreamHandler(ProtocolFileTransfer, m.handleStream)
}

// SendFile initiates sending a file to a peer.
// It sends the metadata notification to the bridge first, then chunks the file
// and sends each chunk over a direct libp2p stream in a background goroutine.
// A non-empty fileID is used as the canonical transfer ID (the app's message
// ID, so notifications correlate with the chat row); empty generates one.
func (m *Manager) SendFile(filePath, recipient, senderNick, fileID string) (*Transfer, error) {
	// Open the file. Ownership passes to sendFileStream (which closes it)
	// — closing here would race the background goroutine reading it.
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}

	// Stat the file
	fi, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	// Canonical transfer ID: caller-supplied (app message ID) or fresh.
	if fileID == "" {
		fileID = newFileID()
	}

	// Calculate chunk count
	fileSize := fi.Size()
	chunkCount := int((fileSize + int64(DefaultChunkSize) - 1) / int64(DefaultChunkSize))

	// Determine mime type (simple extension-based)
	mimeType := detectMimeType(fi.Name())

	// Create metadata
	meta := FileMetadata{
		FileID:     fileID,
		Name:       fi.Name(),
		Size:       fileSize,
		MimeType:   mimeType,
		ChunkCount: chunkCount,
		Sender:     m.host.ID().String(),
		SenderNick: senderNick,
		Recipient:  recipient,
		Timestamp:  time.Now().UnixNano(),
	}

	// Create transfer tracking object
	transfer := &Transfer{
		Metadata:  meta,
		Status:    StatusPending,
		Progress:  0.0,
		Received:  0,
		Chunks:    make(map[int]*FileChunk),
		startedAt: time.Now(),
	}

	m.mu.Lock()
	m.transfers[fileID] = transfer
	m.mu.Unlock()

	// Notify via callback to bridge -> Flutter. The notification carries the
	// transfer's fileID (== the app's message ID), so the app can merge the
	// started/progress/complete frames into the chat row it already shows.
	if m.OnFileNotification != nil {
		m.OnFileNotification(message.NewFileMetadata(
			meta.Sender, meta.SenderNick, recipient, meta.FileID,
			meta.Name, meta.MimeType, fileSize, chunkCount,
		))
	}

	// Start sending in background goroutine
	go m.sendFileStream(file, meta, transfer)

	return transfer, nil
}

// sendFileStream performs the actual chunked transfer over a direct stream.
// Runs in a background goroutine.
func (m *Manager) sendFileStream(file *os.File, meta FileMetadata, transfer *Transfer) {
	defer file.Close()

	transfer.mu.Lock()
	transfer.Status = StatusSending
	transfer.mu.Unlock()

	if m.OnTransferStarted != nil {
		m.OnTransferStarted(transfer)
	}

	// Parse recipient peer ID
	recipientPID, err := peer.Decode(meta.Recipient)
	if err != nil {
		m.failTransfer(transfer, fmt.Errorf("decode recipient: %w", err))
		return
	}

	// Open direct stream to recipient
	ctx, cancel := context.WithTimeout(context.Background(), TransferTimeout)
	defer cancel()

	stream, err := m.host.NewStream(ctx, recipientPID, ProtocolFileTransfer)
	if err != nil {
		m.failTransfer(transfer, fmt.Errorf("open stream: %w", err))
		return
	}
	defer stream.Close()

	// Send metadata packet (first message on stream)
	metaPacket := streamPacket{
		Type:     "meta",
		Metadata: &meta,
	}
	if err := writePacket(stream, &metaPacket); err != nil {
		m.failTransfer(transfer, fmt.Errorf("send metadata: %w", err))
		return
	}

	// Read file and send chunks
	buf := make([]byte, DefaultChunkSize)
	var totalSent int64

	for i := 0; i < meta.ChunkCount; i++ {
		n, err := file.Read(buf)
		if err != nil && err != io.EOF {
			m.failTransfer(transfer, fmt.Errorf("read chunk %d: %w", i, err))
			return
		}
		if n == 0 {
			break
		}

		// Copy chunk data (buf may be reused)
		chunkData := make([]byte, n)
		copy(chunkData, buf[:n])

		chunk := &FileChunk{
			FileID:     meta.FileID,
			ChunkIdx:   i,
			ChunkCount: meta.ChunkCount,
			Data:       chunkData,
			Size:       n,
		}

		chunkPacket := streamPacket{
			Type:  "chunk",
			Chunk: chunk,
		}
		if err := writePacket(stream, &chunkPacket); err != nil {
			m.failTransfer(transfer, fmt.Errorf("send chunk %d: %w", i, err))
			return
		}

		totalSent += int64(n)
		progress := float64(totalSent) / float64(meta.Size)
		if progress > 1.0 {
			progress = 1.0
		}

		transfer.mu.Lock()
		transfer.Progress = progress
		transfer.Received = totalSent
		transfer.mu.Unlock()

		if m.OnTransferProgress != nil {
			m.OnTransferProgress(transfer, progress)
		}

		// Send progress notification to bridge
		if m.OnFileNotification != nil {
			m.OnFileNotification(message.NewFileProgress(
				meta.Sender, meta.Recipient, meta.FileID,
				i, meta.ChunkCount, progress,
			))
		}
	}

	// Mark complete
	transfer.mu.Lock()
	transfer.Status = StatusComplete
	transfer.Progress = 1.0
	transfer.mu.Unlock()

	if m.OnTransferComplete != nil {
		m.OnTransferComplete(transfer)
	}

	// Send completion notification
	if m.OnFileNotification != nil {
		m.OnFileNotification(message.NewFileComplete(
			meta.Sender, meta.Recipient, meta.FileID,
			meta.Name, "",
		))
	}
}

// handleStream processes incoming file transfers from a direct stream.
func (m *Manager) handleStream(s network.Stream) {
	defer s.Close()

	// Set read deadline
	s.SetDeadline(time.Now().Add(TransferTimeout))

	reader := bufio.NewReader(s)

	// --- Read metadata packet (always the first message) ---
	metaPacket, err := readPacket(reader)
	if err != nil {
		return
	}
	if metaPacket.Type != "meta" || metaPacket.Metadata == nil {
		return
	}
	meta := metaPacket.Metadata

	// Verify we are the intended recipient, or this is a broadcast
	localPID := m.host.ID().String()
	if meta.Recipient != "" && meta.Recipient != localPID {
		return // Not for us
	}

	// The filename comes from a remote peer — never trust it in a path.
	// Reject anything containing a path separator (which would let a peer
	// write outside the incoming directory), dot-dot, or an empty name.
	if meta.Name == "" || meta.Name == "." || meta.Name == ".." ||
		strings.ContainsAny(meta.Name, `\/`) {
		return // not for us: untrusted filename
	}

	// Create transfer tracking object
	outputPath := filepath.Join(m.incomingDir, meta.FileID+"_"+meta.Name)
	transfer := &Transfer{
		Metadata:   *meta,
		Status:     StatusReceiving,
		Progress:   0.0,
		Received:   0,
		Chunks:     make(map[int]*FileChunk, meta.ChunkCount),
		OutputPath: outputPath,
		startedAt:  time.Now(),
	}

	m.mu.Lock()
	m.transfers[meta.FileID] = transfer
	m.mu.Unlock()

	// Notify about incoming transfer metadata — same canonical fileID as the
	// sender's, so the receiving app correlates every frame with one entry.
	if m.OnFileNotification != nil {
		m.OnFileNotification(message.NewFileMetadata(
			meta.Sender, meta.SenderNick, localPID, meta.FileID,
			meta.Name, meta.MimeType, meta.Size, meta.ChunkCount,
		))
	}

	if m.OnTransferStarted != nil {
		m.OnTransferStarted(transfer)
	}

	// --- Read chunks ---
	for i := 0; i < meta.ChunkCount; i++ {
		chunkPacket, err := readPacket(reader)
		if err != nil {
			m.failTransfer(transfer, fmt.Errorf("read chunk %d: %w", i, err))
			return
		}
		if chunkPacket.Type != "chunk" || chunkPacket.Chunk == nil {
			m.failTransfer(transfer, fmt.Errorf("unexpected packet type at chunk %d: %s", i, chunkPacket.Type))
			return
		}

		chunk := chunkPacket.Chunk

		// Reject out-of-range or self-inconsistent chunks from the wire.
		if chunk.ChunkIdx < 0 || chunk.ChunkIdx >= meta.ChunkCount {
			m.failTransfer(transfer, fmt.Errorf("chunk %d out of range (count %d)", chunk.ChunkIdx, meta.ChunkCount))
			return
		}
		if chunk.Size != len(chunk.Data) {
			m.failTransfer(transfer, fmt.Errorf("chunk %d size mismatch: declared %d, actual %d", chunk.ChunkIdx, chunk.Size, len(chunk.Data)))
			return
		}

		transfer.mu.Lock()
		transfer.Chunks[chunk.ChunkIdx] = chunk
		transfer.Received += int64(chunk.Size)
		progress := float64(transfer.Received) / float64(meta.Size)
		if progress > 1.0 {
			progress = 1.0
		}
		transfer.Progress = progress
		transfer.mu.Unlock()

		if m.OnTransferProgress != nil {
			m.OnTransferProgress(transfer, progress)
		}

		// Send progress notification to bridge
		if m.OnFileNotification != nil {
			m.OnFileNotification(message.NewFileProgress(
				meta.Sender, localPID, meta.FileID,
				chunk.ChunkIdx, meta.ChunkCount, progress,
			))
		}
	}

	// --- Reassemble file from chunks ---
	if err := m.reassembleFile(transfer); err != nil {
		m.failTransfer(transfer, fmt.Errorf("reassemble file: %w", err))
		return
	}

	transfer.mu.Lock()
	transfer.Status = StatusComplete
	transfer.Progress = 1.0
	transfer.mu.Unlock()

	if m.OnTransferComplete != nil {
		m.OnTransferComplete(transfer)
	}

	// Send completion notification
	if m.OnFileNotification != nil {
		m.OnFileNotification(message.NewFileComplete(
			meta.Sender, localPID, meta.FileID,
			meta.Name, transfer.OutputPath,
		))
	}
}

// reassembleFile writes all chunks to the output file in order.
func (m *Manager) reassembleFile(transfer *Transfer) error {
	outFile, err := os.Create(transfer.OutputPath)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	for i := 0; i < transfer.Metadata.ChunkCount; i++ {
		chunk, ok := transfer.Chunks[i]
		if !ok {
			return fmt.Errorf("missing chunk %d", i)
		}
		if _, err := outFile.Write(chunk.Data); err != nil {
			return fmt.Errorf("write chunk %d: %w", i, err)
		}
	}

	return nil
}

// failTransfer marks a transfer as failed and calls the OnTransferFailed callback.
func (m *Manager) failTransfer(transfer *Transfer, err error) {
	transfer.mu.Lock()
	transfer.Status = StatusFailed
	transfer.mu.Unlock()

	if m.OnTransferFailed != nil {
		m.OnTransferFailed(transfer, err)
	}
}

// GetTransfer returns the transfer state for a file.
func (m *Manager) GetTransfer(fileID string) *Transfer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.transfers[fileID]
}

// ListTransfers returns all active transfers.
func (m *Manager) ListTransfers() []*Transfer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Transfer, 0, len(m.transfers))
	for _, t := range m.transfers {
		result = append(result, t)
	}
	return result
}

// CancelTransfer cancels an in-progress transfer.
func (m *Manager) CancelTransfer(fileID string) error {
	m.mu.Lock()
	transfer, ok := m.transfers[fileID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("transfer %s not found", fileID)
	}

	transfer.mu.Lock()
	if transfer.Status == StatusComplete {
		transfer.mu.Unlock()
		m.mu.Unlock()
		return fmt.Errorf("transfer %s already complete", fileID)
	}
	transfer.Status = StatusCancelled
	transfer.mu.Unlock()
	m.mu.Unlock()

	return nil
}

// Progress returns the current progress of a transfer (0.0 to 1.0).
func (m *Manager) Progress(fileID string) float64 {
	m.mu.RLock()
	transfer, ok := m.transfers[fileID]
	m.mu.RUnlock()
	if !ok {
		return 0.0
	}

	transfer.mu.RLock()
	defer transfer.mu.RUnlock()
	return transfer.Progress
}

// --- Stream I/O ---

// writePacket JSON-encodes a streamPacket and writes it as a newline-terminated line.
func writePacket(stream network.Stream, pkt *streamPacket) error {
	data, err := json.Marshal(pkt)
	if err != nil {
		return fmt.Errorf("marshal packet: %w", err)
	}
	data = append(data, '\n')
	if _, err := stream.Write(data); err != nil {
		return fmt.Errorf("write packet: %w", err)
	}
	return nil
}

// readPacket reads a newline-terminated line and JSON-decodes it as a streamPacket.
func readPacket(reader *bufio.Reader) (*streamPacket, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read packet: %w", err)
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, fmt.Errorf("empty packet")
	}

	var pkt streamPacket
	if err := json.Unmarshal(line, &pkt); err != nil {
		return nil, fmt.Errorf("unmarshal packet: %w", err)
	}
	return &pkt, nil
}

// --- Helpers ---

// newFileID generates a random hex string for file identification.
func newFileID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// detectMimeType returns a MIME type based on the file extension.
func detectMimeType(name string) string {
	ext := filepath.Ext(name)
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".avi":
		return "video/x-msvideo"
	case ".mkv":
		return "video/x-matroska"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".tar":
		return "application/x-tar"
	case ".gz":
		return "application/gzip"
	case ".txt":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".doc", ".docx":
		return "application/msword"
	case ".xls", ".xlsx":
		return "application/vnd.ms-excel"
	case ".csv":
		return "text/csv"
	default:
		return "application/octet-stream"
	}
}
