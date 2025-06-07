package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/paymentintent"
	"github.com/stripe/stripe-go/v74/terminal/reader"

	"checkout/services"
	"checkout/templates"
	"checkout/templates/checkout"
)

// TerminalProcessingResult represents the result of terminal payment processing
type TerminalProcessingResult struct {
	Success        bool
	PaymentSuccess bool
	ShouldStop     bool
	UpdatedIntent  *stripe.PaymentIntent
	Message        string
}

// ProcessTerminalPayment handles all terminal-specific payment processing logic
func ProcessTerminalPayment(w http.ResponseWriter, r *http.Request, intent *stripe.PaymentIntent, email string, summary templates.CartSummary) TerminalProcessingResult {
	// Find an online terminal reader
	selectedReaderID := findOnlineTerminalReader()
	if selectedReaderID == "" {
		log.Println("Error processing terminal payment: No online Stripe Terminal reader found.")
		if renderErr := renderErrorModal(w, r, 
			"No online terminal reader available. Please check reader status or select a different payment method.", 
			intent.ID); renderErr != nil {
			log.Printf("Error rendering no reader available modal: %v", renderErr)
		}
		return TerminalProcessingResult{
			Success:    false,
			ShouldStop: true,
			Message:    "No online terminal reader found",
		}
	}

	// Process payment on the terminal reader
	processedReader, err := processPaymentOnTerminal(intent.ID, selectedReaderID, summary)
	if err != nil {
		log.Printf("Error commanding reader %s to process PaymentIntent %s: %v", selectedReaderID, intent.ID, err)
		errMsg := "Error communicating with the payment terminal."
		if stripeErr, ok := err.(*stripe.Error); ok {
			errMsg = fmt.Sprintf("Terminal communication error: %s", stripeErr.Msg)
		}
		if renderErr := renderErrorModal(w, r, errMsg, intent.ID); renderErr != nil {
			log.Printf("Error rendering terminal communication error modal: %v", renderErr)
		}
		return TerminalProcessingResult{
			Success:    false,
			ShouldStop: true,
			Message:    "Terminal communication error",
		}
	}

	// Handle terminal processing result
	return handleTerminalActionResult(w, r, intent, selectedReaderID, processedReader, email, summary)
}

// findOnlineTerminalReader finds an online terminal reader
func findOnlineTerminalReader() string {
	if len(services.AppState.SiteStripeReaders) == 0 {
		return ""
	}

	for _, reader := range services.AppState.SiteStripeReaders {
		if reader.Status == "online" {
			log.Printf("Selected online terminal reader: %s (Label: %s)", reader.ID, reader.Label)
			return reader.ID
		}
	}
	return ""
}

// processPaymentOnTerminal processes payment intent on a terminal reader
// with tipping configuration based on business rules
func processPaymentOnTerminal(intentID, readerID string, summary templates.CartSummary) (*stripe.TerminalReader, error) {
	// Determine if tipping should be enabled for this transaction
	shouldEnableTipping := services.ShouldEnableTipping(
		summary.Total,
		services.AppState.CurrentCart,
		services.AppState.SelectedStripeLocation.ID,
	)

	readerParams := &stripe.TerminalReaderProcessPaymentIntentParams{
		PaymentIntent: stripe.String(intentID),
		ProcessConfig: &stripe.TerminalReaderProcessPaymentIntentProcessConfigParams{
			SkipTipping: stripe.Bool(!shouldEnableTipping), // Skip tipping if business rules say no
		},
	}

	log.Printf("Attempting to process PaymentIntent %s on reader %s with tipping=%v (amount=%.2f)", 
		intentID, readerID, shouldEnableTipping, summary.Total)
	return reader.ProcessPaymentIntent(readerID, readerParams)
}

