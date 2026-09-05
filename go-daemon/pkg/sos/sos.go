// Package sos implements the SOS emergency broadcast manager for the Ripple mesh.
package sos

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/mesh"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
)

const (
	MaxTTL            = 64               // SOS messages travel up to 64 hops
	ReBroadcastEvery  = 30 * time.Second // Re-broadcast every 30s while active
	ActiveDuration    = 60 * time.Minute // SOS stays active for 60 minutes (matches AutoExpire on the wire)
)

// ActiveAlert tracks an ongoing SOS alert.
type ActiveAlert struct {
	Message     *message.Message
	Payload     *message.SOSPayload
	StartedAt   time.Time
	ExpiresAt   time.Time
	AckCount    int           // number of delivery acks received
	ReceivedAt  time.Time     // when we received this alert (for relays)
	SenderPeer  peer.ID       // peer ID of original sender
}

// Manager handles SOS alerts in the mesh.
type Manager struct {
	node        *mesh.Node
	alerts      map[string]*ActiveAlert // message ID -> alert
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup

	// Callbacks for bridge → Flutter
	OnSOSReceived func(alert *ActiveAlert)
	OnSOSExpired  func(alertID string)
	OnSOSAck      func(alertID string, ackerPeerID string)
}

// NewManager creates a new SOS manager.
func NewManager(node *mesh.Node) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		node:   node,
		alerts: make(map[string]*ActiveAlert),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start begins the SOS manager's background loops.
func (m *Manager) Start(ctx context.Context) {
	// Update context to parent for cancellation
	m.ctx = ctx

	// Start re-broadcast loop
	m.wg.Add(1)
	go m.reBroadcastLoop()

	// Start expiry loop
	m.wg.Add(1)
	go m.expiryLoop()
}

// Stop stops the SOS manager.
func (m *Manager) Stop() {
	m.cancel()
	m.wg.Wait()
}

// SendAlert broadcasts an SOS alert to the entire mesh with high TTL.
// Returns the message ID for tracking.
func (m *Manager) SendAlert(sender, senderNick, text string,
	urgency message.SOSUrgency, lat, lon, accuracy float64) (string, error) {

	if urgency == "" {
		urgency = message.SOSUrgencyHigh
	}

	msg := message.NewSOS(sender, senderNick, text, urgency, lat, lon, accuracy)

	// The payload is parsed from the message itself — the single source of
	// truth (message.NewSOS), so the tracked alert can never drift from
	// what went on the wire.
	payload, err := message.ParseSOSPayload(msg)
	if err != nil {
		return "", fmt.Errorf("parse own SOS payload: %w", err)
	}

	// Track locally as active alert
	alert := &ActiveAlert{
		Message:    msg,
		Payload:    payload,
		StartedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(ActiveDuration),
		ReceivedAt: time.Now(),
		SenderPeer: m.node.Host.ID(),
	}

	m.mu.Lock()
	m.alerts[msg.ID] = alert
	m.mu.Unlock()

	// Broadcast to mesh
	if err := m.node.SendMessage(msg); err != nil {
		m.mu.Lock()
		delete(m.alerts, msg.ID)
		m.mu.Unlock()
		return "", err
	}

	// Notify callback
	if m.OnSOSReceived != nil {
		m.OnSOSReceived(alert)
	}

	return msg.ID, nil
}

// HandleIncomingSOS processes an SOS message received from the mesh.
func (m *Manager) HandleIncomingSOS(msg *message.Message) error {
	// Parse SOS payload
	payload, err := message.ParseSOSPayload(msg)
	if err != nil {
		return err
	}

	senderPID, err := peer.Decode(msg.Sender)
	if err != nil {
		return err
	}

	// Check if we've already seen this alert
	m.mu.RLock()
	existing, exists := m.alerts[msg.ID]
	m.mu.RUnlock()

	if exists {
		// Already tracking this alert, just update receipt time
		existing.ReceivedAt = time.Now()
		return nil
	}

	// Create new active alert
	alert := &ActiveAlert{
		Message:     msg,
		Payload:     payload,
		StartedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(ActiveDuration),
		ReceivedAt:  time.Now(),
		SenderPeer:  senderPID,
	}

	m.mu.Lock()
	m.alerts[msg.ID] = alert
	m.mu.Unlock()

	// Auto-generate delivery ack back to sender if requested
	if payload.AckRequired {
		ackMsg := message.NewDeliveryAck(
			m.node.Host.ID().String(),
			msg.ID,
			msg.Sender,
			message.DeliveryReceived,
			msg.HopCount,
			"",
		)
		// Send ack directly to sender
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s, err := m.node.Host.NewStream(ctx, senderPID, mesh.ProtocolID)
			if err == nil {
				defer s.Close()
				data, _ := ackMsg.Serialize()
				s.Write(data)
			}
		}()
	}

	// Notify callback for Flutter
	if m.OnSOSReceived != nil {
		m.OnSOSReceived(alert)
	}

	return nil
}

