package handlers

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"

	"github.com/a-h/templ"
	"github.com/skip2/go-qrcode"
	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/paymentlink"

	"checkout/services"
	"checkout/templates/checkout"
)

// GenerateQRCodeHandler handles QR code generation for payment links
func GenerateQRCodeHandler(w http.ResponseWriter, r *http.Request) {
	// Check if cart is empty first
	if len(services.AppState.CurrentCart) == 0 {
		// Send a toast message for empty cart
		w.Header().Set("HX-Trigger", `{"showToast": "Cart is empty. Please add items before generating a QR code."}`)
		w.WriteHeader(http.StatusBadRequest)
		log.Printf("Cart is empty, returning 400")
		return
	}

	// Parse email from form if available
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	email := r.FormValue("email") // This might be empty

	log.Printf("Starting QR code generation, cart has %d items", len(services.AppState.CurrentCart))
	summary, err := services.CalculateCartSummary()
	if err != nil {
		log.Printf("Error calculating cart summary: %v", err)

		// Create a sanitized error message for the user
		errorMsg := "Error calculating taxes. Please try again."

		// Properly escape the message for JSON
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": %q}`, errorMsg))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Create and configure payment link
	paymentLink, err := services.CreatePaymentLink(summary.Total, email)
	if err != nil {
		log.Printf("Error creating payment link: %v", err)
		// Send error via toast message
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": "Error creating payment link: %s"}`, err.Error()))
		return
	}

	// Note: We don't create a transaction record for link creation anymore
	// The actual payment transaction will be logged when the payment is completed
	log.Printf("Payment link %s created for amount %.2f", paymentLink.ID, summary.Total)

	// Use the payment link URL for the QR code
	stripePaymentLink := paymentLink.URL

	// Generate the QR code using the go-qrcode library
	qrCode, err := qrcode.New(stripePaymentLink, qrcode.Medium)
	if err != nil {
		log.Printf("Error generating QR code: %v", err)
		// Send error via toast message
		w.Header().Set("HX-Trigger", `{"showToast": "Error generating QR code"}`)
		return
	}

	// Convert QR code to PNG image data
	qrPNG, err := qrCode.PNG(256)
	if err != nil {
		log.Printf("Error converting QR code to PNG: %v", err)
		// Send error via toast message
		w.Header().Set("HX-Trigger", `{"showToast": "Error generating QR code image"}`)
		return
	}

	// Encode the PNG as base64 for embedding in HTML
	qrBase64 := base64.StdEncoding.EncodeToString(qrPNG)

	// Set the HTMX trigger to show modal
	w.Header().Set("HX-Trigger", "showModal")

	// Use the QRCodeDisplay template to render the QR code in the modal
	var qrDisplay templ.Component
	if email != "" {
		// Match the exact case in the template definition
		qrDisplay = checkout.QRCodeDisplayWithEmail(qrBase64, paymentLink.ID, summary.Total, email)
	} else {
		qrDisplay = checkout.QRCodeDisplay(qrBase64, paymentLink.ID, summary.Total)
	}
	if err := qrDisplay.Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// CheckPaymentlinkStatusHandler handles checking the status of payment links for QR code payments
func CheckPaymentlinkStatusHandler(w http.ResponseWriter, r *http.Request) {
	config := PaymentPollingConfig{
		PaymentType:     "qr",
		TimeoutDuration: PAYMENT_POLLING_TIMEOUT,
	}
	checkPaymentStatusGeneric(w, r, config)
}

// CancelPaymentLinkHandler cancels a payment link
func CancelPaymentLinkHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	paymentLinkID := r.FormValue("payment_link_id")
	if paymentLinkID == "" {
		http.Error(w, "Payment link ID is required", http.StatusBadRequest)
		return
	}

	// Deactivate the payment link in Stripe with a single line
	pl, err := paymentlink.Update(paymentLinkID, &stripe.PaymentLinkParams{Active: stripe.Bool(false)})
	if err != nil {
		log.Printf("Error cancelling payment link: %v", err)
		w.Header().Set("HX-Trigger", `{"showToast": "Error cancelling payment link"}`)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Log the cancellation using the unified event logger
	GlobalPaymentEventLogger.LogPaymentEventQuick(paymentLinkID, PaymentEventCancelled, "qr")

	// Clear payment state and cart using unified state manager
	GlobalPaymentStateManager.RemovePaymentAndClearCart(paymentLinkID)

	// Render the cancelled payment template
	component := checkout.PaymentCancelled(pl.ID)
	if err := component.Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ExpirePaymentLinkHandler handles automatic expiration of payment links
func ExpirePaymentLinkHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	paymentLinkID := r.FormValue("payment_link_id")
	if paymentLinkID == "" {
		http.Error(w, "Payment link ID is required", http.StatusBadRequest)
		return
	}

	// Deactivate the payment link in Stripe with a single line
	pl, err := paymentlink.Update(paymentLinkID, &stripe.PaymentLinkParams{Active: stripe.Bool(false)})
	if err != nil {
		log.Printf("Error expiring payment link: %v", err)
		w.Header().Set("HX-Trigger", `{"showToast": "Error expiring payment link"}`)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Log the expiration using the unified event logger
	GlobalPaymentEventLogger.LogPaymentEventQuick(paymentLinkID, PaymentEventExpired, "qr")

	// Clean up payment link creation time
	GlobalPaymentStateManager.RemovePayment(paymentLinkID)

	// Render the expired payment template
	component := checkout.PaymentExpired(pl.ID)
	if err := component.Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// CancelTransactionHandler handles cancelling the entire transaction and resetting state
func CancelTransactionHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	paymentLinkID := r.FormValue("payment_link_id")
	
	// If we have a payment link ID, deactivate it in Stripe
	if paymentLinkID != "" {
		_, err := paymentlink.Update(paymentLinkID, &stripe.PaymentLinkParams{Active: stripe.Bool(false)})
		if err != nil {
			log.Printf("Error cancelling payment link during transaction cancellation: %v", err)
			// Continue anyway - we still want to clear local state
		} else {
			log.Printf("Payment link %s cancelled during transaction cancellation", paymentLinkID)
		}

		// Log the cancellation using the unified event logger
		GlobalPaymentEventLogger.LogPaymentEventQuick(paymentLinkID, PaymentEventCancelled, "qr")
	}

	// Clear all payment states and cart using unified state manager
	GlobalPaymentStateManager.ClearAllAndClearCart()

	log.Println("Transaction cancelled - cart and payment states cleared")

	// Close modal and show success toast
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Trigger", `{"closeModal": true, "showToastSuccess": "Transaction cancelled - cart cleared", "cartUpdated": true}`)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("")) // Empty response since we're just triggering events
}

