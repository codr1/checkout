package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/paymentintent"

	"checkout/config"
	"checkout/services"
	"checkout/templates"
	"checkout/templates/checkout"
	"checkout/utils"
)

// Modal Helper Functions - Generic modal rendering utilities
// These functions eliminate the repeated HTMX modal pattern found throughout the payment handlers

// renderModal renders any templ component in a modal with proper HTMX headers
// This is the core abstraction that eliminates 15+ instances of duplicated modal code
func renderModal(
	w http.ResponseWriter,
	r *http.Request,
	component templ.Component,
	additionalTriggers ...string,
) error {
	// Set the standard HTMX headers for modal display
	w.Header().Set("HX-Retarget", "#modal-content") // Target the modal content div
	w.Header().Set("HX-Reswap", "innerHTML")        // Replace content inside the div

	// Handle different trigger patterns - simple vs complex triggers
	var trigger string
	if len(additionalTriggers) > 0 {
		// Complex trigger: {"showModal": true, "cartUpdated": true, ...}
		triggers := append([]string{`"showModal": true`}, additionalTriggers...)
		trigger = "{" + strings.Join(triggers, ", ") + "}"
	} else {
		// Simple trigger: showModal (no quotes for simple triggers)
		trigger = `showModal`
	}
	w.Header().Set("HX-Trigger", trigger)

	w.WriteHeader(http.StatusOK)
	return component.Render(r.Context(), w)
}

// renderErrorModal - Specialized helper for error cases
// Replaces the common pattern of showing PaymentDeclinedModal with error messages
func renderErrorModal(w http.ResponseWriter, r *http.Request, message, id string) error {
	utils.Debug("payment", "Rendering error modal", "message", message, "id", id)
	return renderModal(w, r, checkout.PaymentDeclinedModal(message, id))
}

// renderSuccessModal - Specialized helper for success cases
// Replaces the common pattern of showing success modals with cart updates
func renderSuccessModal(w http.ResponseWriter, r *http.Request, paymentID string, hasEmail bool) error {
	utils.Info("payment", "Rendering success modal", "payment_id", paymentID, "has_email", hasEmail)
	// Always show receipt form after payment completion
	return renderModal(w, r, checkout.PaymentSuccess(paymentID), `"cartUpdated": true`)
}

// renderInfoModal - Specialized helper for informational modals
// For cases where we need to show information without error/success semantics
func renderInfoModal(w http.ResponseWriter, r *http.Request, component templ.Component) error {
	return renderModal(w, r, component)
}

