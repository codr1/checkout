package handlers

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/webhook"

	"checkout/config"
)

// WebhookPaymentState represents the cached state of a payment from webhooks
type WebhookPaymentState struct {
	ID               string                 `json:"id"`
	Status           string                 `json:"status"`
	LastUpdated      time.Time              `json:"last_updated"`
	PaymentType      string                 `json:"payment_type"` // "payment_intent", "payment_link", "terminal"
	Amount           int64                  `json:"amount"`
	Currency         string                 `json:"currency"`
	Metadata         map[string]string      `json:"metadata"`
	LastPaymentError string                 `json:"last_payment_error,omitempty"` // Store as string for simplicity
	AdditionalData   map[string]interface{} `json:"additional_data,omitempty"`
}

// WebhookStateCache manages cached payment states from webhooks
type WebhookStateCache struct {
	ByPaymentIntent map[string]*WebhookPaymentState `json:"by_payment_intent"`
	ByPaymentLink   map[string]*WebhookPaymentState `json:"by_payment_link"`
	ByReader        map[string]*WebhookPaymentState `json:"by_reader"`
	Mutex           sync.RWMutex                    `json:"-"`
}

// Global payment state cache
var webhookCache = &WebhookStateCache{
	ByPaymentIntent: make(map[string]*WebhookPaymentState),
	ByPaymentLink:   make(map[string]*WebhookPaymentState),
	ByReader:        make(map[string]*WebhookPaymentState),
}

// GetCachedPaymentState retrieves cached payment state by ID and type
func GetCachedPaymentState(id, paymentType string) (*WebhookPaymentState, bool) {
	webhookCache.Mutex.RLock()
	defer webhookCache.Mutex.RUnlock()

	var state *WebhookPaymentState
	var exists bool

	switch paymentType {
	case "payment_intent":
		state, exists = webhookCache.ByPaymentIntent[id]
	case "payment_link":
		state, exists = webhookCache.ByPaymentLink[id]
	case "terminal":
		state, exists = webhookCache.ByReader[id]
	default:
		return nil, false
	}

	if !exists || state == nil {
		return nil, false
	}

	// Check if state has expired (120 seconds as per config)
	if time.Since(state.LastUpdated) > config.PaymentTimeout {
		// State expired, remove from cache
		go func() {
			webhookCache.Mutex.Lock()
			defer webhookCache.Mutex.Unlock()

			switch paymentType {
			case "payment_intent":
				delete(webhookCache.ByPaymentIntent, id)
			case "payment_link":
				delete(webhookCache.ByPaymentLink, id)
			case "terminal":
				delete(webhookCache.ByReader, id)
			}
		}()
		return nil, false
	}

	return state, true
}

// setCachedPaymentState stores payment state in cache
func setCachedPaymentState(id, paymentType string, state *WebhookPaymentState) {
	webhookCache.Mutex.Lock()
	defer webhookCache.Mutex.Unlock()

	state.LastUpdated = time.Now()

	switch paymentType {
	case "payment_intent":
		webhookCache.ByPaymentIntent[id] = state
	case "payment_link":
		webhookCache.ByPaymentLink[id] = state
	case "terminal":
		webhookCache.ByReader[id] = state
	}

	log.Printf("[Cache] Stored %s state for ID: %s, Status: %s", paymentType, id, state.Status)
}

// cleanupExpiredStates removes expired states from cache (called periodically)
func cleanupExpiredStates() {
	webhookCache.Mutex.Lock()
	defer webhookCache.Mutex.Unlock()

	now := time.Now()
	expiry := config.PaymentTimeout

	// Cleanup payment intents
	for id, state := range webhookCache.ByPaymentIntent {
		if now.Sub(state.LastUpdated) > expiry {
			delete(webhookCache.ByPaymentIntent, id)
			log.Printf("[Cache] Expired payment_intent state: %s", id)
		}
	}

	// Cleanup payment links
	for id, state := range webhookCache.ByPaymentLink {
		if now.Sub(state.LastUpdated) > expiry {
			delete(webhookCache.ByPaymentLink, id)
			log.Printf("[Cache] Expired payment_link state: %s", id)
		}
	}

	// Cleanup terminal readers
	for id, state := range webhookCache.ByReader {
		if now.Sub(state.LastUpdated) > expiry {
			delete(webhookCache.ByReader, id)
			log.Printf("[Cache] Expired terminal state: %s", id)
		}
	}
}

// Start periodic cleanup of expired states
func init() {
	go func() {
		ticker := time.NewTicker(30 * time.Second) // Cleanup every 30 seconds
		defer ticker.Stop()

		for range ticker.C {
			cleanupExpiredStates()
		}
	}()
}

