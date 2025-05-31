package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/paymentintent"
	"github.com/stripe/stripe-go/v74/paymentmethod"

	"checkout/services"
	"checkout/templates"
	"checkout/templates/checkout"
)

// ManualCardFormHandler handles the manual card entry form
func ManualCardFormHandler(w http.ResponseWriter, r *http.Request) {
	// Parse payment intent ID from URL query or form
	intentID := r.URL.Query().Get("intent_id")
	if intentID == "" {
		if err := r.ParseForm(); err == nil {
			intentID = r.FormValue("intent_id")
		}
	}

	if intentID == "" {
		http.Error(w, "Payment Intent ID is required", http.StatusBadRequest)
		return
	}

	// If this is a POST request, process the card payment
	if r.Method == "POST" {
		processManualCardPayment(w, r, intentID)
		return
	}

	// Otherwise, render the card entry form
	component := checkout.ManualCardForm(intentID)
	if err := component.Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// processManualCardPayment processes a manual card payment
func processManualCardPayment(w http.ResponseWriter, r *http.Request, intentID string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	// Extract card details from form
	cardNumber := r.FormValue("card_number")
	expMonth := r.FormValue("exp_month")
	expYear := r.FormValue("exp_year")
	cvc := r.FormValue("cvc")
	email := r.FormValue("email")

	// Validate required fields
	if cardNumber == "" || expMonth == "" || expYear == "" || cvc == "" {
		renderManualPaymentError(w, r, "All card fields are required", intentID)
		return
	}

	// Parse and validate expiration month
	expMonthInt, err := strconv.Atoi(expMonth)
	if err != nil || expMonthInt < 1 || expMonthInt > 12 {
		renderManualPaymentError(w, r, "Invalid expiration month. Please enter a value between 1 and 12", intentID)
		return
	}

	// Parse and validate expiration year
	expYearInt, err := strconv.Atoi(expYear)
	if err != nil || expYearInt < 2024 || expYearInt > 2050 {
		renderManualPaymentError(w, r, "Invalid expiration year. Please enter a valid year", intentID)
		return
	}

	// Create payment method with card details
	pmParams := &stripe.PaymentMethodParams{
		Type: stripe.String("card"),
		Card: &stripe.PaymentMethodCardParams{
			Number:   stripe.String(cardNumber),
			ExpMonth: stripe.Int64(int64(expMonthInt)),
			ExpYear:  stripe.Int64(int64(expYearInt)),
			CVC:      stripe.String(cvc),
		},
	}

	pm, err := paymentmethod.New(pmParams)
	if err != nil {
		log.Printf("Error creating payment method: %v", err)
		renderManualPaymentError(w, r, "Invalid card details", intentID)
		return
	}

	// Confirm the payment intent with the payment method
	confirmParams := &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String(pm.ID),
	}

	if email != "" {
		confirmParams.ReceiptEmail = stripe.String(email)
	}

	intent, err := paymentintent.Confirm(intentID, confirmParams)
	if err != nil {
		log.Printf("Error confirming payment intent: %v", err)

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
	if intent.Status == stripe.PaymentIntentStatusSucceeded {
		// Payment successful
		handleManualPaymentSuccess(w, r, intent, email)
	} else if intent.Status == stripe.PaymentIntentStatusRequiresAction {
		// 3D Secure or other authentication required
		renderManualPaymentAuthentication(w, r, intent)
	} else {
		// Other status - treat as failure
		renderManualPaymentError(w, r, fmt.Sprintf("Payment status: %s", intent.Status), intentID)
	}
}

// handleManualPaymentSuccess handles a successful manual card payment
func handleManualPaymentSuccess(w http.ResponseWriter, r *http.Request, intent *stripe.PaymentIntent, email string) {
	log.Printf("Manual card payment %s succeeded", intent.ID)

	// Calculate cart summary for transaction record
	summary, err := services.CalculateCartSummary()
	if err != nil {
		log.Printf("Error calculating cart summary for completed payment: %v", err)
		summary = templates.CartSummary{} // Use empty summary to avoid nil pointer
	}

	// Save transaction using the unified event logger
	GlobalPaymentEventLogger.LogPaymentEvent(intent.ID, PaymentEventSuccess, "manual", services.AppState.CurrentCart, summary, email)

	// Clear cart
	services.AppState.CurrentCart = []templates.Service{}

	// Render success modal
	if err := renderSuccessModal(w, r, intent.ID, email != ""); err != nil {
		log.Printf("Error rendering payment success modal: %v", err)
	}
}

// renderManualPaymentError renders an error modal for manual payment
func renderManualPaymentError(w http.ResponseWriter, r *http.Request, errorMessage, intentID string) {
	log.Printf("Manual payment error for intent %s: %s", intentID, errorMessage)

	if err := renderErrorModal(w, r, errorMessage, intentID); err != nil {
		log.Printf("Error rendering payment error modal: %v", err)
	}
}

// renderManualPaymentAuthentication handles 3D Secure authentication
func renderManualPaymentAuthentication(w http.ResponseWriter, r *http.Request, intent *stripe.PaymentIntent) {
	log.Printf("Manual payment %s requires authentication", intent.ID)

	// For 3D Secure, we would typically redirect to the authentication URL
	// or handle it client-side with Stripe Elements
	authMessage := "This payment requires additional authentication. Please contact support."
	if intent.NextAction != nil && intent.NextAction.RedirectToURL != nil {
		authMessage = fmt.Sprintf("Please complete authentication at: %s", intent.NextAction.RedirectToURL.URL)
	}

	// Use PaymentDeclinedModal as a fallback for authentication requirements
	if err := renderErrorModal(w, r, authMessage, intent.ID); err != nil {
		log.Printf("Error rendering authentication modal: %v", err)
	}
}

// validateCardNumber performs basic Luhn algorithm validation
func validateCardNumber(cardNumber string) bool {
	// Remove any spaces or dashes
	cleaned := ""
	for _, char := range cardNumber {
		if char >= '0' && char <= '9' {
			cleaned += string(char)
		}
	}

	// Must be at least 13 digits
	if len(cleaned) < 13 {
		return false
	}

	// Luhn algorithm
	sum := 0
	alternate := false

	for i := len(cleaned) - 1; i >= 0; i-- {
		digit := int(cleaned[i] - '0')

		if alternate {
			digit *= 2
			if digit > 9 {
				digit = digit%10 + digit/10
			}
		}

		sum += digit
		alternate = !alternate
	}

	return sum%10 == 0
}

// getCardType returns the card type based on the card number
func getCardType(cardNumber string) string {
	// Remove any spaces or dashes
	cleaned := ""
	for _, char := range cardNumber {
		if char >= '0' && char <= '9' {
			cleaned += string(char)
		}
	}

	if len(cleaned) < 4 {
		return "unknown"
	}

	prefix := cleaned[:4]

	// Visa
	if cleaned[0] == '4' {
		return "visa"
	}

	// Mastercard
	if prefix >= "5100" && prefix <= "5599" {
		return "mastercard"
	}
	if prefix >= "2221" && prefix <= "2720" {
		return "mastercard"
	}

	// American Express
	if prefix[:2] == "34" || prefix[:2] == "37" {
		return "amex"
	}

	// Discover
	if prefix[:4] == "6011" || prefix[:2] == "65" {
		return "discover"
	}

	return "unknown"
}