// ProcessPaymentHandler handles payment processing
func ProcessPaymentHandler(w http.ResponseWriter, r *http.Request) {
	if len(services.AppState.CurrentCart) == 0 {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Cart is empty", "type": "warning"}}`)
		w.WriteHeader(http.StatusOK) // Changed from BadRequest to OK since this is a valid user action
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	paymentMethod := r.FormValue("payment_method")

	// Calculate cart summary with taxes
	summary := services.CalculateCartSummary()

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

	intent, err := paymentintent.New(params)
	if err != nil {
		utils.Error("payment", "Error creating payment intent", "payment_method", paymentMethod, "amount", summary.Total, "error", err)
		w.Header().Set("HX-Trigger", `{"showToast": "Error processing payment"}`)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var paymentSuccess bool

	// Process payment based on method
	switch paymentMethod {
	case "terminal":
		// Delegate all terminal processing to payment_terminal.go
		result := ProcessTerminalPayment(w, r, intent, "", summary)
		if result.ShouldStop {
			if result.PaymentSuccess {
				paymentSuccess = true
				if result.UpdatedIntent != nil {
					intent = result.UpdatedIntent // Use updated intent from terminal processing
				}
			}
			if !result.Success {
				return // Terminal processing handled the response
			}
		}

	case "manual":
		// Manual card processing - this would typically involve a form for card details
		// For now, we'll redirect to the manual card form
		if renderErr := renderInfoModal(w, r, checkout.ManualCardForm(intent.ID)); renderErr != nil {
			utils.Error("payment", "Error rendering manual card form", "intent_id", intent.ID, "error", renderErr)
		}
		return

	case "qr":
		// QR code payment processing is handled in payment_qr.go
		// This should redirect to QR code generation
		http.Redirect(
			w,
			r,
			fmt.Sprintf("/generate-qr-code?intent_id=%s", intent.ID),
			http.StatusSeeOther,
		)
		return

	default:
		w.Header().Set("HX-Trigger", `{"showToast": "Invalid payment method"}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Handle successful payment (terminal immediate success)
	if paymentSuccess {
		// Log the successful transaction (no email - will be collected post-payment)
		_ = GlobalPaymentEventLogger.LogPaymentEvent(
			intent.ID,
			PaymentEventSuccess,
			paymentMethod,
			services.AppState.CurrentCart,
			summary,
			"", // No email - will be collected post-payment via receipt form
		)

		// Clear cart
		services.AppState.CurrentCart = []templates.Service{}

		// Show success modal (always show receipt form)
		if renderErr := renderSuccessModal(w, r, intent.ID, false); renderErr != nil {
			utils.Error("payment", "Error rendering payment success modal", "intent_id", intent.ID, "error", renderErr)
		}
	}
}

// ReceiptInfoHandler handles receipt information updates and sending
func ReceiptInfoHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	confirmationCode := r.FormValue("confirmation_code")
	email := r.FormValue("receipt_email")
	phone := r.FormValue("receipt_phone")

	// Debug: Log what we received to understand the current form structure
	utils.Debug("receipt", "ReceiptInfoHandler called", "method", r.Method, "confirmation_code", confirmationCode, "email", email, "phone", phone)

	// Validate that at least email is provided (phone only if SMS is enabled)
	if email == "" {
		if phone != "" && !config.IsSMSEnabled() {
			renderReceiptError(w, "Please provide an email address. SMS receipts are not currently enabled.")
		} else {
			renderReceiptError(w, "Please provide an email address.")
		}
		return
	}

	// Determine delivery method
	var deliveryMethod string
	if email != "" && phone != "" {
		deliveryMethod = "both"
	} else if email != "" {
		deliveryMethod = "email"
	} else {
		deliveryMethod = "sms"
	}

	// Create initial receipt record
	receiptRecord := services.CreateReceiptRecord(confirmationCode, email, phone, deliveryMethod, "pending")
	if err := services.SaveReceiptRecord(receiptRecord); err != nil {
		utils.Error("receipt", "Error saving receipt record", "confirmation_code", confirmationCode, "error", err)
		renderReceiptError(w, "Error recording receipt request. Please try again.")
		return
	}

	// Simulate receipt sending (replace with actual email/SMS service)
	var sentMethod string
	var sendError error

	if email != "" {
		// Send email receipt
		sendError = sendEmailReceipt(confirmationCode, email)
		if sendError == nil {
			sentMethod = "email"
		}
	}

	if phone != "" && sendError == nil && config.IsSMSEnabled() {
		// Send SMS receipt (only if SMS is enabled)
		smsError := sendSMSReceipt(confirmationCode, phone)
		if smsError == nil {
			if sentMethod == "" {
				sentMethod = "SMS"
			} else {
				sentMethod = "email and SMS"
			}
		} else if sendError == nil {
			sendError = smsError // Only set error if email didn't already fail
		}
	}

	// Update receipt delivery status
	var finalStatus string
	var errorMessage string
	if sendError != nil {
		finalStatus = "failed"
		errorMessage = sendError.Error()
		utils.Error("receipt", "Error sending receipt", "confirmation_code", confirmationCode, "method", deliveryMethod, "error", sendError)

		// Log the failure
		_ = services.UpdateReceiptDeliveryStatus(confirmationCode, finalStatus, errorMessage)

		renderReceiptError(w, "Failed to send receipt. Please check your contact information and try again.")
		return
	} else {
		finalStatus = "sent"
		// Log the success
		_ = services.UpdateReceiptDeliveryStatus(confirmationCode, finalStatus, "")
	}

	// Success - render success component
	utils.Info("receipt", "Receipt sent successfully", "confirmation_code", confirmationCode, "method", sentMethod)
	renderReceiptSuccess(w, sentMethod)
}

// sendEmailReceipt simulates sending an email receipt
func sendEmailReceipt(confirmationCode, email string) error {
	// TODO: Replace with actual email service (SendGrid, AWS SES, etc.)
	utils.Debug("receipt", "Sending email receipt", "confirmation_code", confirmationCode, "email", email)

	// Simulate potential failure for testing (remove this in production)
	// Fail if email contains "fail" for demonstration purposes
	if strings.Contains(strings.ToLower(email), "fail") {
		return fmt.Errorf("simulated email sending failure")
	}

	// For now, always succeed for demonstration
	return nil
}

// sendSMSReceipt simulates sending an SMS receipt
func sendSMSReceipt(confirmationCode, phone string) error {
	// TODO: Replace with actual SMS service (Twilio, AWS SNS, etc.)
	utils.Debug("receipt", "Sending SMS receipt", "confirmation_code", confirmationCode, "phone", phone)

	// Simulate potential failure for testing (remove this in production)
	// Fail if phone contains "fail" for demonstration purposes
	if strings.Contains(strings.ToLower(phone), "fail") {
		return fmt.Errorf("simulated SMS sending failure")
	}

	// For now, always succeed for demonstration
	return nil
}

// renderReceiptSuccess renders the receipt success component
func renderReceiptSuccess(w http.ResponseWriter, method string) {
	// Instead of trying to update the DOM directly, use HX-Trigger to close the modal
	// and show a green success toast notification

	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"closeModal": true, "showToastSuccess": "Receipt sent to %s!"}`, method))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("")) // Empty response since we're just triggering events
}

// renderReceiptError renders the receipt error component
func renderReceiptError(w http.ResponseWriter, errorMessage string) {
	// For errors, show a toast notification and keep the modal open
	// This allows the user to try again without losing their input

	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": "%s"}`, errorMessage))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("")) // Empty response since we're just showing a toast
}

// State management utilities

// ClearPaymentStates clears all payment-related state
// ClearPaymentStates clears all payment-related state
func ClearPaymentStates() {
	GlobalPaymentStateManager.ClearAll()
	utils.Info("payment", "All payment states cleared")
}

// ClearExpiredPaymentStates removes expired payment states
func ClearExpiredPaymentStates() {
	GlobalPaymentStateManager.CleanupExpired()
	utils.Info("payment", "Expired payment states cleared")
}

// GetActivePaymentStatesCount returns the number of active payment states
func GetActivePaymentStatesCount() (int, int) {
	return GlobalPaymentStateManager.GetActiveCountByType()
}
