package services

import (
	"time"

	"checkout/templates"
)

// State holds application state
type State struct {
	Services    []templates.Service
	CurrentCart []templates.Service

	// Stripe Terminal state
	AvailableStripeLocations []templates.StripeLocation
	SelectedStripeLocation   templates.StripeLocation
	SiteStripeReaders        []templates.StripeReader
	SelectedReaderID         string // ID of the reader selected by the user
}

// ActiveTerminalPayment holds information about an ongoing terminal payment
type ActiveTerminalPayment struct {
	PaymentIntentID string
	ReaderID        string
	StartTime       time.Time
	Email           string
	Cart            []templates.Service
	Summary         templates.CartSummary
}

// ActiveTerminalPayments stores active terminal payment sessions (key: PaymentIntentID)
// TODO: Add mutex for concurrent access if multiple goroutines can modify this map.
var ActiveTerminalPayments = make(map[string]ActiveTerminalPayment)

// AppState is the global application state instance
var AppState State

// ClearPaymentState clears any pending payment state for the currently selected terminal
func ClearPaymentState() {
	selectedReaderID := AppState.SelectedReaderID
	if selectedReaderID == "" {
		return
	}

	// Find and remove any active terminal payments for this reader
	for paymentIntentID, payment := range ActiveTerminalPayments {
		if payment.ReaderID == selectedReaderID {
			delete(ActiveTerminalPayments, paymentIntentID)
		}
	}

	// Clear the cart as well since the transaction is being reset
	AppState.CurrentCart = []templates.Service{}
}

// ClearSpecificPaymentState clears payment state for a specific payment intent or reader
func ClearSpecificPaymentState(paymentIntentID string, readerID string) {
	// Remove from ActiveTerminalPayments if payment intent ID is provided
	if paymentIntentID != "" {
		delete(ActiveTerminalPayments, paymentIntentID)
	}

	// If reader ID is provided, remove all payments for that reader
	if readerID != "" {
		for piID, payment := range ActiveTerminalPayments {
			if payment.ReaderID == readerID {
				delete(ActiveTerminalPayments, piID)
			}
		}
	}

	// Clear the cart since the transaction is being reset
	AppState.CurrentCart = []templates.Service{}
}

// ClearPaymentLinkState clears payment link state (for payment link cancellations/expirations)
func ClearPaymentLinkState() {
	// Clear the cart since the payment link transaction is being reset
	AppState.CurrentCart = []templates.Service{}
}
