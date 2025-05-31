package handlers

import (
	"log"
	"sync"
	"time"

	"checkout/config"
	"checkout/services"
	"checkout/templates"
)

// PaymentState interface for all payment state types
type PaymentState interface {
	GetID() string
	GetPaymentType() string
	GetStartTime() time.Time
	IsExpired(timeout time.Duration) bool
	GetMetadata() map[string]interface{}
}

// PaymentStateManager manages all payment states in a unified way
type PaymentStateManager struct {
	states map[string]PaymentState
	mutex  sync.RWMutex
}

// NewPaymentStateManager creates a new payment state manager
func NewPaymentStateManager() *PaymentStateManager {
	return &PaymentStateManager{
		states: make(map[string]PaymentState),
	}
}

// AddPayment adds a payment state to the manager
func (psm *PaymentStateManager) AddPayment(state PaymentState) {
	psm.mutex.Lock()
	defer psm.mutex.Unlock()
	psm.states[state.GetID()] = state
}

// GetPayment retrieves a payment state by ID
func (psm *PaymentStateManager) GetPayment(id string) (PaymentState, bool) {
	psm.mutex.RLock()
	defer psm.mutex.RUnlock()
	state, exists := psm.states[id]
	return state, exists
}

// RemovePayment removes a payment state by ID
func (psm *PaymentStateManager) RemovePayment(id string) {
	psm.mutex.Lock()
	defer psm.mutex.Unlock()
	delete(psm.states, id)
}

// CleanupExpired removes all expired payment states
func (psm *PaymentStateManager) CleanupExpired() {
	psm.mutex.Lock()
	defer psm.mutex.Unlock()
	
	for id, state := range psm.states {
		// Use unified timeout for all payment types
		if state.IsExpired(config.PaymentTimeout) {
			delete(psm.states, id)
		}
	}
}

// GetActiveCount returns the number of active payment states
func (psm *PaymentStateManager) GetActiveCount() int {
	psm.mutex.RLock()
	defer psm.mutex.RUnlock()
	return len(psm.states)
}

// GetActiveCountByType returns counts by payment type
func (psm *PaymentStateManager) GetActiveCountByType() (int, int) {
	psm.mutex.RLock()
	defer psm.mutex.RUnlock()
	
	terminalCount := 0
	qrCount := 0
	
	for _, state := range psm.states {
		switch state.GetPaymentType() {
		case "terminal":
			terminalCount++
		case "qr":
			qrCount++
		}
	}
	
	return terminalCount, qrCount
}

// GetStatesByType returns all states of a specific type
func (psm *PaymentStateManager) GetStatesByType(paymentType string) []PaymentState {
	psm.mutex.RLock()
	defer psm.mutex.RUnlock()
	
	var states []PaymentState
	for _, state := range psm.states {
		if state.GetPaymentType() == paymentType {
			states = append(states, state)
		}
	}
	return states
}

// ClearAll removes all payment states
func (psm *PaymentStateManager) ClearAll() {
	psm.mutex.Lock()
	defer psm.mutex.Unlock()
	psm.states = make(map[string]PaymentState)
}

// RemovePaymentAndClearCart removes a payment state and clears the cart in one operation
// This replaces the common pattern of: RemovePayment() + services.ClearPaymentState()
func (psm *PaymentStateManager) RemovePaymentAndClearCart(id string) {
	psm.mutex.Lock()
	defer psm.mutex.Unlock()
	
	// Remove the payment state
	delete(psm.states, id)
	
	// Clear the cart since the transaction is complete/cancelled
	services.AppState.CurrentCart = []templates.Service{}
	
	log.Printf("Removed payment state and cleared cart for payment: %s", id)
}

// ClearAllAndClearCart removes all payment states and clears the cart in one operation
// This replaces the pattern of: ClearAll() + services.ClearPaymentState()
func (psm *PaymentStateManager) ClearAllAndClearCart() {
	psm.mutex.Lock()
	defer psm.mutex.Unlock()
	
	// Clear all payment states
	psm.states = make(map[string]PaymentState)
	
	// Clear the cart since all transactions are being reset
	services.AppState.CurrentCart = []templates.Service{}
	
	log.Printf("Cleared all payment states and cart")
}

// ClearByTypeAndClearCart removes all payment states of a specific type and clears the cart
// Useful for clearing all QR or all terminal payments at once
func (psm *PaymentStateManager) ClearByTypeAndClearCart(paymentType string) {
	psm.mutex.Lock()
	defer psm.mutex.Unlock()
	
	removedCount := 0
	for id, state := range psm.states {
		if state.GetPaymentType() == paymentType {
			delete(psm.states, id)
			removedCount++
		}
	}
	
	// Clear the cart if any payments were removed
	if removedCount > 0 {
		services.AppState.CurrentCart = []templates.Service{}
		log.Printf("Removed %d %s payment states and cleared cart", removedCount, paymentType)
	}
}

// QRPaymentState represents QR payment link state
type QRPaymentState struct {
	PaymentLinkID string
	CreationTime  time.Time
}

// GetID returns the payment link ID
func (q *QRPaymentState) GetID() string {
	return q.PaymentLinkID
}

// GetPaymentType returns "qr"
func (q *QRPaymentState) GetPaymentType() string {
	return "qr"
}

// GetStartTime returns the creation time
func (q *QRPaymentState) GetStartTime() time.Time {
	return q.CreationTime
}

