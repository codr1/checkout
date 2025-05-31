package handlers

import "time"

// PaymentPollingConfig constants for consistent configuration
const (
	// Polling intervals
	PAYMENT_POLLING_INTERVAL = "2s"
	
	// Timeout durations - unified for all payment types  
	// Backend timeout for progress calculations and polling logic
	PAYMENT_TIMEOUT = 120 * time.Second
	
	// Frontend HTMX auto-expire delay (same as timeout for consistency)
	// This acts as a safety net if browser closes or polling stops
	PAYMENT_EXPIRE_DELAY = "120s"
	
	// Polling endpoints
	QR_POLL_ENDPOINT      = "/check-paymentlink-status"
	TERMINAL_POLL_ENDPOINT = "/check-terminal-payment-status"
	
	// Expiration endpoints
	QR_EXPIRE_ENDPOINT      = "/expire-payment-link"
	TERMINAL_EXPIRE_ENDPOINT = "/expire-terminal-payment"
	
	// Cancel endpoints
	QR_CANCEL_ENDPOINT      = "/cancel-payment-link"
	TERMINAL_CANCEL_ENDPOINT = "/cancel-terminal-payment"
)

// PaymentProgressMessages provides consistent status messages
var PaymentProgressMessages = map[string]map[string]string{
	"qr": {
		"default":     "Waiting for QR code scan...",
		"processing":  "Processing QR payment...",
		"scanning":    "Please scan the QR code with your camera app",
	},
	"terminal": {
		"default":     "Processing on terminal...",
		"processing":  "Please complete the transaction on the payment terminal",
		"waiting":     "Waiting for terminal interaction...",
		"receipt":     "Please take your receipt from the terminal",
	},
}

// GetPaymentMessage retrieves the appropriate message for a payment type and status
func GetPaymentMessage(paymentType, status string) string {
	if messages, exists := PaymentProgressMessages[paymentType]; exists {
		if message, exists := messages[status]; exists {
			return message
		}
		return messages["default"]
	}
	return "Processing payment..."
}

