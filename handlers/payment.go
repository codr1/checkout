package handlers

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/skip2/go-qrcode"
	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/paymentintent"
	"github.com/stripe/stripe-go/v74/paymentlink"
	"github.com/stripe/stripe-go/v74/terminal/reader"

	"checkout/services"
	"checkout/templates"
	"checkout/templates/checkout"
)

var (
	// terminalPaymentStates stores the state of active terminal payments.
	// In a production environment, this should be a more persistent store
	// and thread-safe.
	terminalPaymentStates = make(map[string]services.ActiveTerminalPayment)
	// Consider adding a mutex here if handlers run concurrently and modify this map.
	// For now, assuming single-threaded access per request simplifies the example.
)

const terminalPaymentTimeout = 120 * time.Second

// ProcessPaymentHandler handles payment processing
func ProcessPaymentHandler(w http.ResponseWriter, r *http.Request) {
	if len(services.AppState.CurrentCart) == 0 {
		w.Header().Set("HX-Trigger", `{"showToast": "Cart is empty"}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	email := r.FormValue("email")
	paymentMethod := r.FormValue("payment_method")

	// Calculate cart summary with taxes
	summary, err := services.CalculateCartSummary()
	if err != nil {
		log.Printf("Error calculating cart summary: %v", err)

		// Send sanitized error message and set response headers in one line
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": %q}`, "Error calculating taxes. Please try again."))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Create a payment intent with appropriate payment method
	params := &stripe.PaymentIntentParams{
		Amount:        stripe.Int64(int64(summary.Total * 100)), // Convert to cents
		Currency:      stripe.String("usd"),
		CaptureMethod: stripe.String("automatic"),
	}

	// Configure payment method types based on the payment method
	switch paymentMethod {
	case "terminal":
		params.PaymentMethodTypes = []*string{
			stripe.String("card_present"),
		}
	case "manual":
		params.PaymentMethodTypes = []*string{
			stripe.String("card"),
		}
		// Additional fields for manual card entry would be processed here
	case "qr":
		params.PaymentMethodTypes = []*string{
			stripe.String("card"),
		}
		// QR code specific configuration would go here
	default:
		params.PaymentMethodTypes = []*string{
			stripe.String("card_present"),
		}
	}

	// Add receipt email if provided
	if email != "" {
		params.ReceiptEmail = stripe.String(email)
	}

	intent, err := paymentintent.New(params)
	if err != nil {
		log.Printf("Error creating payment intent: %v", err)
		w.Header().Set("HX-Trigger", `{"showToast": "Error processing payment"}`)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var paymentSuccess bool

	// Process payment based on method
	switch paymentMethod {
	case "terminal":
		var selectedReaderID string
		if len(services.AppState.SiteStripeReaders) > 0 {
			for _, r_idx := range services.AppState.SiteStripeReaders {
				if r_idx.Status == "online" {
					selectedReaderID = r_idx.ID
					log.Printf("Selected online terminal reader: %s (Label: %s)", selectedReaderID, r_idx.Label)
					break
				}
			}
		}

		if selectedReaderID == "" {
			log.Println("Error processing terminal payment: No online Stripe Terminal reader found.")
			w.Header().Set("HX-Trigger", "showModal")
			w.Header().Set("HX-Retarget", "#modal-content")
			w.Header().Set("HX-Reswap", "innerHTML")
			w.WriteHeader(http.StatusOK)
			component := checkout.PaymentDeclinedModal(
				"No online terminal reader available. Please check reader status or select a different payment method.",
				intent.ID,
			) // intent.ID is fine HERE
			if renderErr := component.Render(r.Context(), w); renderErr != nil {
				log.Printf("Error rendering no reader available modal: %v", renderErr)
			}
			return // paymentSuccess remains false by default
		}

		readerParams := &stripe.TerminalReaderProcessPaymentIntentParams{
			PaymentIntent: stripe.String(intent.ID),
		}

		log.Printf("Attempting to process PaymentIntent %s on reader %s", intent.ID, selectedReaderID)
		processedReader, err := reader.ProcessPaymentIntent(selectedReaderID, readerParams)

		if err != nil {
			log.Printf("Error commanding reader %s to process PaymentIntent %s: %v", selectedReaderID, intent.ID, err)
			errMsg := "Error communicating with the payment terminal."
			var stripeErr *stripe.Error
			if errors.As(err, &stripeErr) {
				errMsg = fmt.Sprintf("Terminal communication error: %s", stripeErr.Msg)
			}
			w.Header().Set("HX-Trigger", "showModal")
			w.Header().Set("HX-Retarget", "#modal-content")
			w.Header().Set("HX-Reswap", "innerHTML")
			w.WriteHeader(http.StatusOK)
			component := checkout.PaymentDeclinedModal(errMsg, intent.ID)
			if renderErr := component.Render(r.Context(), w); renderErr != nil {
				log.Printf("Error rendering terminal communication error modal: %v", renderErr)
			}
			return
		}

		if processedReader == nil || processedReader.Action == nil {
			log.Printf(
				"Unexpected nil reader or action after ProcessPaymentIntent for PI %s on reader %s.",
				intent.ID,
				selectedReaderID,
			)
			errMsg := "An unexpected error occurred with the terminal. Payment status is unclear."
			w.Header().Set("HX-Trigger", "showModal")
			w.Header().Set("HX-Retarget", "#modal-content")
			w.Header().Set("HX-Reswap", "innerHTML")
			w.WriteHeader(http.StatusOK)
			component := checkout.PaymentDeclinedModal(errMsg, intent.ID)
			if renderErr := component.Render(r.Context(), w); renderErr != nil {
				log.Printf("Error rendering nil action/reader modal: %v", renderErr)
			}
			return
		}

		log.Printf("Reader %s action status for PI %s: %s", selectedReaderID, intent.ID, processedReader.Action.Status)

		switch processedReader.Action.Status {
		case stripe.TerminalReaderActionStatusSucceeded:
			pi := processedReader.Action.ProcessPaymentIntent.PaymentIntent
			if pi == nil {
				log.Printf("PaymentIntent is nil within successful reader action for PI %s.", intent.ID)
				errMsg := "Payment confirmation missing after successful terminal interaction."
				w.Header().Set("HX-Trigger", "showModal")
				w.Header().Set("HX-Retarget", "#modal-content")
				w.Header().Set("HX-Reswap", "innerHTML")
				w.WriteHeader(http.StatusOK)
				component := checkout.PaymentDeclinedModal(errMsg, intent.ID)
				if renderErr := component.Render(r.Context(), w); renderErr != nil {
					log.Printf("Error rendering PI nil in action modal: %v", renderErr)
				}
				return
			}

			log.Printf("Terminal PaymentIntent %s final status: %s", pi.ID, pi.Status)
			if pi.Status == stripe.PaymentIntentStatusSucceeded {
				paymentSuccess = true
				intent = pi // Update the main intent variable with the one from the reader action
				log.Printf("PaymentIntent %s Succeeded on terminal reader %s.", intent.ID, selectedReaderID)
			} else {
				declineMessage := "Payment declined by terminal."
				if pi.LastPaymentError != nil && pi.LastPaymentError.Msg != "" {
					declineMessage = fmt.Sprintf("Payment declined: %s", pi.LastPaymentError.Msg)
				}
				log.Printf("PaymentIntent %s not successful. Status: %s. Decline: %s", pi.ID, string(pi.Status), declineMessage)
				w.Header().Set("HX-Trigger", "showModal")
				w.Header().Set("HX-Retarget", "#modal-content")
				w.Header().Set("HX-Reswap", "innerHTML")
				w.WriteHeader(http.StatusOK)
				component := checkout.PaymentDeclinedModal(declineMessage, pi.ID)
				if renderErr := component.Render(r.Context(), w); renderErr != nil {
					log.Printf("Error rendering payment declined (reader success, PI fail) modal: %v", renderErr)
				}
				return
			}

		case stripe.TerminalReaderActionStatusFailed:
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
			w.Header().Set("HX-Trigger", "showModal")
			w.Header().Set("HX-Retarget", "#modal-content")
			w.Header().Set("HX-Reswap", "innerHTML")
			w.WriteHeader(http.StatusOK)
			component := checkout.PaymentDeclinedModal(errMsg, intent.ID)
			if renderErr := component.Render(r.Context(), w); renderErr != nil {
				log.Printf("Error rendering reader action failed modal: %v", renderErr)
			}
			return

		case stripe.TerminalReaderActionStatusInProgress:
			log.Printf(
				"Terminal payment for PI %s on reader %s is InProgress. Switching to polling.",
				intent.ID,
				selectedReaderID,
			)

			// Store the active payment details for polling handlers
			activePayment := services.ActiveTerminalPayment{
				PaymentIntentID: intent.ID,
				ReaderID:        selectedReaderID,
				StartTime:       time.Now(),
				Email:           email, // Capture email from the form
				Cart:            make([]templates.Service, len(services.AppState.CurrentCart)),
				Summary:         summary, // Capture summary
			}
			copy(activePayment.Cart, services.AppState.CurrentCart)
			terminalPaymentStates[intent.ID] = activePayment // Store globally

			// Render a new template that will initiate polling
			w.Header().Set("HX-Trigger", "showModal") // Ensure modal is shown/stays shown
			w.Header().Set("HX-Retarget", "#modal-content")
			w.Header().Set("HX-Reswap", "innerHTML") // Replace modal content
			w.WriteHeader(http.StatusOK)

			pollingComponent := checkout.TerminalProcessingDisplay(intent.ID, selectedReaderID, summary.Total, email)
			if err := pollingComponent.Render(r.Context(), w); err != nil {
				log.Printf("Error rendering terminal polling display: %v", err)
				delete(terminalPaymentStates, intent.ID) // Clean up if render fails
				http.Error(w, "Failed to render terminal processing view", http.StatusInternalServerError)
			}
			return // Polling will take over

		default: // Handles other statuses like "unknown", etc.
			actualStatus := string(processedReader.Action.Status)
			log.Printf(
				"Default/unexpected case for terminal reader action status. PI %s on reader %s. Actual Status: '%s'",
				intent.ID,
				selectedReaderID,
				actualStatus,
			)

			modalMessage := fmt.Sprintf(
				"Payment status is currently unclear after initial terminal interaction (Status: %s). Please check the terminal or try again.",
				actualStatus,
			)
			w.Header().Set("HX-Trigger", "showModal")
			w.Header().Set("HX-Retarget", "#modal-content")
			w.Header().Set("HX-Reswap", "innerHTML")
			w.WriteHeader(http.StatusOK)
			component := checkout.PaymentDeclinedModal(modalMessage, intent.ID)
			if renderErr := component.Render(r.Context(), w); renderErr != nil {
				log.Printf("Error rendering default/unexpected action status modal: %v", renderErr)
			}
			return // paymentSuccess remains false
		}
		// If paymentSuccess is true (from TerminalReaderActionStatusSucceeded and PI succeeded),
		// it will fall through to the transaction saving logic.
		// If any case above returns, this part is skipped for that specific scenario.

	case "manual":
		// Process manual card payment with payment method
		paymentMethodID := r.FormValue("payment_token") // We're still using the payment_token field name for simplicity
		cardholder := r.FormValue("cardholder")

		if paymentMethodID == "" || cardholder == "" {
			w.Header().Set("HX-Trigger", `{"showToast": "Missing payment information"}`)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Attach the payment method directly to the intent
		piParams := &stripe.PaymentIntentConfirmParams{
			PaymentMethod: stripe.String(paymentMethodID),
		}

		_, err = paymentintent.Confirm(intent.ID, piParams)
		if err != nil {
			log.Printf("Error confirming payment intent: %v", err)
			w.Header().Set("HX-Trigger", `{"showToast": "Payment failed: `+err.Error()+`"}`)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		paymentSuccess = true

	case "qr":
		// In a real app, you'd check if the QR code payment was completed
		// For this example, we'll just simulate success
		paymentSuccess = true

	default:
		w.Header().Set("HX-Trigger", `{"showToast": "Invalid payment method"}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if !paymentSuccess {
		w.Header().Set("HX-Trigger", `{"showToast": "Payment processing failed"}`)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Create transaction record
	now := time.Now()
	transaction := templates.Transaction{
		ID:            intent.ID,
		Date:          now.Format("01/02/2006"),
		Time:          now.Format("15:04:05"),
		Services:      services.AppState.CurrentCart,
		Subtotal:      summary.Subtotal,
		Tax:           summary.Tax,
		Total:         summary.Total,
		PaymentType:   paymentMethod,
		CustomerEmail: email,
	}

	// Save transaction to CSV
	if err := services.SaveTransactionToCSV(transaction); err != nil {
		log.Printf("Error saving transaction to CSV: %v", err)
	}

	// Use centralized payment state clearing
	services.ClearPaymentLinkState()

	// Show success modal
	w.Header().Set("HX-Trigger", `{"showModal": true, "cartUpdated": true}`)
	w.Header().Set("HX-Retarget", "#modal-content")
	w.Header().Set("HX-Reswap", "innerHTML")
	w.WriteHeader(http.StatusOK) // Success response

	successComponent := checkout.PaymentSuccessModal(transaction)
	renderErr := successComponent.Render(r.Context(), w)
	if renderErr != nil {
		log.Printf("Error rendering payment success modal: %v", renderErr)
		// Similar to the decline modal, if Render fails, headers might be partially sent.
		// Just log the error. The client will likely receive a broken response.
		// Robust handling would buffer the output of Render.
	}

}

// ManualCardFormHandler renders the manual card form in a modal
func ManualCardFormHandler(w http.ResponseWriter, r *http.Request) {
	if len(services.AppState.CurrentCart) == 0 {
		w.Header().Set("HX-Trigger", `{"showToast": "Cart is empty. Please add items before proceeding to payment."}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Set trigger to show the modal
	w.Header().Set("HX-Trigger", "showModal")

	// Get Stripe public key
	stripePublicKey := services.GetStripePublicKey()

	component := checkout.ManualCardForm(stripePublicKey)
	err := component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Map to store payment link creation times
var paymentLinkCreationTimes = make(map[string]time.Time)

// GenerateQRCodeHandler generates a QR code for payment
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

	// Log payment link creation in transaction history
	linkTransaction := templates.Transaction{
		ID:                "link-" + time.Now().Format("20060102150405"),
		Date:              time.Now().Format("01/02/2006"),
		Time:              time.Now().Format("15:04:05"),
		Services:          []templates.Service{}, // Empty for link creation event
		PaymentType:       "qr-created",
		PaymentLinkID:     paymentLink.ID,
		PaymentLinkStatus: "active",
		ConfirmationCode:  paymentLink.ID,
		Total:             summary.Total,
		CustomerEmail:     email,
	}

	if err := services.SaveTransactionToCSV(linkTransaction); err != nil {
		log.Printf("Error saving payment link creation to CSV: %v", err)
	}

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

// CheckPaymentlinkStatusHandler checks the current status of a payment link and provides countdown
func CheckPaymentlinkStatusHandler(w http.ResponseWriter, r *http.Request) {
	paymentLinkID := r.URL.Query().Get("payment_link_id")
	if paymentLinkID == "" {
		w.Write([]byte(`<p class="status-message">Waiting for payment information...</p>`))
		return
	}

	// Check if this is a new payment link we haven't seen before
	if _, exists := paymentLinkCreationTimes[paymentLinkID]; !exists {
		// Store the current time as the creation time
		paymentLinkCreationTimes[paymentLinkID] = time.Now()
	}

	// Calculate time elapsed and remaining
	creationTime := paymentLinkCreationTimes[paymentLinkID]
	elapsed := time.Since(creationTime)
	expirationDuration := 120 * time.Second
	remaining := expirationDuration - elapsed

	// If time has expired, cancel the payment link
	if remaining <= 0 {
		// Deactivate the payment link in Stripe with a single line
		pl, err := paymentlink.Update(paymentLinkID, &stripe.PaymentLinkParams{Active: stripe.Bool(false)})
		if err != nil {
			log.Printf("Error expiring payment link: %v", err)
		}

		// Log the expiration in transaction history
		expireTransaction := templates.Transaction{
			ID:                "expire-" + time.Now().Format("20060102150405"),
			Date:              time.Now().Format("01/02/2006"),
			Time:              time.Now().Format("15:04:05"),
			Services:          []templates.Service{}, // No services for this entry
			PaymentType:       "qr-expired",
			PaymentLinkID:     paymentLinkID,
			PaymentLinkStatus: "expired",
			ConfirmationCode:  pl.ID,
			FailureReason:     "Expired after 120 seconds",
		}

		if err := services.SaveTransactionToCSV(expireTransaction); err != nil {
			log.Printf("Error saving expiration to CSV: %v", err)
		}

		// Return payment expired message
		expiredHTML := fmt.Sprintf(`
			<div class="payment-expired">
				<h3>Payment Link Expired ⌛</h3>
				<p>The payment link has expired and has been cancelled.</p>
				<p>Expiration Code: %s</p>
				<button
					type="button"
					class="retry-btn"
					hx-get="/checkout-form"
					hx-target="#payment-methods-container"
					hx-swap="innerHTML"
				>
					Try Again
				</button>
			</div>
		`, pl.ID)

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(expiredHTML))
		return
	}

	// Check payment link status
	paymentStatus, err := services.CheckPaymentLinkStatus(paymentLinkID)
	if err != nil {
		log.Printf("Error checking payment status: %v", err)
		w.Write([]byte(`<p class="status-message error">Error checking payment status</p>`))
		return
	}

	// Calculate countdown seconds (rounded)
	secondsRemaining := int(remaining.Seconds() + 0.5)

	// Calculate progress bar width as a percentage
	progressWidth := float64(secondsRemaining) / 120.0 * 100.0

	if paymentStatus.Completed {
		// Payment was successful! Process completion
		customerEmail := r.URL.Query().Get("customer_email")

		// Check if we already have contact info
		hasContactInfo := customerEmail != ""

		// Log the successful payment in transaction history
		successTransaction := templates.Transaction{
			ID:                "success-" + time.Now().Format("20060102150405"),
			Date:              time.Now().Format("01/02/2006"),
			Time:              time.Now().Format("15:04:05"),
			Services:          services.AppState.CurrentCart,
			PaymentType:       "qr-completed",
			PaymentLinkID:     paymentLinkID,
			PaymentLinkStatus: "completed",
			ConfirmationCode:  paymentLinkID,
			CustomerEmail:     customerEmail,
		}

		if err := services.SaveTransactionToCSV(successTransaction); err != nil {
			log.Printf("Error saving payment success to CSV: %v", err)
		}

		// Clear the cart after successful payment
		services.AppState.CurrentCart = []templates.Service{}

		// Replace the entire payment container with the success component
		// This completely eliminates the polling and the cancel button
		w.Header().Set("HX-Reswap", "outerHTML")
		w.Header().Set("HX-Retarget", "#payment-container")
		w.Header().Set("HX-Trigger", "cartUpdated")

		// Use the PaymentSuccess template component
		component := checkout.PaymentSuccess(paymentLinkID, hasContactInfo)
		if err := component.Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

		return
	} else if !paymentStatus.Active {
		// Link is inactive but no completed payment was found
		// This means it likely expired or was cancelled
		w.Write([]byte(`<p class="status-message">Payment link is no longer active.</p>`))
		return
	}

	// If we got here, payment is still pending
	statusHTML := fmt.Sprintf(`
		<div class="countdown-container">
			<p>QR code expires in <span id="countdown">%d</span> seconds</p>
			<div class="progress-bar">
				<div class="progress-fill" style="width: %.1f%%;"></div>
			</div>
		</div>
	`, secondsRemaining, progressWidth)

	w.Write([]byte(statusHTML))
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

	// Use centralized payment link state clearing
	services.ClearPaymentLinkState()

	// Log the cancellation in transaction history
	cancelTransaction := templates.Transaction{
		ID:                "cancel-" + time.Now().Format("20060102150405"),
		Date:              time.Now().Format("01/02/2006"),
		Time:              time.Now().Format("15:04:05"),
		Services:          []templates.Service{}, // No services for this entry
		PaymentType:       "qr-cancelled",
		PaymentLinkID:     paymentLinkID,
		PaymentLinkStatus: "cancelled",
		ConfirmationCode:  pl.ID,
	}

	if err := services.SaveTransactionToCSV(cancelTransaction); err != nil {
		log.Printf("Error saving cancellation to CSV: %v", err)
	}

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
		// Still show expired UI even if there's an error from Stripe
	}

	// Use centralized payment link state clearing
	services.ClearPaymentLinkState()

	// Log the expiration in transaction history
	expireTransaction := templates.Transaction{
		ID:                "expire-" + time.Now().Format("20060102150405"),
		Date:              time.Now().Format("01/02/2006"),
		Time:              time.Now().Format("15:04:05"),
		Services:          []templates.Service{}, // No services for this entry
		PaymentType:       "qr-expired",
		PaymentLinkID:     paymentLinkID,
		PaymentLinkStatus: "expired",
		ConfirmationCode:  pl.ID,
	}

	if err := services.SaveTransactionToCSV(expireTransaction); err != nil {
		log.Printf("Error saving expiration to CSV: %v", err)
	}

	// Render the expired payment template with the expiration code
	if err := checkout.PaymentExpired(pl.ID).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ReceiptInfoHandler handles updating receipt information
func ReceiptInfoHandler(w http.ResponseWriter, r *http.Request) {
	// Parse form
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	// Get the confirmation code and contact information
	confirmationCode := r.FormValue("confirmation_code")
	email := r.FormValue("receipt_email")
	phone := r.FormValue("receipt_phone")

	if confirmationCode == "" {
		w.Header().Set("HX-Trigger", `{"showToast": "Missing confirmation code"}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// If neither email nor phone was provided, return an error
	if email == "" && phone == "" {
		w.Header().Set("HX-Trigger", `{"showToast": "Please provide at least an email or phone number"}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Update transaction record with the contact information
	// In a real system, you would find the transaction in a database
	// For this example, we'll just log it and pretend it was updated

	log.Printf("Updating transaction %s with email: %s, phone: %s", confirmationCode, email, phone)

	// Simulate sending a receipt
	if email != "" {
		log.Printf("Sending receipt to email: %s", email)
		// In a real app, you would use an email service here
	}

	if phone != "" {
		log.Printf("Sending receipt to phone: %s", phone)
		// In a real app, you would use an SMS service here
	}

	// Return success message
	w.Header().Set(
		"HX-Trigger",
		`{"showToast": "Receipt information saved. Receipt will be sent shortly.", "closeModal": true}`,
	)
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`Receipt information saved`))
}

// CheckTerminalPaymentStatusHandler handles polling for terminal payment status
func CheckTerminalPaymentStatusHandler(w http.ResponseWriter, r *http.Request) {
	paymentIntentID := r.URL.Query().Get("payment_intent_id")
	// readerID := r.URL.Query().Get("reader_id") // Retained for potential future use or logging

	if paymentIntentID == "" {
		log.Println("CheckTerminalPaymentStatusHandler: payment_intent_id missing")
		http.Error(w, "PaymentIntent ID is required", http.StatusBadRequest)
		return
	}

	activePayment, found := terminalPaymentStates[paymentIntentID]
	if !found {
		log.Printf(
			"Terminal payment polling: PI %s not found in active states. It might have been completed, cancelled, or expired.",
			paymentIntentID,
		)
		component := checkout.TerminalInteractionResultModal(
			"Payment Status",
			"This payment session has concluded or is no longer active.",
			paymentIntentID,
			true,
			"",
		)
		w.Header().Set("HX-Reswap", "innerHTML") // Ensure the modal content is replaced
		w.Header().Set("HX-Retarget", "#modal-content")
		component.Render(r.Context(), w)
		return
	}

	// Check for timeout first
	if time.Since(activePayment.StartTime) > terminalPaymentTimeout {
		log.Printf(
			"Terminal payment PI %s on reader %s timed out after %v.",
			paymentIntentID,
			activePayment.ReaderID,
			terminalPaymentTimeout,
		)

		// Attempt to cancel the reader action if it's still associated with this reader
		_, err := reader.CancelAction(activePayment.ReaderID, &stripe.TerminalReaderCancelActionParams{})
		if err != nil {
			var stripeErr *stripe.Error
			if errors.As(err, &stripeErr) && stripeErr.Code == stripe.ErrorCode("terminal_reader_action_not_allowed") {
				log.Printf(
					"Error cancelling reader action for PI %s (Reader %s) on timeout: action not allowed (likely already completed/failed/cancelled). %v",
					paymentIntentID,
					activePayment.ReaderID,
					err,
				)
			} else {
				log.Printf("Error cancelling reader action for PI %s (Reader %s) on timeout: %v", paymentIntentID, activePayment.ReaderID, err)
				// Non-fatal, proceed to cancel PI
			}
		} else {
			log.Printf("Successfully sent cancel action to reader %s for timed-out PI %s.", activePayment.ReaderID, paymentIntentID)
		}

		// Attempt to cancel the Payment Intent as a fallback or primary cancellation method
		pi, cancelErr := paymentintent.Cancel(paymentIntentID, nil)
		if cancelErr != nil {
			log.Printf("Error cancelling PaymentIntent %s on timeout: %v", paymentIntentID, cancelErr)
		} else {
			log.Printf("Successfully cancelled PaymentIntent %s on timeout. Status: %s", pi.ID, pi.Status)
		}

		delete(terminalPaymentStates, paymentIntentID)
		component := checkout.TerminalInteractionResultModal(
			"Payment Timed Out",
			"The payment process on the terminal timed out and has been cancelled.",
			paymentIntentID,
			true,
			"",
		)
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("HX-Retarget", "#modal-content")
		component.Render(r.Context(), w)
		return
	}

	// Retrieve the PaymentIntent from Stripe
	intent, err := paymentintent.Get(paymentIntentID, nil)
	if err != nil {
		log.Printf("Error retrieving PaymentIntent %s during polling: %v", paymentIntentID, err)
		// Don't remove from activePayment yet, could be a temporary Stripe API issue
		// Render a message asking to wait or check terminal
		statusHTML := fmt.Sprintf(
			`<div class="status-message error">Error checking status (PI: %s). Please wait or check terminal. <br/>%s</div>`,
			paymentIntentID,
			err.Error(),
		)
		w.Write([]byte(statusHTML))
		return
	}

	nextActionStr := "N/A"
	if intent.NextAction != nil {
		nextActionStr = string(intent.NextAction.Type)
	}
	log.Printf(
		"Polling PI %s: Status - %s, Reader Action Status (if available) - %s",
		intent.ID,
		intent.Status,
		nextActionStr,
	)

	switch intent.Status {
	case stripe.PaymentIntentStatusSucceeded:
		log.Printf("Terminal PaymentIntent %s Succeeded (polled).", intent.ID)
		delete(terminalPaymentStates, paymentIntentID) // Clean up

		// Create transaction record (using data from activePayment)
		now := time.Now()
		transaction := templates.Transaction{
			ID:            intent.ID,
			Date:          now.Format("01/02/2006"),
			Time:          now.Format("15:04:05"),
			Services:      activePayment.Cart,
			Subtotal:      activePayment.Summary.Subtotal,
			Tax:           activePayment.Summary.Tax,
			Total:         activePayment.Summary.Total,
			PaymentType:   "terminal",
			CustomerEmail: activePayment.Email,
		}
		if err := services.SaveTransactionToCSV(transaction); err != nil {
			log.Printf("Error saving transaction to CSV for PI %s: %v", intent.ID, err)
		}
		services.AppState.CurrentCart = []templates.Service{} // Clear cart

		// Show success modal - replacing the polling display
		w.Header().Set("HX-Trigger", `{"showModal": true, "cartUpdated": true}`)
		w.Header().Set("HX-Retarget", "#modal-content")
		w.Header().Set("HX-Reswap", "innerHTML")
		successComponent := checkout.PaymentSuccessModal(transaction)
		successComponent.Render(r.Context(), w)
		return

	case stripe.PaymentIntentStatusProcessing,
		stripe.PaymentIntentStatusRequiresCapture,
		stripe.PaymentIntentStatusRequiresConfirmation,
		stripe.PaymentIntentStatusRequiresAction:
		// These statuses clearly indicate the payment is still legitimately in progress.
		log.Printf(
			"PI %s still pending (Status: %s). Reader: %s. Email: %s. Elapsed: %s",
			intent.ID, intent.Status, activePayment.ReaderID, activePayment.Email,
			time.Since(activePayment.StartTime).Round(time.Second),
		)
		// Render polling UI
		renderTerminalPollingInProgress(w, intent, activePayment)
		return

	case stripe.PaymentIntentStatusRequiresPaymentMethod:
		if intent.LastPaymentError != nil {
			// This is a failure after an attempt (e.g., card declined on terminal)
			declineMessage := fmt.Sprintf("Payment failed: %s", intent.LastPaymentError.Msg)
			if intent.LastPaymentError.Msg == "" { // Fallback if Stripe gives no specific message
				declineMessage = "Payment was declined by the terminal or an error occurred."
			}
			log.Printf("PI %s is RequiresPaymentMethod WITH LastPaymentError. Error: %s", intent.ID, declineMessage)
			renderTerminalFailureModal(w, r, intent.ID, declineMessage)
		} else {
			// This is likely the initial state, waiting for terminal input
			log.Printf("PI %s is RequiresPaymentMethod WITHOUT LastPaymentError. Continuing to poll.", intent.ID)
			renderTerminalPollingInProgress(w, intent, activePayment)
		}
		return

	case stripe.PaymentIntentStatusCanceled:
		log.Printf("Terminal PaymentIntent %s was Canceled (polled).", intent.ID)
		delete(terminalPaymentStates, paymentIntentID)
		component := checkout.TerminalInteractionResultModal(
			"Payment Cancelled",
			fmt.Sprintf("The payment (ID: %s) was cancelled.", intent.ID),
			intent.ID,
			true,
			"",
		)
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("HX-Retarget", "#modal-content")
		component.Render(r.Context(), w)
		return

	default: // Handles failed (e.g., from LastPaymentError), or other unexpected statuses.
		declineMessage := "Payment failed or was declined."
		if intent.LastPaymentError != nil && intent.LastPaymentError.Msg != "" {
			declineMessage = fmt.Sprintf("Payment failed: %s", intent.LastPaymentError.Msg)
		}
		log.Printf(
			"Terminal PaymentIntent %s failed or other status (polled): %s. Error: %s. Rendering declined modal.",
			intent.ID,
			intent.Status,
			declineMessage,
		)
		delete(terminalPaymentStates, paymentIntentID) // Clean up

		componentToRender := checkout.PaymentDeclinedModal(declineMessage, intent.ID)
		// Set headers for HTMX to replace the entire modal content
		w.Header().Set("HX-Retarget", "#modal-content")
		w.Header().Set("HX-Reswap", "innerHTML")
		// HX-Trigger to ensure modal visibility is handled by JS if needed,
		// though replacing content of an already visible modal should be fine.
		// If "showModal" causes issues by trying to re-display an active modal, it can be removed.
		w.Header().Set("HX-Trigger", `{"showModal": true}`)

		renderErr := componentToRender.Render(r.Context(), w)
		if renderErr != nil {
			log.Printf("CRITICAL: Error rendering PaymentDeclinedModal for PI %s: %v", intent.ID, renderErr)
			// Send a server error response to prevent HTMX from attempting a swap with a bad/empty body.
			// This should also stop the polling on the client side due to the error.
			http.Error(w, "Failed to render payment decline view", http.StatusInternalServerError)
		} else {
			log.Printf("Successfully rendered PaymentDeclinedModal for PI %s. Polling should stop.", intent.ID)
		}
		return
	}
}

// renderTerminalPollingInProgress is a helper to avoid duplicating the polling UI rendering.
func renderTerminalPollingInProgress(
	w http.ResponseWriter,
	intent *stripe.PaymentIntent,
	activePayment services.ActiveTerminalPayment,
) {
	secondsRemaining := int((terminalPaymentTimeout - time.Since(activePayment.StartTime)).Seconds() + 0.5)
	progressWidth := float64(secondsRemaining) / float64(terminalPaymentTimeout.Seconds()) * 100.0
	if secondsRemaining < 0 {
		secondsRemaining = 0
		progressWidth = 0
	}

	statusMessage := fmt.Sprintf("Processing on terminal... (Status: %s)", intent.Status)
	if intent.NextAction != nil &&
		intent.NextAction.Type == stripe.PaymentIntentNextActionType("display_terminal_receipt") {
		statusMessage = "Please take your receipt from the terminal."
	}

	pollingContent := fmt.Sprintf(`
		<div class="terminal-processing-info">
			<p>%s</p>
			<p>Time remaining: <span id="terminal-countdown">%d</span>s</p>
			<div class="progress-bar">
				<div class="progress-fill" style="width: %.1f%%;"></div>
			</div>
			<p><small>PI: %s, Reader: %s</small></p>
		</div>`,
		statusMessage, secondsRemaining, progressWidth, intent.ID, activePayment.ReaderID)
	w.Write([]byte(pollingContent))
}

// renderTerminalFailureModal handles the rendering of a failure/declined modal for terminal payments.
func renderTerminalFailureModal(
	w http.ResponseWriter,
	r *http.Request,
	paymentIntentID string,
	declineMessage string,
) {
	log.Printf(
		"Rendering terminal failure modal for PI %s. Message: %s",
		paymentIntentID,
		declineMessage,
	)
	delete(terminalPaymentStates, paymentIntentID) // Clean up using paymentIntentID

	componentToRender := checkout.PaymentDeclinedModal(declineMessage, paymentIntentID)
	w.Header().Set("HX-Retarget", "#modal-content")
	w.Header().Set("HX-Reswap", "innerHTML")
	w.Header().Set("HX-Trigger", `{"showModal": true}`) // Ensure modal is shown or remains visible

	renderErr := componentToRender.Render(r.Context(), w)
	if renderErr != nil {
		log.Printf("CRITICAL: Error rendering PaymentDeclinedModal for PI %s: %v", paymentIntentID, renderErr)
		http.Error(w, "Failed to render payment decline view", http.StatusInternalServerError)
	} else {
		log.Printf("Successfully rendered PaymentDeclinedModal for PI %s. Polling should stop.", paymentIntentID)
	}
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

	activePayment, found := services.ActiveTerminalPayments[paymentIntentID]
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
			"/close-modal",
		)
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("HX-Retarget", "#modal-content")
		component.Render(r.Context(), w)
		return
	}

	log.Printf("User initiated cancel for PI %s on reader %s.", paymentIntentID, activePayment.ReaderID)

	// Attempt to cancel the reader action first
	_, err := reader.CancelAction(activePayment.ReaderID, &stripe.TerminalReaderCancelActionParams{})
	if err != nil {
		var stripeErr *stripe.Error
		if errors.As(err, &stripeErr) && stripeErr.Code == stripe.ErrorCode("terminal_reader_action_not_allowed") {
			log.Printf(
				"Error cancelling reader action for PI %s (Reader %s): action not allowed (likely already completed/failed/cancelled). %v",
				paymentIntentID,
				activePayment.ReaderID,
				err,
			)
		} else {
			log.Printf("Error cancelling reader action for PI %s (Reader %s): %v", paymentIntentID, activePayment.ReaderID, err)
		}
		// Non-fatal, proceed to cancel PI.
	} else {
		log.Printf("Successfully sent cancel action to reader %s for PI %s.", activePayment.ReaderID, paymentIntentID)
	}

	// Then, cancel the Payment Intent itself
	pi, cancelErr := paymentintent.Cancel(paymentIntentID, nil)
	if cancelErr != nil {
		log.Printf("Error cancelling PaymentIntent %s due to user request: %v", paymentIntentID, cancelErr)
		// Show error, but still clear payment state
		services.ClearSpecificPaymentState(paymentIntentID, activePayment.ReaderID)
		errMsg := fmt.Sprintf("Error trying to cancel payment (ID: %s): %s", paymentIntentID, cancelErr.Error())
		component := checkout.TerminalInteractionResultModal(
			"Cancellation Error",
			errMsg,
			paymentIntentID,
			true,
			"/close-modal",
		)
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("HX-Retarget", "#modal-content")
		component.Render(r.Context(), w)
		return
	}

	log.Printf("Successfully cancelled PaymentIntent %s due to user request. Status: %s", pi.ID, pi.Status)
	
	// Use centralized payment state clearing
	services.ClearSpecificPaymentState(paymentIntentID, activePayment.ReaderID)

	// Render a success message for cancellation
	msg := fmt.Sprintf("Payment (ID: %s) has been cancelled as requested.", pi.ID)
	component := checkout.TerminalInteractionResultModal("Payment Cancelled", msg, pi.ID, true, "/close-modal")
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

	activePayment, found := services.ActiveTerminalPayments[paymentIntentID]
	if !found {
		// Already handled (e.g. succeeded, cancelled by user, or previously expired)
		log.Printf(
			"ExpireTerminalPaymentHandler: PI %s not found in active states. Likely already concluded.",
			paymentIntentID,
		)
		// It's okay for this to be a no-op if not found, the client trigger is just a safety.
		// However, to provide feedback if the modal is still showing this, we can render a generic "ended" message.
		component := checkout.TerminalInteractionResultModal(
			"Payment Expired",
			fmt.Sprintf("The payment session (ID: %s) has expired or was already concluded.", paymentIntentID),
			paymentIntentID,
			true,
			"/close-modal",
		)
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("HX-Retarget", "#modal-content")
		component.Render(r.Context(), w)
		return
	}

	log.Printf("Automatic expiration for PI %s on reader %s.", paymentIntentID, activePayment.ReaderID)

	// Similar logic to timeout in polling: try to cancel reader, then PI.
	_, errReader := reader.CancelAction(activePayment.ReaderID, &stripe.TerminalReaderCancelActionParams{})
	if errReader != nil {
		var stripeErr *stripe.Error
		if errors.As(errReader, &stripeErr) &&
			stripeErr.Code == stripe.ErrorCode("terminal_reader_action_not_allowed") {
			log.Printf(
				"Error cancelling reader action for expired PI %s (Reader %s): action not allowed. %v",
				paymentIntentID,
				activePayment.ReaderID,
				errReader,
			)
		} else {
			log.Printf("Error cancelling reader action for expired PI %s (Reader %s): %v", paymentIntentID, activePayment.ReaderID, errReader)
		}
	} else {
		log.Printf("Successfully sent cancel action to reader %s for expired PI %s.", activePayment.ReaderID, paymentIntentID)
	}

	pi, errPI := paymentintent.Cancel(paymentIntentID, nil)
	if errPI != nil {
		log.Printf("Error cancelling PaymentIntent %s on auto-expiration: %v", paymentIntentID, errPI)
	} else {
		log.Printf("Successfully cancelled PaymentIntent %s on auto-expiration. Status: %s", pi.ID, pi.Status)
	}

	// Use centralized payment state clearing
	services.ClearSpecificPaymentState(paymentIntentID, activePayment.ReaderID)

	msg := fmt.Sprintf("The payment (ID: %s) has expired due to inactivity and has been cancelled.", paymentIntentID)
	component := checkout.TerminalInteractionResultModal("Payment Expired", msg, paymentIntentID, true, "/close-modal")
	w.Header().Set("HX-Reswap", "innerHTML")
	w.Header().Set("HX-Retarget", "#modal-content")
	component.Render(r.Context(), w)
}