// IsExpired checks if the QR payment has expired
func (q *QRPaymentState) IsExpired(timeout time.Duration) bool {
	return time.Since(q.CreationTime) > timeout
}

// GetMetadata returns QR-specific metadata
func (q *QRPaymentState) GetMetadata() map[string]interface{} {
	return map[string]interface{}{
		"payment_link_id": q.PaymentLinkID,
		"creation_time":   q.CreationTime,
	}
}

// TerminalPaymentState represents terminal payment state
type TerminalPaymentState struct {
	PaymentIntentID string
	ReaderID        string
	StartTime       time.Time
	Email           string
	Cart            []templates.Service
	Summary         templates.CartSummary
}

// GetID returns the payment intent ID
func (t *TerminalPaymentState) GetID() string {
	return t.PaymentIntentID
}

// GetPaymentType returns "terminal"
func (t *TerminalPaymentState) GetPaymentType() string {
	return "terminal"
}

// GetStartTime returns the start time
func (t *TerminalPaymentState) GetStartTime() time.Time {
	return t.StartTime
}

// IsExpired checks if the terminal payment has expired
func (t *TerminalPaymentState) IsExpired(timeout time.Duration) bool {
	return time.Since(t.StartTime) > timeout
}

// GetMetadata returns terminal-specific metadata
func (t *TerminalPaymentState) GetMetadata() map[string]interface{} {
	return map[string]interface{}{
		"payment_intent_id": t.PaymentIntentID,
		"reader_id":         t.ReaderID,
		"start_time":        t.StartTime,
		"email":             t.Email,
		"cart_size":         len(t.Cart),
		"total":             t.Summary.Total,
	}
}


// PaymentEventType represents different types of payment events
type PaymentEventType string

const (
	PaymentEventSuccess   PaymentEventType = "success"
	PaymentEventFailed    PaymentEventType = "failed"
	PaymentEventCancelled PaymentEventType = "cancelled"
	PaymentEventExpired   PaymentEventType = "expired"
)

// PaymentEventLogger handles transaction logging with predefined event types
type PaymentEventLogger struct{}

// LogPaymentEvent logs a payment event with standardized transaction creation
func (pel *PaymentEventLogger) LogPaymentEvent(paymentID string, eventType PaymentEventType, paymentMethod string, cart []templates.Service, summary templates.CartSummary, email string) error {
	now := time.Now()
	
	// Create standardized payment type string
	paymentTypeStr := pel.getPaymentTypeString(paymentMethod, eventType)
	
	transaction := templates.Transaction{
		ID:            paymentID,
		Date:          now.Format("01/02/2006"),
		Time:          now.Format("15:04:05"),
		Services:      cart,
		Subtotal:      summary.Subtotal,
		Tax:           summary.Tax,
		Total:         summary.Total,
		PaymentType:   paymentTypeStr,
		CustomerEmail: email,
	}

	// Save transaction with error logging
	if err := services.SaveTransactionToCSV(transaction); err != nil {
		log.Printf("Error saving %s transaction for payment %s: %v", paymentTypeStr, paymentID, err)
		return err
	}

	log.Printf("Successfully logged %s transaction for payment %s", paymentTypeStr, paymentID)
	return nil
}

// LogPaymentEventFromState logs a payment event using payment state data
func (pel *PaymentEventLogger) LogPaymentEventFromState(state PaymentState, eventType PaymentEventType, email string) error {
	var cart []templates.Service
	var summary templates.CartSummary
	var paymentMethod string
	
	switch s := state.(type) {
	case *TerminalPaymentState:
		cart = s.Cart
		summary = s.Summary
		paymentMethod = "terminal"
		if email == "" {
			email = s.Email
		}
	case *QRPaymentState:
		// For QR payments, use current cart state
		cart = services.AppState.CurrentCart
		paymentMethod = "qr"
		// Calculate summary if not provided
		if summary.Total == 0 {
			if calcSummary, err := services.CalculateCartSummary(); err == nil {
				summary = calcSummary
			}
		}
	default:
		// Fallback to current cart state
		cart = services.AppState.CurrentCart
		paymentMethod = "unknown"
		if summary.Total == 0 {
			if calcSummary, err := services.CalculateCartSummary(); err == nil {
				summary = calcSummary
			}
		}
	}
	
	return pel.LogPaymentEvent(state.GetID(), eventType, paymentMethod, cart, summary, email)
}

// LogPaymentEventQuick logs a simple payment event (for failures/cancellations without detailed cart data)
func (pel *PaymentEventLogger) LogPaymentEventQuick(paymentID string, eventType PaymentEventType, paymentMethod string) error {
	return pel.LogPaymentEvent(paymentID, eventType, paymentMethod, []templates.Service{}, templates.CartSummary{}, "")
}

// getPaymentTypeString creates a standardized payment type string
func (pel *PaymentEventLogger) getPaymentTypeString(paymentMethod string, eventType PaymentEventType) string {
	switch eventType {
	case PaymentEventSuccess:
		return paymentMethod
	case PaymentEventFailed:
		return paymentMethod + "_failed"
	case PaymentEventCancelled:
		return paymentMethod + "_cancelled"
	case PaymentEventExpired:
		return paymentMethod + "_expired"
	default:
		return paymentMethod + "_unknown"
	}
}

// Global instances
var GlobalPaymentStateManager = NewPaymentStateManager()
var GlobalPaymentEventLogger = &PaymentEventLogger{}

