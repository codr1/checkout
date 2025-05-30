package handlers

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

	"checkout/config"

	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/webhook"
)

// StripeWebhookHandler processes Stripe webhook events
func StripeWebhookHandler(w http.ResponseWriter, r *http.Request) {
	// Read request body
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading webhook body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Get Stripe signature from header
	sigHeader := r.Header.Get("Stripe-Signature")
	webhookSecret := config.GetStripeWebhookSecret()
	
	if webhookSecret == "" {
		log.Printf("Warning: Stripe webhook secret not configured")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Verify signature
	event, err := webhook.ConstructEvent(payload, sigHeader, webhookSecret)
	if err != nil {
		log.Printf("Webhook signature verification failed: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Handle different event types
	switch event.Type {
	case "payment_intent.created":
		var intent stripe.PaymentIntent
		err := json.Unmarshal(event.Data.Raw, &intent)
		if err != nil {
			log.Printf("Error parsing webhook JSON: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		
		log.Printf("Payment intent created: %s", intent.ID)
		
		// TODO: Store the intent creation time for cleanup of abandoned intents
		// This would be implemented with a background worker that periodically checks
		// for abandoned payment intents and cancels them

	case "payment_intent.succeeded":
		var intent stripe.PaymentIntent
		err := json.Unmarshal(event.Data.Raw, &intent)
		if err != nil {
			log.Printf("Error parsing webhook JSON: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		
		log.Printf("Payment intent succeeded: %s", intent.ID)
		
		// TODO: You could update order status, send confirmation emails, etc.

	case "payment_intent.payment_failed":
		var intent stripe.PaymentIntent
		err := json.Unmarshal(event.Data.Raw, &intent)
		if err != nil {
			log.Printf("Error parsing webhook JSON: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		
		// Safely log the error message if available
		errorMessage := "unknown error"
		if intent.LastPaymentError != nil {
			errorMessage = string(intent.LastPaymentError.Type)
		}
		log.Printf("Payment intent failed: %s, reason: %s", intent.ID, errorMessage)
		
		// TODO: Handle failed payments (e.g., notify customer, retry)

	case "checkout.session.completed":
		var session stripe.CheckoutSession
		err := json.Unmarshal(event.Data.Raw, &session)
		if err != nil {
			log.Printf("Error parsing webhook JSON: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		
		log.Printf("Checkout session completed: %s", session.ID)
		
		// TODO: Handle completed checkout sessions (fulfill orders)

	default:
		log.Printf("Unhandled event type: %s", event.Type)
	}

	// Return a success response to Stripe
	w.WriteHeader(http.StatusOK)
}

