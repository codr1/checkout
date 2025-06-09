package handlers

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/paymentintent"
	"github.com/stripe/stripe-go/v74/paymentlink"

	"checkout/config"
	"checkout/services"
	"checkout/templates"
	"checkout/templates/checkout"
)

// SSEConnection represents a Server-Sent Events connection
type SSEConnection struct {
	Writer    http.ResponseWriter
	Flusher   http.Flusher
	PaymentID string
	Type      string // "qr" or "terminal"
	Done      chan bool
}

// SSEBroadcaster manages SSE connections and broadcasting
type SSEBroadcaster struct {
	connections map[string]*SSEConnection
	mutex       sync.RWMutex
}

// Global SSE broadcaster instance
var GlobalSSEBroadcaster = &SSEBroadcaster{
	connections: make(map[string]*SSEConnection),
}

// AddConnection adds a new SSE connection
func (b *SSEBroadcaster) AddConnection(paymentID, paymentType string, w http.ResponseWriter) *SSEConnection {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil
	}

	conn := &SSEConnection{
		Writer:    w,
		Flusher:   flusher,
		PaymentID: paymentID,
		Type:      paymentType,
		Done:      make(chan bool),
	}

	b.mutex.Lock()
	b.connections[paymentID] = conn
	b.mutex.Unlock()

	log.Printf("[SSE] New connection for %s payment: %s", paymentType, paymentID)
	return conn
}

// RemoveConnection removes an SSE connection
func (b *SSEBroadcaster) RemoveConnection(paymentID string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if conn, exists := b.connections[paymentID]; exists {
		close(conn.Done)
		delete(b.connections, paymentID)
		log.Printf("[SSE] Removed connection for payment: %s", paymentID)
	}
}

// BroadcastPaymentUpdate sends a payment update to relevant SSE connections
func (b *SSEBroadcaster) BroadcastPaymentUpdate(paymentID string, component templ.Component) {
	b.mutex.RLock()
	conn, exists := b.connections[paymentID]
	b.mutex.RUnlock()

	if !exists {
		log.Printf("[SSE] No connection found for payment %s", paymentID)
		return
	}

	// Render component to string - use context.Background() instead of nil
	var buf strings.Builder
	if err := component.Render(context.Background(), &buf); err != nil {
		log.Printf("[SSE] Error rendering component for %s: %v", paymentID, err)
		return
	}

	// Send SSE event - use HTMX-compatible format
	fmt.Fprintf(conn.Writer, "event: payment-update\n")
	fmt.Fprintf(conn.Writer, "data: %s\n\n", buf.String())
	conn.Flusher.Flush()

	log.Printf("[SSE] Sent payment update for: %s", paymentID)
}

// BroadcastModalUpdate sends a payment update that replaces the entire modal content
func (b *SSEBroadcaster) BroadcastModalUpdate(paymentID string, component templ.Component) {
	b.mutex.RLock()
	conn, exists := b.connections[paymentID]
	b.mutex.RUnlock()

	if !exists {
		log.Printf("[SSE] No connection found for payment %s", paymentID)
		return
	}

	// Render component to string
	var buf strings.Builder
	if err := component.Render(context.Background(), &buf); err != nil {
		log.Printf("[SSE] Error rendering component for %s: %v", paymentID, err)
		return
	}

	// Send SSE event that targets modal content to remove SSE container entirely
	fmt.Fprintf(conn.Writer, "event: modal-update\n")
	fmt.Fprintf(conn.Writer, "data: %s\n\n", buf.String())
	conn.Flusher.Flush()

	log.Printf("[SSE] Sent modal update for: %s", paymentID)
}

