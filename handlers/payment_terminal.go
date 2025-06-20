package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/terminal/reader"

	"checkout/services"
	"checkout/templates"
	"checkout/templates/checkout"
	"checkout/utils"
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
	// Use the user's selected reader
	selectedReaderID := services.AppState.SelectedReaderID
	if selectedReaderID == "" {
		utils.Error("payment", "No terminal reader selected", "intent_id", intent.ID)
		if renderErr := renderErrorModal(w, r,
			"Please select a terminal reader before attempting payment.",
			intent.ID); renderErr != nil {
			utils.Error("payment", "Error rendering no reader selected modal", "intent_id", intent.ID, "error", renderErr)
		}
		return TerminalProcessingResult{
			Success:    false,
			ShouldStop: true,
			Message:    "No terminal reader selected",
		}
	}

	// Verify the selected reader is online
	if !isReaderOnline(selectedReaderID) {
		utils.Error("payment", "Selected terminal reader is not online", "reader_id", selectedReaderID, "intent_id", intent.ID)
		if renderErr := renderErrorModal(w, r,
			"The selected terminal reader is not online. Please check reader status or select a different reader.",
			intent.ID); renderErr != nil {
			utils.Error("payment", "Error rendering reader offline modal", "intent_id", intent.ID, "error", renderErr)
		}
		return TerminalProcessingResult{
			Success:    false,
			ShouldStop: true,
			Message:    "Selected reader is offline",
		}
	}

	// Process payment on the terminal reader
	processedReader, err := processPaymentOnTerminal(intent.ID, selectedReaderID, summary)
	if err != nil {
		utils.Error("payment", "Error commanding reader to process PaymentIntent", "reader_id", selectedReaderID, "intent_id", intent.ID, "error", err)
		errMsg := "Error communicating with the payment terminal."
		if stripeErr, ok := err.(*stripe.Error); ok {
			errMsg = fmt.Sprintf("Terminal communication error: %s", stripeErr.Msg)
		}
		if renderErr := renderErrorModal(w, r, errMsg, intent.ID); renderErr != nil {
			utils.Error("payment", "Error rendering terminal communication error modal", "intent_id", intent.ID, "error", renderErr)
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

// isReaderOnline checks if a specific reader ID is online
func isReaderOnline(readerID string) bool {
	for _, reader := range services.AppState.SiteStripeReaders {
		if reader.ID == readerID && reader.Status == "online" {
			return true
		}
	}
	return false
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

	utils.Info("payment", "Attempting to process PaymentIntent on terminal reader",
		"intent_id", intentID, "reader_id", readerID, "tipping_enabled", shouldEnableTipping, "amount", summary.Total)
	return reader.ProcessPaymentIntent(readerID, readerParams)
}

// handleTerminalActionResult handles the result of a terminal reader action
func handleTerminalActionResult(w http.ResponseWriter, r *http.Request, intent *stripe.PaymentIntent,
	selectedReaderID string, processedReader *stripe.TerminalReader, email string, summary templates.CartSummary) TerminalProcessingResult {

	if processedReader == nil || processedReader.Action == nil {
		utils.Error("payment", "Unexpected nil reader or action after ProcessPaymentIntent",
			"intent_id", intent.ID, "reader_id", selectedReaderID)
		errMsg := "An unexpected error occurred with the terminal. Payment status is unclear."
		if renderErr := renderErrorModal(w, r, errMsg, intent.ID); renderErr != nil {
			utils.Error("payment", "Error rendering nil action/reader modal", "intent_id", intent.ID, "error", renderErr)
		}
		return TerminalProcessingResult{
			Success:    false,
			ShouldStop: true,
			Message:    "Unexpected terminal state",
		}
	}

	utils.Debug("payment", "Reader action status", "reader_id", selectedReaderID, "intent_id", intent.ID, "status", processedReader.Action.Status)

	switch processedReader.Action.Status {
	case stripe.TerminalReaderActionStatusSucceeded:
		return handleTerminalSuccess(w, r, intent, processedReader)

	case stripe.TerminalReaderActionStatusFailed:
		return handleTerminalFailure(w, r, intent, processedReader)

	case stripe.TerminalReaderActionStatusInProgress:
		return handleTerminalInProgress(w, r, intent, selectedReaderID, email, summary)

	default:
		utils.Error("payment", "Unexpected terminal reader action status", "status", processedReader.Action.Status, "intent_id", intent.ID)
		errMsg := fmt.Sprintf("Unexpected terminal status: %s", processedReader.Action.Status)
		if renderErr := renderErrorModal(w, r, errMsg, intent.ID); renderErr != nil {
			utils.Error("payment", "Error rendering unexpected status modal", "intent_id", intent.ID, "error", renderErr)
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
		utils.Error("payment", "PaymentIntent is nil within successful reader action", "intent_id", intent.ID)
		errMsg := "Payment confirmation missing after successful terminal interaction."
		if renderErr := renderErrorModal(w, r, errMsg, intent.ID); renderErr != nil {
			utils.Error("payment", "Error rendering PI nil in action modal", "intent_id", intent.ID, "error", renderErr)
		}
		return TerminalProcessingResult{
			Success:    false,
			ShouldStop: true,
			Message:    "Payment confirmation missing",
		}
	}

	utils.Debug("payment", "Terminal PaymentIntent final status", "intent_id", pi.ID, "status", pi.Status)
	if pi.Status == stripe.PaymentIntentStatusSucceeded {
		utils.Info("payment", "PaymentIntent succeeded on terminal reader", "intent_id", intent.ID, "amount", float64(pi.Amount)/100)
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
		utils.Error("payment", "PaymentIntent not successful after terminal success", "intent_id", pi.ID, "status", string(pi.Status), "decline_reason", declineMessage)
		if renderErr := renderErrorModal(w, r, declineMessage, pi.ID); renderErr != nil {
			utils.Error("payment", "Error rendering payment declined modal", "intent_id", pi.ID, "error", renderErr)
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
	utils.Error("payment", "Terminal reader action failed", "intent_id", intent.ID,
		"failure_message", processedReader.Action.FailureMessage, "failure_code", processedReader.Action.FailureCode)
	if renderErr := renderErrorModal(w, r, errMsg, intent.ID); renderErr != nil {
		utils.Error("payment", "Error rendering reader action failed modal", "intent_id", intent.ID, "error", renderErr)
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

	utils.Info("payment", "Terminal payment in progress - switching to polling",
		"intent_id", intent.ID, "reader_id", selectedReaderID)

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
		utils.Error("payment", "Error rendering terminal payment progress modal", "intent_id", intent.ID, "error", renderErr)
	}

	return TerminalProcessingResult{
		Success:    true,
		ShouldStop: true, // Stop processing in main handler, polling will take over
		Message:    "Terminal polling initiated",
	}
}
