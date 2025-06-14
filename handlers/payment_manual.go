package handlers

import (
	"fmt"
	"net/http"

	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/paymentintent"

	"checkout/config"
	"checkout/services"
	"checkout/templates"
	"checkout/templates/checkout"
	"checkout/utils"
)

// ManualCardFormHandler handles the manual card entry form
func ManualCardFormHandler(w http.ResponseWriter, r *http.Request) {
	// Check if cart is empty first (for both GET and POST)
	if len(services.AppState.CurrentCart) == 0 {
		// Send a toast message for empty cart
		w.Header().Set("HX-Trigger", `{"showToast": "Cart is empty. Please add items before entering card details."}`)
		w.WriteHeader(http.StatusBadRequest)
		utils.Warn("payment", "Manual card entry rejected - cart empty")
		return
	}

	// If this is a POST request, process the card payment
	if r.Method == "POST" {
		processManualCardPayment(w, r)
		return
	}

	// For GET requests, just show the card entry form
	// Get Stripe publishable key from config
	stripePublicKey := config.GetStripePublicKey()
	component := checkout.ManualCardForm(stripePublicKey)

	// Use renderInfoModal to set proper HTMX headers for modal display
	if err := renderInfoModal(w, r, component); err != nil {
		utils.Error("payment", "Error rendering manual card form modal", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// processManualCardPayment handles the complete manual card payment flow
func processManualCardPayment(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	// Extract payment method ID and other form data
	paymentMethodID := r.FormValue("payment_method_id")
	cardholder := r.FormValue("cardholder")
	email := r.FormValue("email")

	// Validate required fields (only payment method ID and cardholder are required)
	if paymentMethodID == "" {
		renderManualPaymentError(w, r, "Please enter your card details", "")
		return
	}

	if cardholder == "" {
		renderManualPaymentError(w, r, "Please enter the cardholder name", "")
		return
	}

	// Calculate cart summary with taxes
	summary := services.CalculateCartSummary()

	// Create a payment intent for manual card processing
	params := &stripe.PaymentIntentParams{
		Amount:             stripe.Int64(int64(summary.Total * 100)), // Convert to cents
		Currency:           stripe.String("usd"),
		CaptureMethod:      stripe.String("automatic"),
		PaymentMethodTypes: []*string{stripe.String("card")},
	}

	// Add receipt email if provided
	if email != "" {
		params.ReceiptEmail = stripe.String(email)
	}

	intent, err := paymentintent.New(params)
	if err != nil {
		utils.Error("payment", "Error creating payment intent", "amount", summary.Total, "email", email, "error", err)
		w.Header().Set("HX-Trigger", `{"showToast": "Error processing payment"}`)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	intentID := intent.ID

	// The payment method was already created by Stripe Elements on the frontend
	// We just need to confirm the payment intent with the existing payment method
	confirmParams := &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String(paymentMethodID),
	}

	if email != "" {
		confirmParams.ReceiptEmail = stripe.String(email)
	}

	intent, err = paymentintent.Confirm(intentID, confirmParams)
	if err != nil {
		utils.Error("payment", "Error confirming payment intent", "intent_id", intentID, "error", err)

		// Handle specific error types
		if stripeErr, ok := err.(*stripe.Error); ok {
			switch stripeErr.Code {
			case stripe.ErrorCodeCardDeclined:
				renderManualPaymentError(w, r, "Your card was declined", intentID)
			case stripe.ErrorCodeInsufficientFunds:
				renderManualPaymentError(w, r, "Insufficient funds", intentID)
			case stripe.ErrorCodeIncorrectCVC:
				renderManualPaymentError(w, r, "Incorrect CVC", intentID)
			case stripe.ErrorCodeExpiredCard:
				renderManualPaymentError(w, r, "Your card has expired", intentID)
			default:
				renderManualPaymentError(w, r, "Payment failed: "+stripeErr.Msg, intentID)
			}
		} else {
			renderManualPaymentError(w, r, "Payment processing failed", intentID)
		}
		return
	}

	// Check payment status
	switch intent.Status {
	case stripe.PaymentIntentStatusSucceeded:
		// Payment successful
		handleManualPaymentSuccess(w, r, intent, email)
	case stripe.PaymentIntentStatusRequiresAction:
		// 3D Secure or other authentication required
		renderManualPaymentAuthentication(w, r, intent)
	default:
		// Other status - treat as failure
		renderManualPaymentError(w, r, fmt.Sprintf("Payment status: %s", intent.Status), intentID)
	}
}

// handleManualPaymentSuccess handles a successful manual card payment
func handleManualPaymentSuccess(w http.ResponseWriter, r *http.Request, intent *stripe.PaymentIntent, email string) {
	utils.Info("payment", "Manual card payment succeeded", "intent_id", intent.ID, "amount", float64(intent.Amount)/100)

	// Calculate cart summary for transaction record
	summary := services.CalculateCartSummary()

	// Save transaction
	_ = GlobalPaymentEventLogger.LogPaymentEvent(
		intent.ID,
		PaymentEventSuccess,
		"manual",
		services.AppState.CurrentCart,
		summary,
		email,
	)

	// Clear cart
	services.AppState.CurrentCart = []templates.Service{}

	// Render success modal
	if err := renderSuccessModal(w, r, intent.ID, email != ""); err != nil {
		utils.Error("payment", "Error rendering payment success modal", "intent_id", intent.ID, "error", err)
	}
}

// renderManualPaymentError renders an error modal using the same pattern as terminal payments
func renderManualPaymentError(w http.ResponseWriter, r *http.Request, errorMessage, intentID string) {
	utils.Error("payment", "Manual payment error", "intent_id", intentID, "error_message", errorMessage)

	// Use the same error modal pattern as terminal payments
	if err := renderErrorModal(w, r, errorMessage, intentID); err != nil {
		utils.Error("payment", "Error rendering manual payment error modal", "intent_id", intentID, "error", err)
	}
}

// renderManualPaymentAuthentication handles 3D Secure authentication
func renderManualPaymentAuthentication(w http.ResponseWriter, r *http.Request, intent *stripe.PaymentIntent) {
	utils.Warn("payment", "Manual payment requires authentication", "intent_id", intent.ID)

	// For 3D Secure, we would typically redirect to the authentication URL
	// or handle it client-side with Stripe Elements
	authMessage := "This payment requires additional authentication. Please contact support."
	if intent.NextAction != nil && intent.NextAction.RedirectToURL != nil {
		authMessage = fmt.Sprintf("Please complete authentication at: %s", intent.NextAction.RedirectToURL.URL)
	}

	// Use PaymentDeclinedModal as a fallback for authentication requirements
	if err := renderErrorModal(w, r, authMessage, intent.ID); err != nil {
		utils.Error("payment", "Error rendering authentication modal", "intent_id", intent.ID, "error", err)
	}
}

// Card validation is handled by Stripe Elements on the client-side
