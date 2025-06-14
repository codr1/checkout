package handlers

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"checkout/config"
	"checkout/services"
	"checkout/templates/checkout"
	"checkout/utils"

	"github.com/a-h/templ"
	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/paymentintent"
	"github.com/stripe/stripe-go/v74/paymentlink"
	"github.com/stripe/stripe-go/v74/terminal/reader"
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
	b.mutex.Lock()
	defer b.mutex.Unlock()

	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil
	}

	conn := &SSEConnection{
		Writer:    w,
		Flusher:   flusher,
		PaymentID: paymentID,
		Type:      paymentType,
		Done:      make(chan bool, 1),
	}

	b.connections[paymentID] = conn
	utils.Debug("sse", "New connection established", "payment_type", paymentType, "payment_id", paymentID)
	return conn
}

// RemoveConnection removes an SSE connection
func (b *SSEBroadcaster) RemoveConnection(paymentID string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if conn, exists := b.connections[paymentID]; exists {
		close(conn.Done)
		delete(b.connections, paymentID)
		utils.Debug("sse", "Connection removed", "payment_id", paymentID)
	}
}

// BroadcastPaymentUpdate sends a payment update to relevant SSE connections
func (b *SSEBroadcaster) BroadcastPaymentUpdate(paymentID string, component templ.Component) {
	b.mutex.RLock()
	conn, exists := b.connections[paymentID]
	b.mutex.RUnlock()

	if !exists {
		utils.Info("sse", "No connection found for payment", "payment_id", paymentID)
		return
	}

	// Render the component to HTML
	html, err := templ.ToGoHTML(context.Background(), component)
	if err != nil {
		utils.Error("sse", "Error rendering component", "payment_id", paymentID, "error", err)
		return
	}

	// Write SSE event
	if _, err := fmt.Fprint(conn.Writer, "event: payment-update\n"); err != nil {
		utils.Error("sse", "Error writing SSE event header", "error", err)
		return
	}
	if _, err := fmt.Fprintf(conn.Writer, "data: %s\n\n", html); err != nil {
		utils.Error("sse", "Error writing SSE data", "error", err)
		return
	}

	conn.Flusher.Flush()
	utils.Debug("sse", "Payment update sent", "payment_id", paymentID)
}

// BroadcastModalUpdate sends a payment update that replaces the entire modal content
func (b *SSEBroadcaster) BroadcastModalUpdate(paymentID string, component templ.Component) {
	b.mutex.RLock()
	conn, exists := b.connections[paymentID]
	b.mutex.RUnlock()

	if !exists {
		utils.Debug("sse", "No connection found for modal update", "payment_id", paymentID)
		return
	}

	// Render the component to HTML
	html, err := templ.ToGoHTML(context.Background(), component)
	if err != nil {
		utils.Error("sse", "Error rendering component", "payment_id", paymentID, "error", err)
		return
	}

	// Write SSE event for modal update
	if _, err := fmt.Fprint(conn.Writer, "event: modal-update\n"); err != nil {
		utils.Error("sse", "Error writing modal-update event header", "error", err)
		return
	}
	if _, err := fmt.Fprintf(conn.Writer, "data: %s\n\n", html); err != nil {
		utils.Error("sse", "Error writing SSE data", "error", err)
		return
	}

	conn.Flusher.Flush()
	utils.Debug("sse", "Modal update sent", "payment_id", paymentID)
}

