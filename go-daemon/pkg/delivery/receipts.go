// Package delivery manages message delivery receipts and retry logic.
package delivery

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/mesh"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
)

const (
	// DefaultRetryInterval is how often we check and retry pending messages.
	DefaultRetryInterval = 15 * time.Second

	// MaxRetries is the maximum number of retries before marking as failed.
	MaxRetries = 3

	// AckTimeout is how long we wait for a delivery acknowledgment before giving up.
	AckTimeout = 60 * time.Second

	// AckCheckInterval is how often we check pending messages for timeout/retry.
	AckCheckInterval = 15 * time.Second
)

// PendingReceipt tracks a message awaiting delivery confirmation.
type PendingReceipt struct {
	MessageID   string
	Recipient   string
	SentAt      time.Time
	RetryCount  int
	LastAttempt time.Time
	Status      message.DeliveryStatus
	mu          sync.RWMutex
}

// ReceiptManager handles delivery receipts and retry logic.
type ReceiptManager struct {
	node        *mesh.Node
	pending     map[string]*PendingReceipt // messageID -> receipt
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc

	// Callback for bridge -> Flutter
	OnReceiptReceived func(receipt *PendingReceipt)
}

// NewReceiptManager creates a new delivery receipt manager.
func NewReceiptManager(node *mesh.Node) *ReceiptManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &ReceiptManager{
		node:    node,
		pending: make(map[string]*PendingReceipt),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start begins background retry and timeout loops.
func (rm *ReceiptManager) Start(ctx context.Context) {
	rm.ctx = ctx
	rm.ctx, rm.cancel = context.WithCancel(ctx)

	// Start the background retry/timeout checker
	go rm.retryLoop()
}

// TrackMessage registers a message for delivery confirmation tracking.
func (rm *ReceiptManager) TrackMessage(msgID, recipient string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if _, exists := rm.pending[msgID]; !exists {
		rm.pending[msgID] = &PendingReceipt{
			MessageID:  msgID,
			Recipient:  recipient,
			SentAt:     time.Now(),
			RetryCount: 0,
			Status:     message.DeliverySent,
		}
	}
}

// HandleDeliveryAck processes an incoming delivery acknowledgment.
func (rm *ReceiptManager) HandleDeliveryAck(msg *message.Message) error {
	if msg.Type != message.TypeDeliveryAck {
		return nil // Not a delivery ack, ignore
	}

	// Parse the DeliveryInfo payload
	var info message.DeliveryInfo
	if err := json.Unmarshal([]byte(msg.Payload), &info); err != nil {
		return err
	}

	rm.mu.Lock()
	receipt, exists := rm.pending[info.MessageID]
	if !exists {
		rm.mu.Unlock()
		// Not tracking this message - might be an ack for something we sent directly
		// via pubsub without tracking, or an ack we don't care about
		return nil
	}

	// Update receipt status
	receipt.mu.Lock()
	receipt.Status = info.Status
	receipt.LastAttempt = time.Now()
	receipt.mu.Unlock()

	// If delivered, read, or failed - remove from pending
	if info.Status == message.DeliveryDelivered ||
		info.Status == message.DeliveryRead ||
		info.Status == message.DeliveryFailed {
		delete(rm.pending, info.MessageID)
	}

	// Notify callback
	if rm.OnReceiptReceived != nil {
		rm.OnReceiptReceived(receipt)
	}
	rm.mu.Unlock()

	return nil
}

// MarkDelivered marks a message as delivered (when we receive a direct ack).
func (rm *ReceiptManager) MarkDelivered(msgID string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if receipt, exists := rm.pending[msgID]; exists {
		receipt.mu.Lock()
		receipt.Status = message.DeliveryDelivered
		receipt.LastAttempt = time.Now()
		receipt.mu.Unlock()

		if rm.OnReceiptReceived != nil {
			rm.OnReceiptReceived(receipt)
		}
	}
}

// MarkReceived marks a message as received (when we receive a message addressed to us).
func (rm *ReceiptManager) MarkReceived(msgID string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if receipt, exists := rm.pending[msgID]; exists {
		receipt.mu.Lock()
		receipt.Status = message.DeliveryReceived
		receipt.LastAttempt = time.Now()
		receipt.mu.Unlock()

		if rm.OnReceiptReceived != nil {
			rm.OnReceiptReceived(receipt)
		}
	}
}

// PendingReceipts returns all messages awaiting delivery confirmation.
func (rm *ReceiptManager) PendingReceipts() []*PendingReceipt {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	result := make([]*PendingReceipt, 0, len(rm.pending))
	for _, r := range rm.pending {
		result = append(result, r)
	}
	return result
}

// GetReceipt returns the receipt status for a message.
func (rm *ReceiptManager) GetReceipt(msgID string) *PendingReceipt {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.pending[msgID]
}

// retryLoop periodically checks pending messages and retries or marks as failed.
func (rm *ReceiptManager) retryLoop() {
	ticker := time.NewTicker(AckCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rm.ctx.Done():
			return
		case <-ticker.C:
			rm.checkPending()
		}
	}
}

// checkPending iterates pending receipts and retries or fails them.
func (rm *ReceiptManager) checkPending() {
	now := time.Now()

	rm.mu.Lock()
	defer rm.mu.Unlock()

	for msgID, receipt := range rm.pending {
		receipt.mu.Lock()

		// Check if we've exceeded the timeout
		if now.Sub(receipt.SentAt) > AckTimeout {
			receipt.Status = message.DeliveryFailed
			if rm.OnReceiptReceived != nil {
				rm.OnReceiptReceived(receipt)
			}
			delete(rm.pending, msgID)
			receipt.mu.Unlock()
			continue
		}

		// Check if we should retry (30 seconds elapsed, retries < 3)
		if now.Sub(receipt.LastAttempt) > 30*time.Second && receipt.RetryCount < MaxRetries {
			receipt.RetryCount++
			receipt.LastAttempt = now

			// Re-send the original message via pubsub
			// Note: We need the original message to re-send it.
			// For now, we just track the retry count. The actual re-send
			// would need the original message content.
			// In practice, the mesh will re-relay the message if TTL allows.

			receipt.mu.Unlock()
			continue
		}

		receipt.mu.Unlock()
	}
}

// Stop stops the receipt manager.
func (rm *ReceiptManager) Stop() {
	if rm.cancel != nil {
		rm.cancel()
	}
}