// handleTerminalActionResult handles the result of a terminal reader action
func handleTerminalActionResult(w http.ResponseWriter, r *http.Request, intent *stripe.PaymentIntent, 
	selectedReaderID string, processedReader *stripe.TerminalReader, email string, summary templates.CartSummary) TerminalProcessingResult {
	
	if processedReader == nil || processedReader.Action == nil {
		log.Printf(
			"Unexpected nil reader or action after ProcessPaymentIntent for PI %s on reader %s.",
			intent.ID,
			selectedReaderID,
		)
		errMsg := "An unexpected error occurred with the terminal. Payment status is unclear."
		if renderErr := renderErrorModal(w, r, errMsg, intent.ID); renderErr != nil {
			log.Printf("Error rendering nil action/reader modal: %v", renderErr)
		}
		return TerminalProcessingResult{
			Success:    false,
			ShouldStop: true,
			Message:    "Unexpected terminal state",
		}
	}

	log.Printf("Reader %s action status for PI %s: %s", selectedReaderID, intent.ID, processedReader.Action.Status)

	switch processedReader.Action.Status {
	case stripe.TerminalReaderActionStatusSucceeded:
		return handleTerminalSuccess(w, r, intent, processedReader)

	case stripe.TerminalReaderActionStatusFailed:
		return handleTerminalFailure(w, r, intent, processedReader)

	case stripe.TerminalReaderActionStatusInProgress:
		return handleTerminalInProgress(w, r, intent, selectedReaderID, email, summary)

	default:
		log.Printf("Unexpected terminal reader action status: %s for PI %s", processedReader.Action.Status, intent.ID)
		errMsg := fmt.Sprintf("Unexpected terminal status: %s", processedReader.Action.Status)
		if renderErr := renderErrorModal(w, r, errMsg, intent.ID); renderErr != nil {
			log.Printf("Error rendering unexpected status modal: %v", renderErr)
		}
		return TerminalProcessingResult{
			Success:    false,
			ShouldStop: true,
			Message:    "Unexpected terminal status",
		}
	}
}

// handleTerminalSuccess handles successful terminal payment
func handleTerminalSuccess(w http.ResponseWriter, r *http.Request, intent *stripe.PaymentIntent, processedReader *stripe.TerminalReader) TerminalProcessingResult {
	pi := processedReader.Action.ProcessPaymentIntent.PaymentIntent
	if pi == nil {
		log.Printf("PaymentIntent is nil within successful reader action for PI %s.", intent.ID)
		errMsg := "Payment confirmation missing after successful terminal interaction."
		if renderErr := renderErrorModal(w, r, errMsg, intent.ID); renderErr != nil {
			log.Printf("Error rendering PI nil in action modal: %v", renderErr)
		}
		return TerminalProcessingResult{
			Success:    false,
			ShouldStop: true,
			Message:    "Payment confirmation missing",
		}
	}

	log.Printf("Terminal PaymentIntent %s final status: %s", pi.ID, pi.Status)
	if pi.Status == stripe.PaymentIntentStatusSucceeded {
		log.Printf("PaymentIntent %s Succeeded on terminal reader.", intent.ID)
		return TerminalProcessingResult{
			Success:        true,
			PaymentSuccess: true,
			ShouldStop:     true,
			UpdatedIntent:  pi,
			Message:        "Payment succeeded",
		}
	} else {
		declineMessage := "Payment declined by terminal."
		if pi.LastPaymentError != nil && pi.LastPaymentError.Msg != "" {
			declineMessage = fmt.Sprintf("Payment declined: %s", pi.LastPaymentError.Msg)
		}
		log.Printf("PaymentIntent %s not successful. Status: %s. Decline: %s", pi.ID, string(pi.Status), declineMessage)
		if renderErr := renderErrorModal(w, r, declineMessage, pi.ID); renderErr != nil {
			log.Printf("Error rendering payment declined (reader success, PI fail) modal: %v", renderErr)
		}
		return TerminalProcessingResult{
			Success:    false,
			ShouldStop: true,
			Message:    "Payment declined",
		}
	}
}

