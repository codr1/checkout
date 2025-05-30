package main

import (
	"log"
	"net/http"
	"os"

	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/balance"

	"checkout/config"
	"checkout/handlers"
	"checkout/services"
)

// Configuration
const (
	PORT             = "3000"
	DATA_DIR         = "./data"
	TRANSACTIONS_DIR = "./data/transactions"
)

// Initialize the application
func init() {
	// Load configuration
	if err := config.Load(); err != nil {
		log.Fatal(err)
	}

	// Check if password is strong enough
	if len(config.Config.Password) < 8 {
		log.Fatal("Password must be at least 8 characters long. Please update your configuration.")
	}

	// Create data directories if they don't exist
	// Use directories from config or fallback to constants
	dataDir := config.Config.DataDir
	if dataDir == "" {
		dataDir = DATA_DIR
	}

	transactionsDir := config.Config.TransactionsDir
	if transactionsDir == "" {
		transactionsDir = TRANSACTIONS_DIR
	}

	os.MkdirAll(dataDir, 0755)
	os.MkdirAll(transactionsDir, 0755)

	// Initialize Stripe with API key from config or environment variable
	stripe.Key = config.GetStripeKey()
	if stripe.Key == "" {
		log.Fatal(
			"Missing Stripe Secret Key in config or environment. Please set STRIPE_SECRET_KEY environment variable or configure it in the config file.",
		)
	}

	// Load services from JSON
	if err := services.LoadServices(); err != nil {
		log.Printf("Error loading services: %v", err)
		// Depending on severity, you might want to log.Fatal(err) here
	}

	// Load Stripe Terminal Locations and select one
	services.LoadStripeLocationsAndSelect()

	// If a location is selected, load its readers
	if services.AppState.SelectedStripeLocation.ID != "" {
		services.LoadStripeReadersForLocation(services.AppState.SelectedStripeLocation.ID)
	} else {
		log.Println("[MainInit] No Stripe Terminal Location selected. Terminal reader-specific functionalities might be limited.")
	}

	// Test the Stripe API key by making a simple API call
	_, err := balance.Get(nil)
	if err != nil {
		log.Fatalf(
			"Stripe API key is invalid or not working: %v\nPlease ensure your Stripe Secret Key is correct in the environment variable or config file.",
			err,
		)
	}

	log.Println("Stripe API initialized successfully")
}

func main() {
	rootMux := http.NewServeMux()

	// Static files: Publicly accessible
	// Ensure the path to static files is correct, e.g., "./static"
	// If your static directory is in the root of your project, http.Dir("./static") is correct.
	rootMux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	// Auth routes: Publicly accessible for login/logout
	rootMux.HandleFunc("/login", handlers.LoginHandler)
	rootMux.HandleFunc("/logout", handlers.LogoutHandler)

	// Stripe webhook handler: Public, but typically has its own signature verification, not session auth
	rootMux.HandleFunc("/stripe-webhook", handlers.StripeWebhookHandler)

	// Application-specific routes that require authentication will go into appMux
	appMux := http.NewServeMux()

	// API routes (protected)
	appMux.HandleFunc("/services", handlers.ServicesHandler)
	appMux.HandleFunc("/cart", handlers.CartHandler)
	appMux.HandleFunc("/checkout-form", handlers.CheckoutFormHandler)
	appMux.HandleFunc("/add-to-cart", handlers.AddToCartHandler)
	appMux.HandleFunc("/add-custom-service", handlers.AddCustomServiceHandler)
	appMux.HandleFunc("/remove-from-cart", handlers.RemoveFromCartHandler)
	appMux.HandleFunc("/process-payment", handlers.ProcessPaymentHandler)
	appMux.HandleFunc("/generate-qr-code", handlers.GenerateQRCodeHandler)
	appMux.HandleFunc("/manual-card-form", handlers.ManualCardFormHandler)
	appMux.HandleFunc("/check-paymentlink-status", handlers.CheckPaymentlinkStatusHandler)
	appMux.HandleFunc("/cancel-payment-link", handlers.CancelPaymentLinkHandler)
	appMux.HandleFunc("/expire-payment-link", handlers.ExpirePaymentLinkHandler)
	appMux.HandleFunc("/update-receipt-info", handlers.ReceiptInfoHandler)

	// Terminal Payment Polling Endpoints
	appMux.HandleFunc("/check-terminal-payment-status", handlers.CheckTerminalPaymentStatusHandler)
	appMux.HandleFunc("/cancel-terminal-payment", handlers.CancelTerminalPaymentHandler)
	appMux.HandleFunc("/expire-terminal-payment", handlers.ExpireTerminalPaymentHandler)
	appMux.HandleFunc("/clear-terminal-transaction", handlers.ClearTerminalTransactionHandler)

	// POS Page specific handlers
	appMux.HandleFunc("/set-selected-reader", handlers.SetSelectedReaderHandler)

	// Modal closing endpoint (assuming it's part of the authenticated UI)
	// If it can be public, it could also be on rootMux.
	appMux.HandleFunc("/close-modal", func(w http.ResponseWriter, r *http.Request) {
		// Send HX-Trigger header to close the modal
		w.Header().Set("HX-Trigger", "closeModal")
		w.WriteHeader(http.StatusOK)
	})

	// Main application route (POS): Requires authentication
	// This will handle requests to "/" after authentication.
	appMux.HandleFunc("/", handlers.POSHandler)

	// Apply auth middleware only to appMux routes.
	// rootMux.Handle("/", ...) will catch all requests not already handled by rootMux
	// (like /static/, /login, etc.) and pass them to the authedAppHandler.
	authedAppHandler := handlers.AuthMiddleware(appMux)
	rootMux.Handle("/", authedAppHandler)

	// Start server using port from config or default
	port := config.Config.Port
	if port == "" {
		port = PORT
	}
	log.Printf("Starting server on port %s...", port)
	// Use rootMux as the main handler for the server
	log.Fatal(http.ListenAndServe(":"+port, rootMux))
}
