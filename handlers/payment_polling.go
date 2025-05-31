package handlers

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/paymentintent"
	"github.com/stripe/stripe-go/v74/paymentlink"
	"github.com/stripe/stripe-go/v74/terminal/reader"

	"checkout/services"
	"checkout/templates"
	"checkout/templates/checkout"
)

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
const PAYMENT_POLLING_TIMEOUT = PAYMENT_TIMEOUT

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
// Now returns raw HTML that templates can embed
func createPaymentProgressComponentWithOptions(opts PaymentProgressOptions) templ.Component {
	// Determine the status message
	statusMessage := GetPaymentMessage(opts.PaymentType, "default")
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

	// Generate the unified progress HTML using our centralized constants
	progressHTML := fmt.Sprintf(`
		<div class="payment-progress %s-progress">
			<h4>%s in Progress</h4>
			<p>%s</p>
			<p>Payment expires in <span id="countdown">%d</span> seconds</p>
			<div class="progress-bar">
				<div class="progress-fill" style="width: %.1f%%;"></div>
			</div>
			%s
		</div>`,
		opts.PaymentType, 
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

	// Continue polling - render progress component
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

	// Check payment status
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
// checkTerminalPaymentStatus checks terminal payment status
func checkTerminalPaymentStatus(intentID string) PaymentStatusResult {
	state, exists := GlobalPaymentStateManager.GetPayment(intentID)
	if !exists {
		return PaymentStatusResult{
			Status:     "error",
			Failed:     true,
			Message:    "Payment session not found",
			ShouldStop: true,
		}
	}

	terminalState := state.(*TerminalPaymentState)
	progress := calculateProgressInfo(state.GetStartTime(), PAYMENT_POLLING_TIMEOUT)

	// Check for timeout
	if progress.SecondsRemaining <= 0 {
		return handleTerminalPaymentTimeout(intentID)
	}

	// Check payment intent status
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

	// Handle completed payment
	if intent.Status == stripe.PaymentIntentStatusSucceeded {
		return handleTerminalPaymentSuccess(intentID, terminalState, intent)
	}

	// Handle failed payment
	if intent.Status == stripe.PaymentIntentStatusCanceled ||
		intent.Status == stripe.PaymentIntentStatusRequiresPaymentMethod {
		return handleTerminalPaymentFailure(intentID, intent)
	}

	// Continue polling - render progress using our enhanced reusable function
	var statusMessage string
	if intent.NextAction != nil &&
		intent.NextAction.Type == stripe.PaymentIntentNextActionType("display_terminal_receipt") {
		statusMessage = "Please take your receipt from the terminal."
	} else {
		statusMessage = fmt.Sprintf("Processing on terminal... (Status: %s)", intent.Status)
	}

	options := PaymentProgressOptions{
		PaymentID:     intentID,
		PaymentType:   "terminal",
		Progress:      progress,
		StatusMessage: statusMessage,
		ReaderID:      terminalState.ReaderID,
		PaymentStatus: string(intent.Status),
	}
	component := createPaymentProgressComponentWithOptions(options)
	return PaymentStatusResult{
		Status:     "pending",
		Component:  component,
		ShouldStop: false,
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
	GlobalPaymentEventLogger.LogPaymentEvent(paymentLinkID, PaymentEventSuccess, "qr", services.AppState.CurrentCart, summary, paymentLinkStatus.CustomerEmail)

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

// Helper functions for terminal payment handling
func handleTerminalPaymentTimeout(intentID string) PaymentStatusResult {
	log.Printf("Terminal payment %s timed out after %v", intentID, PAYMENT_POLLING_TIMEOUT)

	state, _ := GlobalPaymentStateManager.GetPayment(intentID)
	terminalState := state.(*TerminalPaymentState)

	// Cancel the payment intent
	_, err := paymentintent.Cancel(intentID, nil)
	if err != nil {
		log.Printf("Error canceling payment intent %s: %v", intentID, err)
	}

	// Cancel reader action if possible
	if terminalState.ReaderID != "" {
		_, err := reader.CancelAction(terminalState.ReaderID, nil)
		if err != nil {
			log.Printf("Error canceling reader action for %s: %v", terminalState.ReaderID, err)
		}
	}

	// Log transaction as expired
	GlobalPaymentEventLogger.LogPaymentEventFromState(terminalState, PaymentEventExpired, "")

	// Clean up state
	GlobalPaymentStateManager.RemovePayment(intentID)

	component := checkout.PaymentExpired(intentID)
	return PaymentStatusResult{
		Status:     "expired",
		Expired:    true,
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

	// Clean up state and clear cart in one operation
	GlobalPaymentStateManager.RemovePaymentAndClearCart(intentID)

	component := checkout.PaymentSuccess(intentID, terminalState.Email != "")
	return PaymentStatusResult{
		Status:     "completed",
		Completed:  true,
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