// PaymentSSEHandler handles SSE connections for payment updates
func PaymentSSEHandler(w http.ResponseWriter, r *http.Request) {
	paymentID := r.URL.Query().Get("payment_id")
	paymentType := r.URL.Query().Get("type") // "qr" or "terminal"

	utils.Debug("sse", "New connection request", "payment_type", paymentType, "payment_id", paymentID)

	if paymentID == "" || paymentType == "" {
		utils.Warn("sse", "Missing required parameters", "payment_id", paymentID, "type", paymentType)
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
		utils.Error("sse", "Failed to add connection", "payment_id", paymentID, "reason", "SSE not supported by client")
		http.Error(w, "SSE not supported by client", http.StatusInternalServerError)
		return
	}

	utils.Debug("sse", "Connection established successfully", "payment_type", paymentType, "payment_id", paymentID)

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

// SSE functions removed - architecture uses event-driven completion notifications only
// Progress display is handled by client-side JavaScript countdown
// Completion events are triggered by webhook handlers

// handleSSETimeout handles payment timeout via SSE
func handleSSETimeout(paymentID, paymentType string) {
	switch paymentType {
	case "qr":
		// QR timeout handler does its own BroadcastModalUpdate() + RemoveConnection()
		handleQRPaymentTimeout(paymentID)
	case "terminal":
		// Fetch the real PaymentIntent from Stripe
		intent, err := paymentintent.Get(paymentID, nil)
		if err != nil {
			utils.Error("payment", "Error fetching PaymentIntent for timeout handling", "payment_id", paymentID, "error", err)
			// If we can't fetch it, create a minimal intent for cleanup
			intent = &stripe.PaymentIntent{
				ID:     paymentID,
				Status: stripe.PaymentIntentStatusRequiresPaymentMethod,
			}
		}
		// Terminal timeout handler does its own BroadcastModalUpdate() + RemoveConnection()
		handleTerminalPaymentTimeout(paymentID, intent)
	}
}

// PaymentStatusResult represents the result of checking payment status
type PaymentStatusResult struct {
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

	// Generate the progress HTML with stop-polling trigger when final state reached
	var stopPollingAttr string
	if opts.Progress.SecondsRemaining <= 0 {
		// Payment has timed out, stop polling
		stopPollingAttr = `hx-trigger="none"`
	}

	// Generate the progress HTML (single line to avoid newline issues in SSE)
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
func calculateProgressInfo(creationTime time.Time, _ time.Duration) ProgressInfo {
	elapsed := time.Since(creationTime)
	remaining := PAYMENT_POLLING_TIMEOUT - elapsed

	secondsRemaining := int(remaining.Seconds())
	if secondsRemaining < 0 {
		secondsRemaining = 0
	}

	progressWidth := (elapsed.Seconds() / PAYMENT_POLLING_TIMEOUT.Seconds()) * 100
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
		switch config.PaymentType {
		case "qr":
			paymentID = r.URL.Query().Get("payment_link_id")
		case "terminal":
			paymentID = r.URL.Query().Get("intent_id")
		}
	}

	if paymentID == "" {
		if _, err := w.Write([]byte(`<p class="status-message">Waiting for payment information...</p>`)); err != nil {
			utils.Error("http", "Error writing waiting response", "error", err)
		}
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
				utils.Error("http", "Error rendering payment result component", "error", err)
			}
		} else {
			if _, err := w.Write([]byte(result.Message)); err != nil {
				utils.Error("http", "Error writing result message", "error", err)
			}
		}
		return
	}

	// Continue polling - render progress component with updated countdown/progress
	if result.Component != nil {
		w.WriteHeader(http.StatusOK)
		if err := result.Component.Render(r.Context(), w); err != nil {
			utils.Error("http", "Error rendering payment progress component", "error", err)
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
		utils.Debug("payment", "Using cached state for QR payment link", "payment_link_id", paymentLinkID, "status", cachedState.Status)

		// Handle cached payment completion
		if cachedState.Status == "completed" {
			paymentLinkStatus := services.PaymentLinkStatus{
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
	utils.Debug("payment", "No cached state found, checking Stripe API", "payment_link_id", paymentLinkID)
	paymentLinkStatus, err := services.CheckPaymentLinkStatus(paymentLinkID)
	if err != nil {
		utils.Error("payment", "Error checking payment link status", "payment_link_id", paymentLinkID, "error", err)
		return PaymentStatusResult{
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
		Component:  component,
		ShouldStop: false,
	}
}

// checkTerminalPaymentStatus checks terminal payment status
func checkTerminalPaymentStatus(intentID string) PaymentStatusResult {
	utils.Debug("payment", "Checking terminal payment status", "intent_id", intentID)
	state, exists := GlobalPaymentStateManager.GetPayment(intentID)
	if !exists {
		utils.Debug("payment", "No cached payment state found", "intent_id", intentID)
		// Payment session not found - render a final "session concluded" message
		component := checkout.TerminalInteractionResultModal(
			"Payment Session Concluded",
			"This payment session is no longer active.",
			intentID,
			true, // hasCloseButton
			"",   // no additional message
		)
		return PaymentStatusResult{
			Component:  component,
			ShouldStop: true,
		}
	}
	utils.Debug("payment", "Found cached payment state", "intent_id", intentID)

	terminalState := state.(*TerminalPaymentState)
	progress := calculateProgressInfo(state.GetStartTime(), PAYMENT_POLLING_TIMEOUT)

	// Check for timeout
	if progress.SecondsRemaining <= 0 {
		// Fetch the real PaymentIntent to see its actual status
		intent, err := paymentintent.Get(intentID, nil)
		if err != nil {
			utils.Error("payment", "Error fetching PaymentIntent for timeout handling", "intent_id", intentID, "error", err)
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
		utils.Debug("payment", "Using cached webhook state", "intent_id", intentID, "status", cachedState.Status)

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
	utils.Debug("payment", "No cached webhook state found, checking Stripe API", "intent_id", intentID)
	intent, err := paymentintent.Get(intentID, nil)
	if err != nil {
		utils.Error("payment", "Error fetching PaymentIntent", "intent_id", intentID, "error", err)
		return PaymentStatusResult{
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
			Component: component,
		}

	default:
		utils.Warn("payment", "Unknown PaymentIntent status for terminal payment", "intent_id", intentID, "status", intent.Status)
		return PaymentStatusResult{
			Message:    fmt.Sprintf("Unknown payment status: %s", intent.Status),
			ShouldStop: true,
		}
	}
}

// Helper functions for QR payment handling
func handleQRPaymentTimeout(paymentLinkID string) PaymentStatusResult {
	utils.Info("payment", "Payment link timed out", "payment_link_id", paymentLinkID, "timeout", PAYMENT_POLLING_TIMEOUT)

	// Deactivate the payment link
	_, err := paymentlink.Update(paymentLinkID, &stripe.PaymentLinkParams{
		Active: stripe.Bool(false),
	})
	if err != nil {
		utils.Error("payment", "Error deactivating payment link", "payment_link_id", paymentLinkID, "error", err)
	}

	// Log transaction as expired
	_ = GlobalPaymentEventLogger.LogPaymentEventQuick(paymentLinkID, PaymentEventExpired, "qr")

	// Create timeout component that replaces the entire modal
	component := checkout.PaymentExpired(paymentLinkID)

	// Send timeout via SSE to replace entire modal content - this removes the SSE container
	utils.Debug("sse", "Sending QR payment timeout", "payment_link_id", paymentLinkID)

	// Use modal update to replace entire modal content
	GlobalSSEBroadcaster.BroadcastModalUpdate(paymentLinkID, component)

	// Clean up state and SSE connection
	GlobalPaymentStateManager.RemovePayment(paymentLinkID)
	GlobalSSEBroadcaster.RemoveConnection(paymentLinkID)

	return PaymentStatusResult{
		Component:  component,
		ShouldStop: true,
	}
}

func handleQRPaymentSuccess(paymentLinkID string, paymentLinkStatus services.PaymentLinkStatus) PaymentStatusResult {
	utils.Info("payment", "Payment link completed successfully", "payment_link_id", paymentLinkID)

	// Calculate cart summary for transaction record
	summary := services.CalculateCartSummary()

	// Save transaction
	_ = GlobalPaymentEventLogger.LogPaymentEvent(
		paymentLinkID,
		PaymentEventSuccess,
		"qr",
		services.AppState.CurrentCart,
		summary,
		paymentLinkStatus.CustomerEmail,
	)

	// Determine if we have contact info (customer email)
	hasContactInfo := paymentLinkStatus.CustomerEmail != ""

	// Create success component that replaces the entire modal
	component := checkout.PaymentSuccess(paymentLinkID, hasContactInfo)

	// Send success via SSE to replace entire modal content - this removes the SSE container
	utils.Debug("sse", "Sending QR payment success", "payment_link_id", paymentLinkID)

	// Use modal update to replace entire modal content (same as terminal)
	GlobalSSEBroadcaster.BroadcastModalUpdate(paymentLinkID, component)

	// Clean up state and SSE connection
	GlobalPaymentStateManager.RemovePaymentAndClearCart(paymentLinkID)
	GlobalSSEBroadcaster.RemoveConnection(paymentLinkID)

	return PaymentStatusResult{
		Component:  component,
		ShouldStop: true,
	}
}

func handleTerminalPaymentSuccess(
	intentID string,
	terminalState *TerminalPaymentState,
	_ *stripe.PaymentIntent,
) PaymentStatusResult {
	utils.Info("payment", "Terminal payment completed successfully", "intent_id", intentID)

	// Save transaction
	_ = GlobalPaymentEventLogger.LogPaymentEventFromState(terminalState, PaymentEventSuccess, "")

	// Create success component that replaces the entire modal
	component := checkout.PaymentSuccess(intentID, terminalState.Email != "")

	// Send success via SSE to replace entire modal content - this removes the SSE container
	utils.Debug("sse", "Sending payment success", "intent_id", intentID)

	// Create a custom SSE event that targets the modal content instead of status details
	GlobalSSEBroadcaster.BroadcastModalUpdate(intentID, component)

	// Clean up state and SSE connection
	GlobalPaymentStateManager.RemovePaymentAndClearCart(intentID)
	GlobalSSEBroadcaster.RemoveConnection(intentID)

	return PaymentStatusResult{
		Component:  component,
		ShouldStop: true,
	}
}

func handleTerminalPaymentTimeout(intentID string, _ *stripe.PaymentIntent) PaymentStatusResult {
	utils.Info("payment", "Terminal payment timed out", "intent_id", intentID, "timeout", PAYMENT_POLLING_TIMEOUT)

	state, _ := GlobalPaymentStateManager.GetPayment(intentID)
	terminalState := state.(*TerminalPaymentState)

	// Log transaction as expired
	_ = GlobalPaymentEventLogger.LogPaymentEventFromState(terminalState, PaymentEventExpired, "")

	// Create timeout component that replaces the entire modal
	component := checkout.TerminalInteractionResultModal(
		"Payment Timed Out",
		fmt.Sprintf("Customer did not present payment method within %.0f seconds.", config.PaymentTimeout.Seconds()),
		intentID,
		true, // hasCloseButton
		"",   // no additional message
	)

	// Send timeout via SSE to replace entire modal content - this removes the SSE container
	utils.Debug("sse", "Sending terminal payment timeout", "intent_id", intentID)

	// Use modal update to replace entire modal content
	GlobalSSEBroadcaster.BroadcastModalUpdate(intentID, component)

	// Clean up state and SSE connection
	GlobalPaymentStateManager.RemovePayment(intentID)
	GlobalSSEBroadcaster.RemoveConnection(intentID)

	return PaymentStatusResult{
		Component:  component,
		ShouldStop: true,
	}
}

func handleTerminalPaymentFailure(intentID string, intent *stripe.PaymentIntent) PaymentStatusResult {
	utils.Info("payment", "Terminal payment failed", "intent_id", intentID, "status", intent.Status)

	state, _ := GlobalPaymentStateManager.GetPayment(intentID)
	terminalState := state.(*TerminalPaymentState)

	// Create failure message
	failureMessage := "Payment failed"
	if intent.LastPaymentError != nil && intent.LastPaymentError.Msg != "" {
		failureMessage = intent.LastPaymentError.Msg
	}

	// Log transaction as failed
	_ = GlobalPaymentEventLogger.LogPaymentEventFromState(terminalState, PaymentEventFailed, "")

	// Create failure component that replaces the entire modal
	component := checkout.PaymentDeclinedModal(failureMessage, intentID)

	// Send failure via SSE to replace entire modal content - this removes the SSE container
	utils.Debug("sse", "Sending terminal payment failure", "intent_id", intentID)

	// Use modal update to replace entire modal content
	GlobalSSEBroadcaster.BroadcastModalUpdate(intentID, component)

	// Clean up state and SSE connection
	GlobalPaymentStateManager.RemovePayment(intentID)
	GlobalSSEBroadcaster.RemoveConnection(intentID)

	return PaymentStatusResult{
		Component:  component,
		ShouldStop: true,
	}
}

// GetPaymentStatusHandler - endpoint for checking payment status
// Used by failsafe timeout and can be used for any payment status check
func GetPaymentStatusHandler(w http.ResponseWriter, r *http.Request) {
	paymentType := r.URL.Query().Get("type")
	if paymentType == "" {
		paymentType = r.FormValue("type")
	}

	if paymentType == "" {
		http.Error(w, "type parameter required (qr or terminal)", http.StatusBadRequest)
		return
	}

	switch paymentType {
	case "qr":
		config := PaymentPollingConfig{
			PaymentType:     "qr",
			TimeoutDuration: PAYMENT_POLLING_TIMEOUT,
		}
		checkPaymentStatusGeneric(w, r, config)
	case "terminal":
		config := PaymentPollingConfig{
			PaymentType:     "terminal",
			TimeoutDuration: PAYMENT_POLLING_TIMEOUT,
		}
		checkPaymentStatusGeneric(w, r, config)
	default:
		http.Error(w, "invalid payment type, must be 'qr' or 'terminal'", http.StatusBadRequest)
	}
}

// CancelOrRefreshPaymentHandler - endpoint for cancel + hard refresh
// Cancels the payment server-side, then returns current state (like GetPaymentStatusHandler)
// Used by both cancel buttons and timeout handling for consistent behavior
func CancelOrRefreshPaymentHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	paymentID := r.FormValue("payment_id")
	paymentType := r.FormValue("type")

	// Also check URL parameters as fallback
	if paymentID == "" {
		paymentID = r.URL.Query().Get("payment_id")
	}
	if paymentType == "" {
		paymentType = r.URL.Query().Get("type")
	}

	if paymentID == "" || paymentType == "" {
		http.Error(w, "payment_id and type parameters required", http.StatusBadRequest)
		return
	}

	utils.Info("payment", "Starting cancel+refresh", "payment_type", paymentType, "payment_id", paymentID)

	// Step 1: Cancel the payment server-side
	cancelSuccess := cancelPaymentServerSide(paymentID, paymentType)
	if cancelSuccess {
		utils.Info("payment", "Successfully cancelled payment in cancel/refresh", "payment_type", paymentType, "payment_id", paymentID)
	} else {
		utils.Warn("payment", "Cancel attempt failed in cancel/refresh, continuing with refresh", "payment_type", paymentType, "payment_id", paymentID)
	}

	// Step 2: Return current state using existing hard refresh logic
	// This reuses the exact same logic as GetPaymentStatusHandler
	switch paymentType {
	case "qr":
		config := PaymentPollingConfig{
			PaymentType:     "qr",
			TimeoutDuration: PAYMENT_POLLING_TIMEOUT,
		}
		checkPaymentStatusGeneric(w, r, config)
	case "terminal":
		config := PaymentPollingConfig{
			PaymentType:     "terminal",
			TimeoutDuration: PAYMENT_POLLING_TIMEOUT,
		}
		checkPaymentStatusGeneric(w, r, config)
	default:
		http.Error(w, "invalid payment type, must be 'qr' or 'terminal'", http.StatusBadRequest)
	}
}

// cancelPaymentServerSide attempts to cancel a payment server-side
// Returns true if cancellation succeeded, false if it failed (but that's ok)
func cancelPaymentServerSide(paymentID, paymentType string) bool {
	switch paymentType {
	case "qr":
		return cancelQRPaymentServerSide(paymentID)
	case "terminal":
		return cancelTerminalPaymentServerSide(paymentID)
	default:
		utils.Warn("payment", "Unknown payment type in cancel operation", "payment_type", paymentType)
		return false
	}
}

// cancelQRPaymentServerSide cancels a QR payment link
func cancelQRPaymentServerSide(paymentLinkID string) bool {
	// Deactivate the payment link in Stripe
	_, err := paymentlink.Update(paymentLinkID, &stripe.PaymentLinkParams{Active: stripe.Bool(false)})
	if err != nil {
		utils.Error("payment", "Error cancelling QR payment link", "payment_link_id", paymentLinkID, "error", err)
		return false
	}

	// Log the cancellation
	_ = GlobalPaymentEventLogger.LogPaymentEventQuick(paymentLinkID, PaymentEventCancelled, "qr")

	utils.Info("payment", "Successfully cancelled QR payment link", "payment_link_id", paymentLinkID)
	return true
}

// cancelTerminalPaymentServerSide cancels a terminal payment
func cancelTerminalPaymentServerSide(paymentIntentID string) bool {
	state, found := GlobalPaymentStateManager.GetPayment(paymentIntentID)
	if !found {
		utils.Debug("payment", "Terminal payment not found in active states during cancel", "payment_intent_id", paymentIntentID)
		return false // Not found, but that's ok - might already be concluded
	}

	terminalState, ok := state.(*TerminalPaymentState)
	if !ok {
		utils.Error("payment", "Payment is not a terminal payment during cancel", "payment_id", paymentIntentID)
		return false
	}

	// Try to cancel the reader action first
	_, err := reader.CancelAction(terminalState.ReaderID, &stripe.TerminalReaderCancelActionParams{})
	if err != nil {
		utils.Warn("payment", "Error cancelling reader action", "payment_intent_id", paymentIntentID, "reader_id", terminalState.ReaderID, "error", err)
		// Continue anyway - try to cancel the payment intent
	}

	// Cancel the Payment Intent
	_, cancelErr := paymentintent.Cancel(paymentIntentID, nil)
	if cancelErr != nil {
		utils.Error("payment", "Error cancelling PaymentIntent", "payment_intent_id", paymentIntentID, "error", cancelErr)
		return false
	}

	// Log the cancellation
	_ = GlobalPaymentEventLogger.LogPaymentEventFromState(terminalState, PaymentEventCancelled, "")

	utils.Info("payment", "Successfully cancelled terminal payment", "payment_intent_id", paymentIntentID)
	return true
}
