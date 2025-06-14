package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/webhook"

	"checkout/config"
	"checkout/services"
	"checkout/utils"
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

	utils.Debug("webhook", "Cached payment state", "type", paymentType, "id", id, "status", state.Status)
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
			utils.Debug("webhook", "Expired payment_intent state", "id", id)
		}
	}

	// Cleanup payment links
	for id, state := range webhookCache.ByPaymentLink {
		if now.Sub(state.LastUpdated) > expiry {
			delete(webhookCache.ByPaymentLink, id)
			utils.Debug("webhook", "Expired payment_link state", "id", id)
		}
	}

	// Cleanup terminal readers
	for id, state := range webhookCache.ByReader {
		if now.Sub(state.LastUpdated) > expiry {
			delete(webhookCache.ByReader, id)
			utils.Debug("webhook", "Expired terminal state", "id", id)
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
func StripeWebhookHandler(w http.ResponseWriter, r *http.Request) {
	// Read request body
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		utils.Error("webhook", "Error reading webhook body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Get Stripe signature from header
	sigHeader := r.Header.Get("Stripe-Signature")
	webhookSecret := config.GetStripeWebhookSecret()

	if webhookSecret == "" {
		utils.Warn("webhook", "Stripe webhook secret not configured")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Verify signature
	event, err := webhook.ConstructEvent(payload, sigHeader, webhookSecret)
	if err != nil {
		utils.Error("webhook", "Signature verification failed", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	utils.Info("webhook", "Received event", "type", event.Type, "id", event.ID)

	// Handle different event types
	switch event.Type {
	case "payment_intent.created":
		handlePaymentIntentCreated(event.Data.Raw)

	case "payment_intent.succeeded":
		handlePaymentIntentSucceeded(event.Data.Raw)
		sendSSEUpdateFromWebhook(event)

	case "payment_intent.payment_failed":
		handlePaymentIntentFailed(event.Data.Raw)
		sendSSEUpdateFromWebhook(event)

	case "payment_intent.canceled":
		handlePaymentIntentCanceled(event.Data.Raw)
		sendSSEUpdateFromWebhook(event)

	case "payment_intent.requires_action":
		handlePaymentIntentRequiresAction(event.Data.Raw)
		sendSSEUpdateFromWebhook(event)

	case "payment_link.completed":
		handlePaymentLinkCompleted(event.Data.Raw)
		sendSSEUpdateFromWebhook(event)

	case "payment_link.updated":
		handlePaymentLinkUpdated(event.Data.Raw)

	case "terminal.reader.action_succeeded":
		handleTerminalActionSucceeded(event.Data.Raw)
		sendSSEUpdateFromWebhook(event)

	case "terminal.reader.action_failed":
		handleTerminalActionFailed(event.Data.Raw)
		sendSSEUpdateFromWebhook(event)

	case "charge.succeeded":
		handleChargeSucceeded(event.Data.Raw)
		sendSSEUpdateFromWebhook(event)

	case "charge.failed":
		handleChargeFailed(event.Data.Raw)
		sendSSEUpdateFromWebhook(event)

	default:
		utils.Error("webhook", "Unhandled event type", "type", event.Type)
	}

	// Return a success response to Stripe
	w.WriteHeader(http.StatusOK)
}

// Helper functions for webhook event handling

func handlePaymentIntentCreated(raw json.RawMessage) {
	var intent stripe.PaymentIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		utils.Error("webhook", "Error parsing payment_intent.created", "error", err)
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
	utils.Debug("webhook", "Payment intent created", "id", intent.ID, "amount", intent.Amount, "currency", intent.Currency)
}

func handlePaymentIntentSucceeded(raw json.RawMessage) {
	var intent stripe.PaymentIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		utils.Error("webhook", "Error parsing payment_intent.succeeded", "error", err)
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
	utils.Info("webhook", "Payment intent succeeded", "id", intent.ID, "amount", intent.Amount)
}

func handlePaymentIntentFailed(raw json.RawMessage) {
	var intent stripe.PaymentIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		utils.Error("webhook", "Error parsing payment_intent.payment_failed", "error", err)
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
	utils.Error("webhook", "Payment intent failed", "id", intent.ID, "reason", errorMessage)
}

func handlePaymentIntentCanceled(raw json.RawMessage) {
	var intent stripe.PaymentIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		utils.Error("webhook", "Error parsing payment_intent.canceled", "error", err)
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
	utils.Info("webhook", "Payment intent canceled", "id", intent.ID)
}

func handlePaymentIntentRequiresAction(raw json.RawMessage) {
	var intent stripe.PaymentIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		utils.Error("webhook", "Error parsing payment_intent.requires_action", "error", err)
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
	utils.Debug("webhook", "Payment intent requires action", "id", intent.ID)
}

func handlePaymentLinkCompleted(raw json.RawMessage) {
	var paymentLink stripe.PaymentLink
	if err := json.Unmarshal(raw, &paymentLink); err != nil {
		utils.Error("webhook", "Error parsing payment_link.completed", "error", err)
		return
	}

	state := &WebhookPaymentState{
		ID:          paymentLink.ID,
		Status:      "completed",
		PaymentType: "payment_link",
		Metadata:    paymentLink.Metadata,
	}

	setCachedPaymentState(paymentLink.ID, "payment_link", state)
	utils.Info("webhook", "Payment link completed", "id", paymentLink.ID)
}

func handlePaymentLinkUpdated(raw json.RawMessage) {
	var paymentLink stripe.PaymentLink
	if err := json.Unmarshal(raw, &paymentLink); err != nil {
		utils.Error("webhook", "Error parsing payment_link.updated", "error", err)
		return
	}

	// Only cache if status changed to something meaningful
	if !paymentLink.Active {
		state := &WebhookPaymentState{
			ID:          paymentLink.ID,
			Status:      "inactive",
			PaymentType: "payment_link",
			Metadata:    paymentLink.Metadata,
		}

		setCachedPaymentState(paymentLink.ID, "payment_link", state)
		utils.Debug("webhook", "Payment link updated to inactive", "id", paymentLink.ID)
	}
}

func handleTerminalActionSucceeded(raw json.RawMessage) {
	// Terminal events have a different structure, may need adjustment
	var event map[string]interface{}
	if err := json.Unmarshal(raw, &event); err != nil {
		utils.Error("webhook", "Error parsing terminal.reader.action_succeeded", "error", err)
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
			utils.Debug("webhook", "Terminal action succeeded", "reader_id", readerID)
		}
	}
}

func handleTerminalActionFailed(raw json.RawMessage) {
	var event map[string]interface{}
	if err := json.Unmarshal(raw, &event); err != nil {
		utils.Error("webhook", "Error parsing terminal.reader.action_failed", "error", err)
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
			utils.Error("webhook", "Terminal action failed", "reader_id", readerID)
		}
	}
}

func handleChargeSucceeded(raw json.RawMessage) {
	var charge stripe.Charge
	if err := json.Unmarshal(raw, &charge); err != nil {
		utils.Error("webhook", "Error parsing charge.succeeded", "error", err)
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
		utils.Info("webhook", "Charge succeeded", "payment_intent_id", charge.PaymentIntent.ID, "amount", charge.Amount)
	}
}

func handleChargeFailed(raw json.RawMessage) {
	var charge stripe.Charge
	if err := json.Unmarshal(raw, &charge); err != nil {
		utils.Error("webhook", "Error parsing charge.failed", "error", err)
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
		utils.Error("webhook", "Charge failed", "payment_intent_id", charge.PaymentIntent.ID, "reason", errorMessage)
	}
}

// sendSSEUpdateFromWebhook sends SSE updates based on webhook events
func sendSSEUpdateFromWebhook(event stripe.Event) {
	switch event.Type {
	case "payment_intent.succeeded", "payment_intent.payment_failed", "payment_intent.canceled":
		if paymentIntent := extractPaymentIntentFromEvent(event); paymentIntent != nil {
			sendTerminalSSEUpdate(paymentIntent.ID, paymentIntent)
		}
	case "payment_link.completed":
		if paymentLinkID := extractPaymentLinkIDFromEvent(event); paymentLinkID != "" {
			sendQRSSEUpdate(paymentLinkID, "completed")
		}
	case "terminal.reader.action_succeeded", "terminal.reader.action_failed":
		actionData := extractTerminalActionFromEvent(event)
		if rawData, ok := actionData.(json.RawMessage); ok && len(rawData) > 0 {
			sendTerminalActionSSEUpdate(actionData)
		}
	case "charge.succeeded", "charge.failed":
		if charge := extractChargeFromEvent(event); charge != nil {
			// Try to find associated payment intent
			if charge.PaymentIntent != nil {
				sendTerminalSSEUpdate(charge.PaymentIntent.ID, charge.PaymentIntent)
			}
		}
	}
}

// sendTerminalSSEUpdate sends SSE update for terminal payments
func sendTerminalSSEUpdate(intentID string, intent *stripe.PaymentIntent) {
	state, exists := GlobalPaymentStateManager.GetPayment(intentID)
	if !exists {
		return
	}

	terminalState, ok := state.(*TerminalPaymentState)
	if !ok {
		return
	}

	var result PaymentStatusResult

	switch intent.Status {
	case stripe.PaymentIntentStatusSucceeded:
		result = handleTerminalPaymentSuccess(intentID, terminalState, intent)
	case stripe.PaymentIntentStatusCanceled, stripe.PaymentIntentStatusRequiresPaymentMethod:
		result = handleTerminalPaymentFailure(intentID, intent)
	default:
		// Continue with progress update
		progress := calculateProgressInfo(state.GetStartTime(), config.PaymentTimeout)
		options := PaymentProgressOptions{
			PaymentID:     intentID,
			PaymentType:   "terminal",
			Progress:      progress,
			StatusMessage: fmt.Sprintf("Processing... (Status: %s)", intent.Status),
			ReaderID:      terminalState.ReaderID,
			PaymentStatus: string(intent.Status),
		}
		result = PaymentStatusResult{
			Component:  createPaymentProgressComponentWithOptions(options),
			ShouldStop: false,
		}
	}

	if result.Component != nil {
		GlobalSSEBroadcaster.BroadcastPaymentUpdate(intentID, result.Component)
	}

	if result.ShouldStop {
		GlobalSSEBroadcaster.RemoveConnection(intentID)
	}
}

// sendQRSSEUpdate sends SSE update for QR payments
func sendQRSSEUpdate(paymentLinkID, status string) {
	state, exists := GlobalPaymentStateManager.GetPayment(paymentLinkID)
	if !exists {
		return
	}

	var result PaymentStatusResult

	switch status {
	case "completed":
		// Create a simple payment link status to pass to the handler
		paymentLinkStatus := services.PaymentLinkStatus{
			Completed:     true,
			CustomerEmail: "", // Will be extracted from cached state if available
		}

		// Try to get customer email from cached state
		if cachedState, found := GetCachedPaymentState(paymentLinkID, "payment_link"); found {
			if email, exists := cachedState.Metadata["customer_email"]; exists {
				paymentLinkStatus.CustomerEmail = email
			}
		}

		result = handleQRPaymentSuccess(paymentLinkID, paymentLinkStatus)
	default:
		// Continue with progress update
		progress := calculateProgressInfo(state.GetStartTime(), config.PaymentTimeout)
		result = PaymentStatusResult{
			Component:  createPaymentProgressComponent(paymentLinkID, progress, "qr"),
			ShouldStop: false,
		}
	}

	if result.Component != nil {
		GlobalSSEBroadcaster.BroadcastPaymentUpdate(paymentLinkID, result.Component)
	}

	if result.ShouldStop {
		GlobalSSEBroadcaster.RemoveConnection(paymentLinkID)
	}
}

// sendTerminalActionSSEUpdate sends SSE update for terminal reader actions
func sendTerminalActionSSEUpdate(actionData interface{}) {
	// This would handle terminal reader action events
	// Implementation depends on the specific action data structure
	utils.Debug("sse", "Terminal action update", "action_data", actionData)
}

// Helper functions to extract data from webhook events
func extractPaymentIntentFromEvent(event stripe.Event) *stripe.PaymentIntent {
	var paymentIntent stripe.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &paymentIntent); err != nil {
		utils.Error("webhook", "Error parsing payment intent from webhook", "error", err)
		return nil
	}
	return &paymentIntent
}

func extractPaymentLinkIDFromEvent(event stripe.Event) string {
	var paymentLink stripe.PaymentLink
	if err := json.Unmarshal(event.Data.Raw, &paymentLink); err != nil {
		utils.Error("webhook", "Error parsing payment link from webhook", "error", err)
		return ""
	}
	return paymentLink.ID
}

func extractChargeFromEvent(event stripe.Event) *stripe.Charge {
	var charge stripe.Charge
	if err := json.Unmarshal(event.Data.Raw, &charge); err != nil {
		utils.Error("webhook", "Error parsing charge from webhook", "error", err)
		return nil
	}
	return &charge
}

func extractTerminalActionFromEvent(event stripe.Event) interface{} {
	// This would extract terminal reader action data
	return event.Data.Raw
}
