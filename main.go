package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/balance"
	"github.com/stripe/stripe-go/v74/webhookendpoint"

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

	// Set up communication strategy (polling vs webhooks)
	registerWebhookEndpoint()
}

// generateSelfSignedCert creates a self-signed certificate for localhost
func generateSelfSignedCert() (tls.Certificate, error) {
	// Generate a private key
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  []string{"PicklePOS Development"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{""},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour), // 1 year
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:    []string{"localhost"},
	}

	// Create the certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	// Create TLS certificate
	cert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  priv,
	}

	return cert, nil
}

// shouldUseHTTPS determines if HTTPS should be used based on websiteName config
func shouldUseHTTPS() bool {
	websiteName := strings.TrimSpace(config.Config.WebsiteName)

	// Use HTTPS if no domain configured or domain is localhost
	// (for local testing with Stripe.js)
	return websiteName == "" || websiteName == "localhost"
}

// getCommunicationStrategy determines whether to use polling or webhooks
func getCommunicationStrategy() string {
	websiteName := strings.TrimSpace(config.Config.WebsiteName)
	if websiteName != "" && websiteName != "localhost" {
		return "webhooks"
	}
	return "polling"
}

// registerWebhookEndpoint registers webhook endpoint with Stripe if using webhooks strategy
func registerWebhookEndpoint() {
	strategy := getCommunicationStrategy()
	if strategy != "webhooks" {
		log.Printf("[Communication] Using polling strategy (localhost/no domain)")
		return
	}

	// Check if webhook secret is configured
	webhookSecret := config.GetStripeWebhookSecret()
	if webhookSecret == "" {
		log.Printf("[Warning] Webhook strategy selected but no webhook secret configured")
		return
	}

	// TODO: Consider persisting webhook registration to survive server restarts
	// For now, we'll register on each startup which is acceptable for development

	websiteName := config.Config.WebsiteName
	webhookURL := "https://" + websiteName + "/stripe-webhook"

	// Events we need for our POS system
	enabledEvents := []string{
		"payment_intent.succeeded",
		"payment_intent.payment_failed",
		"payment_intent.canceled",
		"payment_intent.requires_action",
		"payment_link.completed",
		"payment_link.updated",
		"terminal.reader.action_succeeded",
		"terminal.reader.action_failed",
		"charge.succeeded",
		"charge.failed",
	}

	params := &stripe.WebhookEndpointParams{
		URL:           stripe.String(webhookURL),
		EnabledEvents: stripe.StringSlice(enabledEvents),
	}

	result, err := webhookendpoint.New(params)
	if err != nil {
		log.Printf("[Error] Failed to register webhook endpoint: %v", err)
		log.Printf("[Info] Falling back to polling mode")
		return
	}

	log.Printf("[Communication] Using webhook strategy")
	log.Printf("[Webhook] Registered endpoint: %s", webhookURL)
	log.Printf("[Webhook] Endpoint ID: %s", result.ID)
	log.Printf("[Webhook] Events: %v", enabledEvents)
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


	// Payment events endpoint - SSE for real-time payment updates
	rootMux.HandleFunc("/payment-events", handlers.PaymentSSEHandler)


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
	appMux.HandleFunc("/cancel-transaction", handlers.CancelTransactionHandler)
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

	// Determine protocol and start appropriate server
	if shouldUseHTTPS() {
		log.Printf(
			"No domain configured (websiteName: '%s') - starting HTTPS server on port %s for local testing...",
			config.Config.WebsiteName,
			port,
		)
		log.Printf("⚠️  You will need to accept the security warning in your browser for the self-signed certificate")
		log.Printf("🔗 Access your application at: https://localhost:%s", port)

		// Generate self-signed certificate
		cert, err := generateSelfSignedCert()
		if err != nil {
			log.Fatalf("Failed to generate self-signed certificate: %v", err)
		}

		// Create HTTPS server
		server := &http.Server{
			Addr:    ":" + port,
			Handler: rootMux,
			TLSConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
			},
		}

		log.Fatal(server.ListenAndServeTLS("", ""))
	} else {
		log.Printf("Domain configured (websiteName: '%s') - starting HTTP server on port %s for cloudflared...", config.Config.WebsiteName, port)
		log.Printf("🔗 Expected to be accessed via cloudflared tunnel or reverse proxy")
		log.Printf("🔗 Local HTTP access: http://localhost:%s", port)

		log.Fatal(http.ListenAndServe(":"+port, rootMux))
	}
}
