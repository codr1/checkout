package handlers

import (
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/skip2/go-qrcode"
	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/paymentlink"

	"checkout/services"
	"checkout/templates/checkout"
	"checkout/utils"
)

// GenerateQRCodeHandler handles QR code generation for payment links
func GenerateQRCodeHandler(w http.ResponseWriter, r *http.Request) {
	// Check if cart is empty first
	if len(services.AppState.CurrentCart) == 0 {
		// Send a toast message for empty cart
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Cart is empty. Please add items before generating a QR code.", "type": "warning"}}`)
		w.WriteHeader(http.StatusOK) // Changed from BadRequest to OK since this is a valid user action
		utils.Info("payment", "QR generation rejected - cart empty")
		return
	}

	utils.Info("payment", "Starting QR code generation", "cart_items", len(services.AppState.CurrentCart))
	summary := services.CalculateCartSummary()

	// Create and configure payment link (no email - receipt will be collected post-payment)
	paymentLink, err := services.CreatePaymentLink(summary.Total, "")
	if err != nil {
		utils.Error("payment", "Error creating payment link", "amount", summary.Total, "error", err)
		// Send error via toast message
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": "Error creating payment link: %s"}`, err.Error()))
		return
	}

	// Note: We don't create a transaction record for link creation anymore
	// The actual payment transaction will be logged when the payment is completed
	utils.Info("payment", "Payment link created", "payment_link_id", paymentLink.ID, "amount", summary.Total)

	// Use the payment link URL for the QR code
	stripePaymentLink := paymentLink.URL

	// Generate the QR code using the go-qrcode library
	qrCode, err := qrcode.New(stripePaymentLink, qrcode.Medium)
	if err != nil {
		utils.Error("payment", "Error generating QR code", "payment_link_id", paymentLink.ID, "error", err)
		// Send error via toast message
		w.Header().Set("HX-Trigger", `{"showToast": "Error generating QR code"}`)
		return
	}

	// Convert QR code to PNG image data
	qrPNG, err := qrCode.PNG(256)
	if err != nil {
		utils.Error("payment", "Error converting QR code to PNG", "payment_link_id", paymentLink.ID, "error", err)
		// Send error via toast message
		w.Header().Set("HX-Trigger", `{"showToast": "Error generating QR code image"}`)
		return
	}

	// Encode the PNG as base64 for embedding in HTML
	qrBase64 := base64.StdEncoding.EncodeToString(qrPNG)

	// Set the HTMX trigger to show modal
	w.Header().Set("HX-Trigger", "showModal")

	// Use the QRCodeDisplay template to render the QR code in the modal
	// No email collected pre-payment - receipt will be collected post-payment
	qrDisplay := checkout.QRCodeDisplay(qrBase64, paymentLink.ID, summary.Total)
	if err := qrDisplay.Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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
			utils.Error("payment", "Error cancelling payment link during transaction cancellation", "payment_link_id", paymentLinkID, "error", err)
			// Continue anyway - we still want to clear local state
		} else {
			utils.Info("payment", "Payment link cancelled during transaction cancellation", "payment_link_id", paymentLinkID)
		}

		// Log the cancellation using the unified event logger
		_ = GlobalPaymentEventLogger.LogPaymentEventQuick(paymentLinkID, PaymentEventCancelled, "qr")
	}

	// Clear all payment states and cart using unified state manager
	GlobalPaymentStateManager.ClearAllAndClearCart()

	utils.Info("payment", "Transaction cancelled - cart and payment states cleared")

	// Close modal and show success toast
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Trigger", `{"closeModal": true, "showToast": {"message": "Transaction cancelled - cart cleared", "type": "success"}, "cartUpdated": true}`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("")) // Empty response since we're just triggering events
}