// StripeWebhookHandler processes Stripe webhook events
// StripeWebhookHandler processes Stripe webhook events
func StripeWebhookHandler(w http.ResponseWriter, r *http.Request) {
	// Read request body
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading webhook body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Get Stripe signature from header
	sigHeader := r.Header.Get("Stripe-Signature")
	webhookSecret := config.GetStripeWebhookSecret()

	if webhookSecret == "" {
		log.Printf("Warning: Stripe webhook secret not configured")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Verify signature
	event, err := webhook.ConstructEvent(payload, sigHeader, webhookSecret)
	if err != nil {
		log.Printf("Webhook signature verification failed: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	log.Printf("[Webhook] Received event: %s", event.Type)

	// Handle different event types
	switch event.Type {
	case "payment_intent.created":
		handlePaymentIntentCreated(event.Data.Raw)

	case "payment_intent.succeeded":
		handlePaymentIntentSucceeded(event.Data.Raw)

	case "payment_intent.payment_failed":
		handlePaymentIntentFailed(event.Data.Raw)

	case "payment_intent.canceled":
		handlePaymentIntentCanceled(event.Data.Raw)

	case "payment_intent.requires_action":
		handlePaymentIntentRequiresAction(event.Data.Raw)

	case "payment_link.completed":
		handlePaymentLinkCompleted(event.Data.Raw)

	case "payment_link.updated":
		handlePaymentLinkUpdated(event.Data.Raw)

	case "terminal.reader.action_succeeded":
		handleTerminalActionSucceeded(event.Data.Raw)

	case "terminal.reader.action_failed":
		handleTerminalActionFailed(event.Data.Raw)

	case "charge.succeeded":
		handleChargeSucceeded(event.Data.Raw)

	case "charge.failed":
		handleChargeFailed(event.Data.Raw)

	default:
		log.Printf("[Webhook] Unhandled event type: %s", event.Type)
	}

	// Return a success response to Stripe
	w.WriteHeader(http.StatusOK)
}

// Helper functions for webhook event handling

func handlePaymentIntentCreated(raw json.RawMessage) {
	var intent stripe.PaymentIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		log.Printf("Error parsing payment_intent.created: %v", err)
		return
	}

	state := &WebhookPaymentState{
		ID:          intent.ID,
		Status:      string(intent.Status),
		PaymentType: "payment_intent",
		Amount:      intent.Amount,
		Currency:    string(intent.Currency),
		Metadata:    intent.Metadata,
	}

	setCachedPaymentState(intent.ID, "payment_intent", state)
	log.Printf("[Webhook] Payment intent created: %s", intent.ID)
}

func handlePaymentIntentSucceeded(raw json.RawMessage) {
	var intent stripe.PaymentIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		log.Printf("Error parsing payment_intent.succeeded: %v", err)
		return
	}

	state := &WebhookPaymentState{
		ID:          intent.ID,
		Status:      "succeeded",
		PaymentType: "payment_intent",
		Amount:      intent.Amount,
		Currency:    string(intent.Currency),
		Metadata:    intent.Metadata,
	}

	setCachedPaymentState(intent.ID, "payment_intent", state)
	log.Printf("[Webhook] Payment intent succeeded: %s", intent.ID)
}

func handlePaymentIntentFailed(raw json.RawMessage) {
	var intent stripe.PaymentIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		log.Printf("Error parsing payment_intent.payment_failed: %v", err)
		return
	}

	errorMessage := "unknown error"
	if intent.LastPaymentError != nil {
		errorMessage = string(intent.LastPaymentError.Type)
	}

	state := &WebhookPaymentState{
		ID:               intent.ID,
		Status:           "failed",
		PaymentType:      "payment_intent",
		Amount:           intent.Amount,
		Currency:         string(intent.Currency),
		Metadata:         intent.Metadata,
		LastPaymentError: errorMessage,
	}

	setCachedPaymentState(intent.ID, "payment_intent", state)
	log.Printf("[Webhook] Payment intent failed: %s, reason: %s", intent.ID, errorMessage)
}

func handlePaymentIntentCanceled(raw json.RawMessage) {
	var intent stripe.PaymentIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		log.Printf("Error parsing payment_intent.canceled: %v", err)
		return
	}

	state := &WebhookPaymentState{
		ID:          intent.ID,
		Status:      "canceled",
		PaymentType: "payment_intent",
		Amount:      intent.Amount,
		Currency:    string(intent.Currency),
		Metadata:    intent.Metadata,
	}

	setCachedPaymentState(intent.ID, "payment_intent", state)
	log.Printf("[Webhook] Payment intent canceled: %s", intent.ID)
}

func handlePaymentIntentRequiresAction(raw json.RawMessage) {
	var intent stripe.PaymentIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		log.Printf("Error parsing payment_intent.requires_action: %v", err)
		return
	}

	state := &WebhookPaymentState{
		ID:          intent.ID,
		Status:      "requires_action",
		PaymentType: "payment_intent",
		Amount:      intent.Amount,
		Currency:    string(intent.Currency),
		Metadata:    intent.Metadata,
	}

	setCachedPaymentState(intent.ID, "payment_intent", state)
	log.Printf("[Webhook] Payment intent requires action: %s", intent.ID)
}