// handleTerminalFailure handles failed terminal payment
func handleTerminalFailure(w http.ResponseWriter, r *http.Request, intent *stripe.PaymentIntent, processedReader *stripe.TerminalReader) TerminalProcessingResult {
	errMsg := "Payment failed at terminal."
	if processedReader.Action.FailureMessage != "" {
		errMsg = fmt.Sprintf("Terminal error: %s", processedReader.Action.FailureMessage)
	}
	log.Printf(
		"Terminal reader action failed for PI %s. Reason: %s (Code: %s)",
		intent.ID,
		processedReader.Action.FailureMessage,
		processedReader.Action.FailureCode,
	)
	if renderErr := renderErrorModal(w, r, errMsg, intent.ID); renderErr != nil {
		log.Printf("Error rendering reader action failed modal: %v", renderErr)
	}
	return TerminalProcessingResult{
		Success:    false,
		ShouldStop: true,
		Message:    "Terminal action failed",
	}
}

// handleTerminalInProgress handles in-progress terminal payment (sets up polling)
func handleTerminalInProgress(w http.ResponseWriter, r *http.Request, intent *stripe.PaymentIntent, 
	selectedReaderID, email string, summary templates.CartSummary) TerminalProcessingResult {
	
	log.Printf(
		"Terminal payment for PI %s on reader %s is InProgress. Switching to polling.",
		intent.ID,
		selectedReaderID,
	)

	// Store the active payment details for polling handlers
	terminalState := &TerminalPaymentState{
		PaymentIntentID: intent.ID,
		ReaderID:        selectedReaderID,
		StartTime:       time.Now(),
		Email:           email,
		Cart:            make([]templates.Service, len(services.AppState.CurrentCart)),
		Summary:         summary,
	}
	copy(terminalState.Cart, services.AppState.CurrentCart)
	GlobalPaymentStateManager.AddPayment(terminalState)

	// Render terminal payment container with SSE support
	component := checkout.TerminalPaymentContainer(
		intent.ID,
		selectedReaderID,
		float64(intent.Amount)/100.0, // Convert from cents to dollars
		email,
	)
	if renderErr := renderInfoModal(w, r, component); renderErr != nil {
		log.Printf("Error rendering terminal payment progress modal: %v", renderErr)
	}

	return TerminalProcessingResult{
		Success:    true,
		ShouldStop: true, // Stop processing in main handler, polling will take over
		Message:    "Terminal polling initiated",
	}
}

// CheckTerminalPaymentStatusHandler handles checking the status of terminal payments
func CheckTerminalPaymentStatusHandler(w http.ResponseWriter, r *http.Request) {
	config := PaymentPollingConfig{
		PaymentType:     "terminal",
		TimeoutDuration: PAYMENT_POLLING_TIMEOUT,
	}
	checkPaymentStatusGeneric(w, r, config)
}