// CancelAlert cancels an active SOS alert.
func (m *Manager) CancelAlert(alertID string) error {
	m.mu.Lock()
	_, exists := m.alerts[alertID]
	if exists {
		delete(m.alerts, alertID)
	}
	m.mu.Unlock()

	if !exists {
		return nil // Already gone
	}

	// Notify callback
	if m.OnSOSExpired != nil {
		m.OnSOSExpired(alertID)
	}
	return nil
}

// ActiveAlerts returns all currently active SOS alerts.
func (m *Manager) ActiveAlerts() []*ActiveAlert {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*ActiveAlert, 0, len(m.alerts))
	for _, a := range m.alerts {
		result = append(result, a)
	}
	return result
}

// GetAlert returns a specific alert by ID.
func (m *Manager) GetAlert(alertID string) *ActiveAlert {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.alerts[alertID]
}

// HandleDeliveryAck processes a delivery acknowledgment for an SOS.
func (m *Manager) HandleDeliveryAck(ack *message.Message) {
	var info message.DeliveryInfo
	if err := json.Unmarshal([]byte(ack.Payload), &info); err != nil {
		return
	}

	m.mu.Lock()
	alert, exists := m.alerts[info.MessageID]
	if exists {
		alert.AckCount++
	}
	m.mu.Unlock()

	if exists && m.OnSOSAck != nil {
		m.OnSOSAck(info.MessageID, ack.Sender)
	}
}

// reBroadcastLoop periodically re-broadcasts active SOS alerts.
func (m *Manager) reBroadcastLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(ReBroadcastEvery)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.reBroadcastActive()
		}
	}
}

// reBroadcastActive sends out all active SOS alerts again.
func (m *Manager) reBroadcastActive() {
	m.mu.RLock()
	alerts := make([]*ActiveAlert, 0, len(m.alerts))
	for _, a := range m.alerts {
		alerts = append(alerts, a)
	}
	m.mu.RUnlock()

	now := time.Now()
	for _, alert := range alerts {
		if now.After(alert.ExpiresAt) {
			continue // Will be cleaned up by expiry loop
		}

		// Create a rebroadcast message with decremented TTL
		rebroadcastMsg := &message.Message{
			ID:          alert.Message.ID, // Same ID for dedup
			Type:        message.TypeSOS,
			Sender:      alert.Message.Sender,
			SenderNick:  alert.Message.SenderNick,
			Recipient:   "",
			Payload:     alert.Message.Payload,
			Timestamp:   time.Now().UnixNano(),
			TTL:         alert.Message.TTL - 1, // Decrement TTL on rebroadcast
			HopCount:    alert.Message.HopCount + 1,
		}

		// If TTL exhausted, don't rebroadcast
		if rebroadcastMsg.TTL <= 0 {
			continue
		}

		// Broadcast to mesh
		if err := m.node.Publish(rebroadcastMsg); err != nil {
			// Log but continue
			continue
		}
	}
}

// expiryLoop periodically checks for expired alerts.
func (m *Manager) expiryLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.checkExpiry()
		}
	}
}

// checkExpiry removes expired alerts and notifies callback.
func (m *Manager) checkExpiry() {
	now := time.Now()
	var expiredIDs []string

	m.mu.Lock()
	for id, alert := range m.alerts {
		if now.After(alert.ExpiresAt) {
			expiredIDs = append(expiredIDs, id)
			delete(m.alerts, id)
		}
	}
	m.mu.Unlock()

	for _, id := range expiredIDs {
		if m.OnSOSExpired != nil {
			m.OnSOSExpired(id)
		}
	}
}