func handlePaymentLinkCompleted(raw json.RawMessage) {
	var paymentLink stripe.PaymentLink
	if err := json.Unmarshal(raw, &paymentLink); err != nil {
		log.Printf("Error parsing payment_link.completed: %v", err)
		return
	}

	state := &WebhookPaymentState{
		ID:          paymentLink.ID,
		Status:      "completed",
		PaymentType: "payment_link",
		Metadata:    paymentLink.Metadata,
	}

	setCachedPaymentState(paymentLink.ID, "payment_link", state)
	log.Printf("[Webhook] Payment link completed: %s", paymentLink.ID)
}

func handlePaymentLinkUpdated(raw json.RawMessage) {
	var paymentLink stripe.PaymentLink
	if err := json.Unmarshal(raw, &paymentLink); err != nil {
		log.Printf("Error parsing payment_link.updated: %v", err)
		return
	}

	// Only cache if status changed to something meaningful
	// Only cache if status changed to something meaningful
	if !paymentLink.Active {
		state := &WebhookPaymentState{
			ID:          paymentLink.ID,
			Status:      "inactive",
			PaymentType: "payment_link",
			Metadata:    paymentLink.Metadata,
		}

		setCachedPaymentState(paymentLink.ID, "payment_link", state)
		log.Printf("[Webhook] Payment link updated: %s", paymentLink.ID)
	}
}

func handleTerminalActionSucceeded(raw json.RawMessage) {
	// Terminal events have a different structure, may need adjustment
	var event map[string]interface{}
	if err := json.Unmarshal(raw, &event); err != nil {
		log.Printf("Error parsing terminal.reader.action_succeeded: %v", err)
		return
	}

	// Extract reader ID and relevant info
	if reader, ok := event["object"].(map[string]interface{}); ok {
		if readerID, ok := reader["id"].(string); ok {
			state := &WebhookPaymentState{
				ID:             readerID,
				Status:         "action_succeeded",
				PaymentType:    "terminal",
				AdditionalData: event,
			}

			setCachedPaymentState(readerID, "terminal", state)
			log.Printf("[Webhook] Terminal action succeeded: %s", readerID)
		}
	}
}

func handleTerminalActionFailed(raw json.RawMessage) {
	var event map[string]interface{}
	if err := json.Unmarshal(raw, &event); err != nil {
		log.Printf("Error parsing terminal.reader.action_failed: %v", err)
		return
	}

	if reader, ok := event["object"].(map[string]interface{}); ok {
		if readerID, ok := reader["id"].(string); ok {
			state := &WebhookPaymentState{
				ID:             readerID,
				Status:         "action_failed",
				PaymentType:    "terminal",
				AdditionalData: event,
			}

			setCachedPaymentState(readerID, "terminal", state)
			log.Printf("[Webhook] Terminal action failed: %s", readerID)
		}
	}
}

func handleChargeSucceeded(raw json.RawMessage) {
	var charge stripe.Charge
	if err := json.Unmarshal(raw, &charge); err != nil {
		log.Printf("Error parsing charge.succeeded: %v", err)
		return
	}

	// Cache charge success as backup confirmation
	if charge.PaymentIntent != nil {
		state := &WebhookPaymentState{
			ID:          charge.PaymentIntent.ID,
			Status:      "charge_succeeded",
			PaymentType: "payment_intent",
			Amount:      charge.Amount,
			Currency:    string(charge.Currency),
			Metadata:    charge.Metadata,
		}

		setCachedPaymentState(charge.PaymentIntent.ID, "payment_intent", state)
		log.Printf("[Webhook] Charge succeeded for payment intent: %s", charge.PaymentIntent.ID)
	}
}

func handleChargeFailed(raw json.RawMessage) {
	var charge stripe.Charge
	if err := json.Unmarshal(raw, &charge); err != nil {
		log.Printf("Error parsing charge.failed: %v", err)
		return
	}

	errorMessage := "unknown error"
	if charge.FailureMessage != "" {
		errorMessage = charge.FailureMessage
	}

	if charge.PaymentIntent != nil {
		state := &WebhookPaymentState{
			ID:               charge.PaymentIntent.ID,
			Status:           "charge_failed",
			PaymentType:      "payment_intent",
			Amount:           charge.Amount,
			Currency:         string(charge.Currency),
			Metadata:         charge.Metadata,
			LastPaymentError: errorMessage,
		}

		setCachedPaymentState(charge.PaymentIntent.ID, "payment_intent", state)
		log.Printf("[Webhook] Charge failed for payment intent: %s, reason: %s", charge.PaymentIntent.ID, errorMessage)
	}
}