// PaymentSSEHandler handles SSE connections for payment updates
func PaymentSSEHandler(w http.ResponseWriter, r *http.Request) {
	paymentID := r.URL.Query().Get("payment_id")
	paymentType := r.URL.Query().Get("type") // "qr" or "terminal"

	log.Printf("[SSE] New connection request for %s payment: %s", paymentType, paymentID)

	if paymentID == "" || paymentType == "" {
		log.Printf("[SSE] Missing parameters: payment_id=%s, type=%s", paymentID, paymentType)
		http.Error(w, "payment_id and type parameters required", http.StatusBadRequest)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Add connection to broadcaster
	conn := GlobalSSEBroadcaster.AddConnection(paymentID, paymentType, w)
	if conn == nil {
		log.Printf("[SSE] Failed to add connection for payment: %s", paymentID)
		http.Error(w, "SSE not supported by client", http.StatusInternalServerError)
		return
	}

	log.Printf("[SSE] Successfully established connection for %s payment: %s", paymentType, paymentID)

	// Set up timeout
	timeout := time.NewTimer(config.PaymentTimeout)
	defer timeout.Stop()

	// Wait for payment completion or timeout - no periodic progress updates
	for {
		select {
		case <-conn.Done:
			GlobalSSEBroadcaster.RemoveConnection(paymentID)
			return
		case <-r.Context().Done():
			GlobalSSEBroadcaster.RemoveConnection(paymentID)
			return
		case <-timeout.C:
			// Payment timeout - send expiration event and cleanup
			handleSSETimeout(paymentID, paymentType)
			GlobalSSEBroadcaster.RemoveConnection(paymentID)
			return
		}
	}
}

// sendInitialSSEUpdate sends the initial progress update
func sendInitialSSEUpdate(conn *SSEConnection, paymentID, paymentType string) {
	state, exists := GlobalPaymentStateManager.GetPayment(paymentID)
	if !exists {
		return
	}

	progress := calculateProgressInfo(state.GetStartTime(), config.PaymentTimeout)
	component := createPaymentProgressComponent(paymentID, progress, paymentType)

	var buf strings.Builder
	if err := component.Render(context.Background(), &buf); err != nil {
		log.Printf("[SSE] Error rendering initial component: %v", err)
		return
	}

	fmt.Fprintf(conn.Writer, "event: payment-update\n")
	fmt.Fprintf(conn.Writer, "data: %s\n\n", buf.String())
	conn.Flusher.Flush()
}

// checkAndSendSSEUpdate checks payment status and sends update via SSE
func checkAndSendSSEUpdate(paymentID, paymentType string) {
	var result PaymentStatusResult

	switch paymentType {
	case "qr":
		result = checkQRPaymentStatus(paymentID)
	case "terminal":
		result = checkTerminalPaymentStatus(paymentID)
	default:
		return
	}

	if result.Component != nil {
		GlobalSSEBroadcaster.BroadcastPaymentUpdate(paymentID, result.Component)
	}

	if result.ShouldStop {
		GlobalSSEBroadcaster.RemoveConnection(paymentID)
	}
}

// handleSSETimeout handles payment timeout via SSE
func handleSSETimeout(paymentID, paymentType string) {
	switch paymentType {
	case "qr":
		result := handleQRPaymentTimeout(paymentID)
		if result.Component != nil {
			GlobalSSEBroadcaster.BroadcastPaymentUpdate(paymentID, result.Component)
		}
	case "terminal":
		// Fetch the real PaymentIntent from Stripe
		intent, err := paymentintent.Get(paymentID, nil)
		if err != nil {
			log.Printf("Error fetching PaymentIntent %s for timeout handling: %v", paymentID, err)
			// If we can't fetch it, create a minimal intent for cleanup
			intent = &stripe.PaymentIntent{
				ID:     paymentID,
				Status: stripe.PaymentIntentStatusRequiresPaymentMethod,
			}
		}
		result := handleTerminalPaymentTimeout(paymentID, intent)
		if result.Component != nil {
			GlobalSSEBroadcaster.BroadcastPaymentUpdate(paymentID, result.Component)
		}
	}
}

// PaymentStatusResult represents the result of checking payment status
type PaymentStatusResult struct {
	Status     string
	Completed  bool
	Failed     bool
	Expired    bool
	Message    string
	Component  templ.Component
	ShouldStop bool // Whether polling should stop
}

// PaymentPollingConfig holds configuration for payment status polling
type PaymentPollingConfig struct {
	PaymentID       string
	PaymentType     string // "qr" or "terminal"
	TimeoutDuration time.Duration
}

// ProgressInfo holds progress bar and countdown information
type ProgressInfo struct {
	SecondsRemaining int
	ProgressWidth    float64
	Elapsed          time.Duration
}

// Use the centralized configuration constant
const PAYMENT_POLLING_TIMEOUT = config.PaymentTimeout

// PaymentProgressOptions holds options for payment progress display
type PaymentProgressOptions struct {
	PaymentID     string
	PaymentType   string
	Progress      ProgressInfo
	StatusMessage string
	ReaderID      string
	PaymentStatus string
}

// createPaymentProgressComponent creates a generic payment progress component
func createPaymentProgressComponent(paymentID string, progress ProgressInfo, paymentType string) templ.Component {
	options := PaymentProgressOptions{
		PaymentID:   paymentID,
		PaymentType: paymentType,
		Progress:    progress,
	}
	return createPaymentProgressComponentWithOptions(options)
}

// createPaymentProgressComponentWithOptions creates a payment progress component with advanced options
// Now returns raw HTML that templates can embed with real-time server-calculated progress
func createPaymentProgressComponentWithOptions(opts PaymentProgressOptions) templ.Component {
	// Determine the status message
	statusMessage := config.GetPaymentMessage(opts.PaymentType, "default")
	if opts.StatusMessage != "" {
		statusMessage = opts.StatusMessage
	}

	// Build additional info for display
	var additionalInfo string
	if opts.PaymentType == "terminal" && opts.ReaderID != "" {
		additionalInfo = fmt.Sprintf(
			"<p><small>Reader: %s | Payment ID: %s</small></p>",
			opts.ReaderID,
			opts.PaymentID,
		)
	} else {
		additionalInfo = fmt.Sprintf("<p><small>Payment ID: %s</small></p>", opts.PaymentID)
	}

	// Generate the unified progress HTML with stop-polling trigger when final state reached
	var stopPollingAttr string
	if opts.Progress.SecondsRemaining <= 0 {
		// Payment has timed out, stop polling
		stopPollingAttr = `hx-trigger="none"`
	}

	// Generate the unified progress HTML (single line to avoid newline issues in SSE)
	progressHTML := fmt.Sprintf(
		`<div class="payment-progress %s-progress" %s><h4>%s in Progress</h4><p>%s</p><p>Payment expires in <span id="countdown">%d</span> seconds</p><div class="progress-bar"><div class="progress-fill" style="width: %.1f%%;"></div></div>%s</div>`,
		opts.PaymentType,
		stopPollingAttr,
		getPaymentTypeDisplayString(opts.PaymentType),
		statusMessage,
		opts.Progress.SecondsRemaining,
		opts.Progress.ProgressWidth,
		additionalInfo,
	)

	return templ.Raw(progressHTML)
}

// Helper function since we can't access template functions from handlers
func getPaymentTypeDisplayString(paymentType string) string {
	switch paymentType {
	case "qr":
		return "QR Code Payment"
	case "terminal":
		return "Terminal Payment"
	default:
		return "Payment"
	}
}

// calculateProgressInfo calculates progress bar and countdown information
func calculateProgressInfo(creationTime time.Time, timeoutDuration time.Duration) ProgressInfo {
	elapsed := time.Since(creationTime)
	remaining := timeoutDuration - elapsed

	secondsRemaining := int(remaining.Seconds())
	if secondsRemaining < 0 {
		secondsRemaining = 0
	}

	progressWidth := (elapsed.Seconds() / timeoutDuration.Seconds()) * 100
	if progressWidth > 100 {
		progressWidth = 100
	}

	return ProgressInfo{
		SecondsRemaining: secondsRemaining,
		ProgressWidth:    progressWidth,
		Elapsed:          elapsed,
	}
}

// checkPaymentStatusGeneric handles the common polling logic for both QR and terminal payments
func checkPaymentStatusGeneric(w http.ResponseWriter, r *http.Request, config PaymentPollingConfig) {
	paymentID := r.URL.Query().Get("payment_id")
	if paymentID == "" {
		// Use appropriate payment ID parameter based on type
		if config.PaymentType == "qr" {
			paymentID = r.URL.Query().Get("payment_link_id")
		} else if config.PaymentType == "terminal" {
			paymentID = r.URL.Query().Get("intent_id")
		}
	}

	if paymentID == "" {
		w.Write([]byte(`<p class="status-message">Waiting for payment information...</p>`))
		return
	}

	var result PaymentStatusResult

	// Handle different payment types
	switch config.PaymentType {
	case "qr":
		result = checkQRPaymentStatus(paymentID)
	case "terminal":
		result = checkTerminalPaymentStatus(paymentID)
	default:
		result = PaymentStatusResult{
			Status:     "error",
			Failed:     true,
			Message:    "Unknown payment type",
			ShouldStop: true,
		}
	}

	// Handle timeout, success, or failure
	if result.ShouldStop {
		// Add HTMX header to stop polling
		w.Header().Set("HX-Trigger", "stopPolling")
		if result.Component != nil {
			w.WriteHeader(http.StatusOK)
			if err := result.Component.Render(r.Context(), w); err != nil {
				log.Printf("Error rendering payment result component: %v", err)
			}
		} else {
			w.Write([]byte(result.Message))
		}
		return
	}

	// Continue polling - render progress component with updated countdown/progress
	if result.Component != nil {
		w.WriteHeader(http.StatusOK)
		if err := result.Component.Render(r.Context(), w); err != nil {
			log.Printf("Error rendering payment progress component: %v", err)
		}
	}
}

// checkQRPaymentStatus checks QR payment link status
// checkQRPaymentStatus checks QR payment link status
func checkQRPaymentStatus(paymentLinkID string) PaymentStatusResult {
	// Check if this is a new payment link we haven't seen before
	if _, exists := GlobalPaymentStateManager.GetPayment(paymentLinkID); !exists {
		qrState := &QRPaymentState{
			PaymentLinkID: paymentLinkID,
			CreationTime:  time.Now(),
		}
		GlobalPaymentStateManager.AddPayment(qrState)
	}

	state, _ := GlobalPaymentStateManager.GetPayment(paymentLinkID)
	progress := calculateProgressInfo(state.GetStartTime(), PAYMENT_POLLING_TIMEOUT)

	// Check for timeout
	if progress.SecondsRemaining <= 0 {
		return handleQRPaymentTimeout(paymentLinkID)
	}

	// First, check webhook cache if available
	if cachedState, found := GetCachedPaymentState(paymentLinkID, "payment_link"); found {
		log.Printf("[QR] Using cached state for payment link: %s, Status: %s", paymentLinkID, cachedState.Status)

		// Handle cached payment completion
		if cachedState.Status == "completed" {
			paymentLinkStatus := services.PaymentLinkStatus{
				Completed:     true,
				CustomerEmail: cachedState.Metadata["customer_email"],
			}
			return handleQRPaymentSuccess(paymentLinkID, paymentLinkStatus)
		}

		// Handle cached inactive/expired state
		if cachedState.Status == "inactive" {
			return handleQRPaymentTimeout(paymentLinkID)
		}
	}

	// Fallback to direct Stripe API call if no cached state
	log.Printf("[QR] No cached state found, checking Stripe API for payment link: %s", paymentLinkID)
	paymentLinkStatus, err := services.CheckPaymentLinkStatus(paymentLinkID)
	if err != nil {
		log.Printf("Error checking payment link status: %v", err)
		return PaymentStatusResult{
			Status:     "error",
			Failed:     true,
			Message:    "Error checking payment status",
			ShouldStop: true,
		}
	}

	// Handle completed payment
	if paymentLinkStatus.Completed {
		return handleQRPaymentSuccess(paymentLinkID, paymentLinkStatus)
	}

	// Continue polling - render progress using our reusable function
	component := createPaymentProgressComponent(paymentLinkID, progress, "qr")
	return PaymentStatusResult{
		Status:     "pending",
		Component:  component,
		ShouldStop: false,
	}
}

// checkTerminalPaymentStatus checks terminal payment status
func checkTerminalPaymentStatus(intentID string) PaymentStatusResult {
	log.Printf("[Terminal] Checking status for payment intent: %s", intentID)
	state, exists := GlobalPaymentStateManager.GetPayment(intentID)
	if !exists {
		log.Printf("[Terminal] No cached state found for PI %s", intentID)
		// Payment session not found - render a final "session concluded" message
		component := checkout.TerminalInteractionResultModal(
			"Payment Session Concluded",
			"This payment session is no longer active.",
			intentID,
			true, // hasCloseButton
			"",   // no additional message
		)
		return PaymentStatusResult{
			Status:     "concluded",
			Failed:     true,
			Component:  component,
			ShouldStop: true,
		}
	}
	log.Printf("[Terminal] Found cached state for PI %s", intentID)

	terminalState := state.(*TerminalPaymentState)
	progress := calculateProgressInfo(state.GetStartTime(), PAYMENT_POLLING_TIMEOUT)

	// Check for timeout
	if progress.SecondsRemaining <= 0 {
		// Fetch the real PaymentIntent to see its actual status
		intent, err := paymentintent.Get(intentID, nil)
		if err != nil {
			log.Printf("Error fetching PaymentIntent %s for timeout handling: %v", intentID, err)
			// If we can't fetch it, create a minimal intent for cleanup
			intent = &stripe.PaymentIntent{
				ID:     intentID,
				Status: stripe.PaymentIntentStatusRequiresPaymentMethod,
			}
		}
		return handleTerminalPaymentTimeout(intentID, intent)
	}

	// First, check webhook cache if available
	if cachedState, found := GetCachedPaymentState(intentID, "payment_intent"); found {
		log.Printf("[Terminal] Using cached state for payment intent: %s, Status: %s", intentID, cachedState.Status)

		// Handle cached payment success
		if cachedState.Status == "succeeded" || cachedState.Status == "charge_succeeded" {
			// Create a mock intent object with the status we need
			intent := &stripe.PaymentIntent{
				ID:     intentID,
				Status: stripe.PaymentIntentStatusSucceeded,
			}
			return handleTerminalPaymentSuccess(intentID, terminalState, intent)
		}

		// Handle cached payment failures
		if cachedState.Status == "failed" || cachedState.Status == "charge_failed" || cachedState.Status == "canceled" {
			// Create a mock intent object with the status we need
			intent := &stripe.PaymentIntent{
				ID:     intentID,
				Status: stripe.PaymentIntentStatusCanceled,
				LastPaymentError: &stripe.Error{
					Msg: cachedState.LastPaymentError,
				},
			}
			return handleTerminalPaymentFailure(intentID, intent)
		}
	}

	// Fallback to direct Stripe API call if no cached state
	log.Printf("[Terminal] No cached state found, checking Stripe API for payment intent: %s", intentID)
	intent, err := paymentintent.Get(intentID, nil)
	if err != nil {
		log.Printf("Error fetching PaymentIntent %s: %v", intentID, err)
		return PaymentStatusResult{
			Status:     "error",
			Failed:     true,
			Message:    "Error checking payment status",
			ShouldStop: true,
		}
	}

	// Check for various payment states
	switch intent.Status {
	case stripe.PaymentIntentStatusSucceeded:
		return handleTerminalPaymentSuccess(intentID, terminalState, intent)

	case stripe.PaymentIntentStatusCanceled:
		return handleTerminalPaymentFailure(intentID, intent)

	case stripe.PaymentIntentStatusRequiresPaymentMethod:
		// This is NORMAL for terminal payments - terminal is waiting for customer to present card
		elapsed := time.Since(terminalState.StartTime)
		secondsRemaining := int(math.Max(0, config.PaymentTimeout.Seconds()-elapsed.Seconds()))
		progressWidth := math.Min(100, (elapsed.Seconds()/config.PaymentTimeout.Seconds())*100)

		// Check if we've timed out
		if secondsRemaining <= 0 {
			return handleTerminalPaymentTimeout(intentID, intent)
		}

		// Show "waiting for card" progress
		options := PaymentProgressOptions{
			PaymentID:     intentID,
			PaymentType:   "terminal",
			Progress:      ProgressInfo{SecondsRemaining: secondsRemaining, ProgressWidth: progressWidth},
			StatusMessage: "Waiting for customer to present payment method on terminal...",
			ReaderID:      terminalState.ReaderID,
			PaymentStatus: string(intent.Status),
		}
		component := createPaymentProgressComponentWithOptions(options)
		return PaymentStatusResult{
			Status:    "waiting_for_card",
			Component: component,
		}

	case stripe.PaymentIntentStatusProcessing,
		stripe.PaymentIntentStatusRequiresConfirmation,
		stripe.PaymentIntentStatusRequiresAction:
		// Payment is still in progress, continue polling
		elapsed := time.Since(terminalState.StartTime)
		secondsRemaining := int(math.Max(0, config.PaymentTimeout.Seconds()-elapsed.Seconds()))
		progressWidth := math.Min(100, (elapsed.Seconds()/config.PaymentTimeout.Seconds())*100)

		var statusMessage string
		if intent.NextAction != nil &&
			intent.NextAction.Type == stripe.PaymentIntentNextActionType("display_terminal_receipt") {
			statusMessage = "Please take your receipt from the terminal."
		} else {
			statusMessage = fmt.Sprintf("Processing payment on terminal... (Status: %s)", intent.Status)
		}

		options := PaymentProgressOptions{
			PaymentID:     intentID,
			PaymentType:   "terminal",
			Progress:      ProgressInfo{SecondsRemaining: secondsRemaining, ProgressWidth: progressWidth},
			StatusMessage: statusMessage,
			ReaderID:      terminalState.ReaderID,
			PaymentStatus: string(intent.Status),
		}
		component := createPaymentProgressComponentWithOptions(options)
		return PaymentStatusResult{
			Status:    "processing",
			Component: component,
		}

	default:
		log.Printf("Unknown PaymentIntent status for terminal payment %s: %s", intentID, intent.Status)
		return PaymentStatusResult{
			Status:     "error",
			Failed:     true,
			Message:    fmt.Sprintf("Unknown payment status: %s", intent.Status),
			ShouldStop: true,
		}
	}
}

// Helper functions for QR payment handling
func handleQRPaymentTimeout(paymentLinkID string) PaymentStatusResult {
	log.Printf("Payment link %s timed out after %v", paymentLinkID, PAYMENT_POLLING_TIMEOUT)

	// Deactivate the payment link
	_, err := paymentlink.Update(paymentLinkID, &stripe.PaymentLinkParams{
		Active: stripe.Bool(false),
	})
	if err != nil {
		log.Printf("Error deactivating payment link %s: %v", paymentLinkID, err)
	}

	// Clean up state
	GlobalPaymentStateManager.RemovePayment(paymentLinkID)

	// Log transaction as expired
	GlobalPaymentEventLogger.LogPaymentEventQuick(paymentLinkID, PaymentEventExpired, "qr")

	component := checkout.PaymentExpired(paymentLinkID)
	return PaymentStatusResult{
		Status:     "expired",
		Expired:    true,
		Component:  component,
		ShouldStop: true,
	}
}

func handleQRPaymentSuccess(paymentLinkID string, paymentLinkStatus services.PaymentLinkStatus) PaymentStatusResult {
	log.Printf("Payment link %s completed successfully", paymentLinkID)

	// Calculate cart summary for transaction record
	summary, err := services.CalculateCartSummary()
	if err != nil {
		log.Printf("Error calculating cart summary for completed payment: %v", err)
		summary = templates.CartSummary{} // Use empty summary to avoid nil pointer
	}

	// Save transaction
	GlobalPaymentEventLogger.LogPaymentEvent(
		paymentLinkID,
		PaymentEventSuccess,
		"qr",
		services.AppState.CurrentCart,
		summary,
		paymentLinkStatus.CustomerEmail,
	)

	// Clean up state and clear cart in one operation
	GlobalPaymentStateManager.RemovePaymentAndClearCart(paymentLinkID)

	// Determine if we have contact info (customer email)
	hasContactInfo := paymentLinkStatus.CustomerEmail != ""

	component := checkout.PaymentSuccess(paymentLinkID, hasContactInfo)
	return PaymentStatusResult{
		Status:     "completed",
		Completed:  true,
		Component:  component,
		ShouldStop: true,
	}
}

func handleTerminalPaymentSuccess(
	intentID string,
	terminalState *TerminalPaymentState,
	intent *stripe.PaymentIntent,
) PaymentStatusResult {
	log.Printf("Terminal payment %s completed successfully", intentID)

	// Save transaction
	GlobalPaymentEventLogger.LogPaymentEventFromState(terminalState, PaymentEventSuccess, "")

	// Create success component that replaces the entire modal
	component := checkout.PaymentSuccess(intentID, terminalState.Email != "")

	// Send success via SSE to replace entire modal content - this removes the SSE container
	log.Printf("[SSE] Sending payment success for: %s", intentID)

	// Create a custom SSE event that targets the modal content instead of status details
	GlobalSSEBroadcaster.BroadcastModalUpdate(intentID, component)

	// Clean up state and SSE connection
	GlobalPaymentStateManager.RemovePaymentAndClearCart(intentID)
	GlobalSSEBroadcaster.RemoveConnection(intentID)

	return PaymentStatusResult{
		Status:     "completed",
		Completed:  true,
		Component:  component,
		ShouldStop: true,
	}
}

func handleTerminalPaymentTimeout(intentID string, intent *stripe.PaymentIntent) PaymentStatusResult {
	log.Printf("Terminal payment %s timed out after %v", intentID, PAYMENT_POLLING_TIMEOUT)

	state, _ := GlobalPaymentStateManager.GetPayment(intentID)
	terminalState := state.(*TerminalPaymentState)

	// Log transaction as expired
	GlobalPaymentEventLogger.LogPaymentEventFromState(terminalState, PaymentEventExpired, "")

	// Clean up state
	GlobalPaymentStateManager.RemovePayment(intentID)

	component := checkout.TerminalInteractionResultModal(
		"Payment Timed Out",
		"Customer did not present payment method within 120 seconds.",
		intentID,
		true, // hasCloseButton
		"",   // no additional message
	)
	return PaymentStatusResult{
		Status:     "expired",
		Failed:     true,
		Component:  component,
		ShouldStop: true,
	}
}

func handleTerminalPaymentFailure(intentID string, intent *stripe.PaymentIntent) PaymentStatusResult {
	log.Printf("Terminal payment %s failed with status: %s", intentID, intent.Status)

	state, _ := GlobalPaymentStateManager.GetPayment(intentID)
	terminalState := state.(*TerminalPaymentState)

	// Create failure message
	failureMessage := "Payment failed"
	if intent.LastPaymentError != nil && intent.LastPaymentError.Msg != "" {
		failureMessage = intent.LastPaymentError.Msg
	}

	// Log transaction as failed using the unified event logger
	GlobalPaymentEventLogger.LogPaymentEventFromState(terminalState, PaymentEventFailed, "")

	// Clean up state
	GlobalPaymentStateManager.RemovePayment(intentID)

	component := checkout.PaymentDeclinedModal(failureMessage, intentID)
	return PaymentStatusResult{
		Status:     "failed",
		Failed:     true,
		Component:  component,
		ShouldStop: true,
	}
}