// CancelTerminalPaymentHandler handles user-initiated cancellation of a terminal payment
func CancelTerminalPaymentHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}
	paymentIntentID := r.FormValue("payment_intent_id")

	if paymentIntentID == "" {
		http.Error(w, "PaymentIntent ID is required for cancellation", http.StatusBadRequest)
		return
	}

	state, found := GlobalPaymentStateManager.GetPayment(paymentIntentID)
	if !found {
		log.Printf(
			"CancelTerminalPaymentHandler: PI %s not found in active states. Already concluded?",
			paymentIntentID,
		)
		// Render a message indicating it's already done.
		component := checkout.TerminalInteractionResultModal(
			"Cancellation Request",
			"This payment session is no longer active or has already concluded.",
			paymentIntentID,
			true,
			"",
		)
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("HX-Retarget", "#modal-content")
		component.Render(r.Context(), w)
		return
	}

	// Convert to terminal state
	terminalState, ok := state.(*TerminalPaymentState)
	if !ok {
		log.Printf("CancelTerminalPaymentHandler: PI %s is not a terminal payment", paymentIntentID)
		http.Error(w, "Invalid payment type", http.StatusBadRequest)
		return
	}

	// Try to cancel the reader action first (if reader is still available)
	_, err := reader.CancelAction(terminalState.ReaderID, &stripe.TerminalReaderCancelActionParams{})
	if err != nil {
		var stripeErr *stripe.Error
		if errors.As(err, &stripeErr) && stripeErr.Code == stripe.ErrorCode("terminal_reader_action_not_allowed") {
			log.Printf(
				"CancelTerminalPaymentHandler: Reader action not allowed for PI %s (Reader %s) - may already be completed. %v",
				paymentIntentID,
				terminalState.ReaderID,
				err,
			)
		} else {
			log.Printf("CancelTerminalPaymentHandler: Error cancelling reader action for PI %s (Reader %s): %v", paymentIntentID, terminalState.ReaderID, err)
		}
	} else {
		log.Printf("CancelTerminalPaymentHandler: Successfully sent cancel action to reader %s for PI %s.", terminalState.ReaderID, paymentIntentID)
	}

	// Regardless of reader cancellation success, attempt to cancel the Payment Intent
	pi, cancelErr := paymentintent.Cancel(paymentIntentID, nil)
	if cancelErr != nil {
		log.Printf("CancelTerminalPaymentHandler: Error cancelling PaymentIntent %s: %v", paymentIntentID, cancelErr)
		// Even if cancellation fails, clean up our state and render a message
		GlobalPaymentStateManager.RemovePayment(paymentIntentID)
		component := checkout.TerminalInteractionResultModal(
			"Cancellation Error",
			fmt.Sprintf("Could not cancel payment %s. It may have already completed or failed.", paymentIntentID),
			paymentIntentID,
			true,
			"",
		)
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("HX-Retarget", "#modal-content")
		component.Render(r.Context(), w)
		return
	}

	log.Printf("CancelTerminalPaymentHandler: Successfully cancelled PaymentIntent %s. Status: %s", pi.ID, pi.Status)

	// Log the cancellation
	GlobalPaymentEventLogger.LogPaymentEventFromState(terminalState, PaymentEventCancelled, "")

	// Clean up state
	GlobalPaymentStateManager.RemovePayment(paymentIntentID)

	// Render success message
	component := checkout.TerminalInteractionResultModal(
		"Payment Cancelled",
		fmt.Sprintf("Payment %s has been successfully cancelled.", paymentIntentID),
		paymentIntentID,
		true,
		"",
	)
	w.Header().Set("HX-Reswap", "innerHTML")
	w.Header().Set("HX-Retarget", "#modal-content")
	component.Render(r.Context(), w)
}

// ExpireTerminalPaymentHandler handles automatic expiration of terminal payments
func ExpireTerminalPaymentHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	paymentIntentID := r.FormValue("payment_intent_id")
	if paymentIntentID == "" {
		http.Error(w, "PaymentIntent ID is required for expiration", http.StatusBadRequest)
		return
	}

	state, found := GlobalPaymentStateManager.GetPayment(paymentIntentID)
	if !found {
		log.Printf(
			"ExpireTerminalPaymentHandler: PI %s not found in active states. Already concluded?",
			paymentIntentID,
		)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Payment session not found or already concluded"))
		return
	}

	// Convert to terminal state
	terminalState, ok := state.(*TerminalPaymentState)
	if !ok {
		log.Printf("ExpireTerminalPaymentHandler: PI %s is not a terminal payment", paymentIntentID)
		http.Error(w, "Invalid payment type", http.StatusBadRequest)
		return
	}

	// Try to cancel the reader action and payment intent (same logic as cancellation)
	_, err := reader.CancelAction(terminalState.ReaderID, &stripe.TerminalReaderCancelActionParams{})
	if err != nil {
		log.Printf("ExpireTerminalPaymentHandler: Error cancelling reader action for PI %s: %v", paymentIntentID, err)
	}

	_, cancelErr := paymentintent.Cancel(paymentIntentID, nil)
	if cancelErr != nil {
		log.Printf("ExpireTerminalPaymentHandler: Error cancelling PaymentIntent %s: %v", paymentIntentID, cancelErr)
	}

	// Log the expiration
	GlobalPaymentEventLogger.LogPaymentEventFromState(terminalState, PaymentEventExpired, "")

	// Clean up state
	GlobalPaymentStateManager.RemovePayment(paymentIntentID)

	log.Printf("ExpireTerminalPaymentHandler: Payment %s expired and cleaned up", paymentIntentID)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Payment expired"))
}


