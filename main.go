package main

import (
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/paymentintent"
	"github.com/stripe/stripe-go/v74/paymentlink"
	"github.com/stripe/stripe-go/v74/paymentmethod"
	"github.com/stripe/stripe-go/v74/price"
	"github.com/stripe/stripe-go/v74/product"
	"github.com/stripe/stripe-go/v74/terminal/reader"

	"checkout/config"
	"checkout/templates"
)

// Configuration
const (
	PORT             = "3000"
	PIN              = "1234" // In production, use proper authentication
	DATA_DIR         = "./data"
	TRANSACTIONS_DIR = "./data/transactions"
)

// TODO: Replace hardcoded tax calculation with Stripe Tax API

// AppState holds application state
type AppState struct {
	Services    []templates.Service
	CurrentCart []templates.Service
}

var appState AppState

// Initialize the application
func init() {
	// Load configuration
	if err := config.Load(); err != nil {
		log.Fatal(err)
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

	// Load services from JSON
	loadServices()

	// Initialize Stripe with API key from config or environment variable
	stripe.Key = config.GetStripeKey()
	if stripe.Key == "" {
		log.Fatal("Missing Stripe Secret Key and config or environment")
	}
}

// Load services from the JSON file and ensure each service has a Stripe price ID
func loadServices() {
	// Use data directory from config or fallback to constant
	dataDir := config.Config.DataDir
	if dataDir == "" {
		dataDir = DATA_DIR
	}
	servicesFilePath := filepath.Join(dataDir, "services.json")

	// Create default services if file doesn't exist
	if _, err := os.Stat(servicesFilePath); os.IsNotExist(err) {
		defaultServices := []templates.Service{
			{ID: "1", Name: "Service 1", Description: "Basic service", Price: 50.00},
			{ID: "2", Name: "Service 2", Description: "Premium service", Price: 100.00},
			{ID: "3", Name: "Service 3", Description: "Deluxe service", Price: 150.00},
		}

		// Ensure each service has a Stripe priceID
		for i := range defaultServices {
			ensureServiceHasPriceID(&defaultServices[i])
		}

		jsonData, _ := json.MarshalIndent(defaultServices, "", "  ")
		os.WriteFile(servicesFilePath, jsonData, 0644)

		appState.Services = defaultServices
		return
	}

	// Read existing services
	data, err := os.ReadFile(servicesFilePath)
	if err != nil {
		log.Printf("Error reading services: %v", err)
		return
	}

	if err := json.Unmarshal(data, &appState.Services); err != nil {
		log.Printf("Error parsing services: %v", err)
		return
	}

	// Check if any services need a priceID and update the file if necessary
	needsUpdate := false
	for i := range appState.Services {
		if appState.Services[i].PriceID == "" {
			ensureServiceHasPriceID(&appState.Services[i])
			needsUpdate = true
		}
	}

	// If any services were updated with new price IDs, save back to the file
	if needsUpdate {
		jsonData, _ := json.MarshalIndent(appState.Services, "", "  ")
		if err := os.WriteFile(servicesFilePath, jsonData, 0644); err != nil {
			log.Printf("Error updating services file with price IDs: %v", err)
		}
	}
}

// ensureServiceHasPriceID creates a Stripe price for a service if it doesn't have one
func ensureServiceHasPriceID(service *templates.Service) {
	if service.PriceID != "" {
		return // Already has a price ID
	}

	// Create a product in Stripe for this service
	productParams := &stripe.ProductParams{
		Name:        stripe.String(service.Name),
		Description: stripe.String(service.Description),
	}

	product, err := product.New(productParams)
	if err != nil {
		log.Printf("Error creating Stripe product for %s: %v", service.Name, err)
		return
	}

	// Create a price for this product
	priceParams := &stripe.PriceParams{
		Currency:   stripe.String("usd"),
		UnitAmount: stripe.Int64(int64(service.Price * 100)), // Convert to cents
		Product:    stripe.String(product.ID),
	}

	price, err := price.New(priceParams)
	if err != nil {
		log.Printf("Error creating Stripe price for %s: %v", service.Name, err)
		return
	}

	// Save the price ID to the service
	service.PriceID = price.ID
}

// Calculate cart summary
func calculateCartSummary() templates.CartSummary {
	var subtotal float64
	for _, service := range appState.CurrentCart {
		subtotal += service.Price
	}

	// TODO: Use Stripe Tax API instead of hardcoded rate
	// For now, we'll use a placeholder tax rate of 6.25%
	const taxRate = 0.0625 // Temporary until Stripe Tax API integration
	tax := subtotal * taxRate
	total := subtotal + tax

	return templates.CartSummary{
		Subtotal: subtotal,
		Tax:      tax,
		Total:    total,
	}
}

// Authentication middleware
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip authentication for login page and static assets
		if r.URL.Path == "/login" || r.URL.Path == "/static/" || r.URL.Path == "/static/css/styles.css" {
			next.ServeHTTP(w, r)
			return
		}

		// Check if authenticated
		cookie, err := r.Cookie("auth")
		if err != nil || cookie.Value != "authenticated" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// loginHandler handles the login page
func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Error parsing form", http.StatusBadRequest)
			return
		}

		if r.FormValue("pin") == PIN {
			// Set authentication cookie
			http.SetCookie(w, &http.Cookie{
				Name:     "auth",
				Value:    "authenticated",
				Path:     "/",
				MaxAge:   3600 * 8, // 8 hours
				HttpOnly: true,
			})

			// For HTMX requests, we need to set the HX-Redirect header
			// This is crucial for making HTMX follow the redirect
			w.Header().Set("HX-Redirect", "/")

			// Also set a standard HTTP redirect for non-HTMX clients
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}

		// Wrong PIN
		w.Header().Set("HX-Trigger", `{"showToast": "Invalid PIN. Please try again."}`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Check if already logged in
	cookie, err := r.Cookie("auth")
	if err == nil && cookie.Value == "authenticated" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Display login page using templ
	component := templates.LoginPage()
	err = component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// posHandler renders the main POS page
func posHandler(w http.ResponseWriter, r *http.Request) {
	component := templates.PosPage()
	err := component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// servicesHandler renders the services list
func servicesHandler(w http.ResponseWriter, r *http.Request) {
	component := templates.ServicesList(appState.Services)
	err := component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// cartHandler renders the cart contents
func cartHandler(w http.ResponseWriter, r *http.Request) {
	summary := calculateCartSummary()
	component := templates.CartView(appState.CurrentCart, summary)
	err := component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// checkoutFormHandler renders the checkout form
func checkoutFormHandler(w http.ResponseWriter, r *http.Request) {
	component := templates.CheckoutForm()
	err := component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// addToCartHandler adds a service to the cart
func addToCartHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	serviceID := r.FormValue("id")

	for _, service := range appState.Services {
		if service.ID == serviceID {
			appState.CurrentCart = append(appState.CurrentCart, service)
			w.Header().Set("HX-Trigger", "cartUpdated")
			return
		}
	}

	http.Error(w, "Service not found", http.StatusNotFound)
}

// addCustomServiceHandler adds a custom service to the cart
func addCustomServiceHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	description := r.FormValue("description")
	priceStr := r.FormValue("price")

	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		http.Error(w, "Invalid price", http.StatusBadRequest)
		return
	}

	// Create custom service
	customService := templates.Service{
		ID:          fmt.Sprintf("custom-%d", time.Now().UnixNano()),
		Name:        name,
		Description: description,
		Price:       price,
	}

	// Add to cart
	appState.CurrentCart = append(appState.CurrentCart, customService)
	w.Header().Set("HX-Trigger", "cartUpdated")
}

// removeFromCartHandler removes an item from the cart
func removeFromCartHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	indexStr := r.FormValue("index")
	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 || index >= len(appState.CurrentCart) {
		http.Error(w, "Invalid index", http.StatusBadRequest)
		return
	}

	// Remove item at index
	appState.CurrentCart = append(appState.CurrentCart[:index], appState.CurrentCart[index+1:]...)
	w.Header().Set("HX-Trigger", "cartUpdated")
}

// processPaymentHandler handles payment processing
func processPaymentHandler(w http.ResponseWriter, r *http.Request) {
	if len(appState.CurrentCart) == 0 {
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
	summary := calculateCartSummary()

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
		// Process terminal payment
		readerParams := &stripe.TerminalReaderProcessPaymentIntentParams{
			PaymentIntent: stripe.String(intent.ID),
		}

		// In a real app, you would specify the actual terminal reader ID
		terminalReader := "tmr_xxx" // Replace with actual terminal reader ID

		_, err = reader.ProcessPaymentIntent(terminalReader, readerParams)
		if err != nil {
			log.Printf("Error processing terminal payment: %v", err)
			w.Header().Set("HX-Trigger", `{"showToast": "Error processing terminal payment"}`)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		paymentSuccess = true

	case "manual":
		// Process manual card payment through Stripe
		cardNumber := r.FormValue("card_number")
		expiry := r.FormValue("expiry")
		cvv := r.FormValue("cvv")
		cardholder := r.FormValue("cardholder")

		if cardNumber == "" || expiry == "" || cvv == "" || cardholder == "" {
			w.Header().Set("HX-Trigger", `{"showToast": "Please fill in all card details"}`)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Parse expiry date (MM/YY format)
		expiryParts := strings.Split(expiry, "/")
		if len(expiryParts) != 2 {
			w.Header().Set("HX-Trigger", `{"showToast": "Invalid expiry date format (use MM/YY)"}`)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		expMonth, err := strconv.Atoi(expiryParts[0])
		if err != nil || expMonth < 1 || expMonth > 12 {
			w.Header().Set("HX-Trigger", `{"showToast": "Invalid expiry month"}`)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		expYear, err := strconv.Atoi("20" + expiryParts[1]) // Convert YY to 20YY
		if err != nil {
			w.Header().Set("HX-Trigger", `{"showToast": "Invalid expiry year"}`)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// In a production environment, you should use Stripe.js on the client side
		// and only pass a payment method ID to the server, NEVER raw card details.
		// This is for demonstration purposes only.

		// Create a payment method with the card details
		pmParams := &stripe.PaymentMethodParams{
			Type: stripe.String("card"),
			Card: &stripe.PaymentMethodCardParams{
				Number:   stripe.String(cardNumber),
				ExpMonth: stripe.Int64(int64(expMonth)),
				ExpYear:  stripe.Int64(int64(expYear)),
				CVC:      stripe.String(cvv),
			},
		}

		// Set cardholder name
		if cardholder != "" {
			pmParams.BillingDetails = &stripe.PaymentMethodBillingDetailsParams{
				Name: stripe.String(cardholder),
			}
		}

		pm, err := paymentmethod.New(pmParams)
		if err != nil {
			log.Printf("Error creating payment method: %v", err)
			w.Header().Set("HX-Trigger", `{"showToast": "Invalid card details: `+err.Error()+`"}`)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Attach the payment method to the intent and confirm it
		piParams := &stripe.PaymentIntentConfirmParams{
			PaymentMethod: stripe.String(pm.ID),
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
		Services:      appState.CurrentCart,
		Subtotal:      summary.Subtotal,
		Tax:           summary.Tax,
		Total:         summary.Total,
		PaymentType:   paymentMethod,
		CustomerEmail: email,
	}

	// Save transaction to CSV
	if err := saveTransactionToCSV(transaction); err != nil {
		log.Printf("Error saving transaction to CSV: %v", err)
	}

	// Clear cart
	appState.CurrentCart = []templates.Service{}

	// Send success response
	w.Header().Set("HX-Trigger", `{"showToast": "Payment processed successfully", "cartUpdated": true}`)
	w.Header().Set("HX-Redirect", "/")
}

// manualCardFormHandler renders the manual card form
func manualCardFormHandler(w http.ResponseWriter, r *http.Request) {
	if len(appState.CurrentCart) == 0 {
		w.Header().Set("HX-Trigger", `{"showToast": "Cart is empty. Please add items before proceeding to payment."}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	component := templates.ManualCardForm()
	err := component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// generateQRCodeHandler generates a QR code for payment
func generateQRCodeHandler(w http.ResponseWriter, r *http.Request) {
	// Check if cart is empty first
	if len(appState.CurrentCart) == 0 {
		// Send a toast message for empty cart
		w.Header().Set("HX-Trigger", `{"showToast": "Cart is empty. Please add items before generating a QR code."}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Render the QR code container template
	component := templates.QRCodeSection()
	err := component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	summary := calculateCartSummary()

	// Create payment link params
	params := &stripe.PaymentLinkParams{}

	// Add line items using the stored price IDs
	for _, service := range appState.CurrentCart {
		// Ensure the service has a price ID
		if service.PriceID == "" {
			// For custom services, create a price ID on the fly
			if strings.HasPrefix(service.ID, "custom-") {
				ensureServiceHasPriceID(&service)
			}

			if service.PriceID == "" {
				log.Printf("Error: Service %s has no price ID", service.Name)
				w.Header().Set("HX-Trigger", `{"showToast": "Error creating payment link: missing price ID"}`)
				return
			}
		}

		// Add the line item using stored price ID
		params.LineItems = append(params.LineItems, &stripe.PaymentLinkLineItemParams{
			Price:    stripe.String(service.PriceID),
			Quantity: stripe.Int64(1),
		})
	}

	// Add success URL for redirect after payment
	params.AfterCompletion = &stripe.PaymentLinkAfterCompletionParams{
		Type: stripe.String("redirect"),
		Redirect: &stripe.PaymentLinkAfterCompletionRedirectParams{
			URL: stripe.String("http://localhost:3000/payment-success"),
		},
	}

	paymentLink, err := paymentlink.New(params)
	if err != nil {
		log.Printf("Error creating payment link: %v", err)
		// Send error via toast message
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": "Error creating payment link: %s"}`, err.Error()))
		return
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

	// Create HTML with the actual QR code
	qrHTML := fmt.Sprintf(`
		<div class="qr-code">
			<img src="data:image/png;base64,%s" alt="Payment QR Code" width="256" height="256">
			<p class="payment-link-id">Payment Link ID: %s</p>
			<p class="total-amount">Total Amount: $%.2f</p>
			<p class="instructions">Scan with your camera app to pay</p>
		</div>
	`, qrBase64, paymentLink.ID, summary.Total)

	// Update the qr-code-container with the generated QR code
	w.Header().Set("HX-Trigger-After-Swap", `{"qrCodeGenerated": true}`)
	w.Header().Set("HX-Target", "#qr-code-container")
	w.Header().Set("HX-Swap", "innerHTML")
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(qrHTML))
}

// Save transaction to CSV in QuickBooks-friendly format
func saveTransactionToCSV(transaction templates.Transaction) error {
	// Create filename with current date (same date format as the transaction date)
	today := time.Now().Format("2006-01-02")
	
	// Use transactions directory from config or fallback to constant
	transactionsDir := config.Config.TransactionsDir
	if transactionsDir == "" {
		transactionsDir = TRANSACTIONS_DIR
	}
	
	filename := filepath.Join(transactionsDir, today+".csv")

	// Check if file exists to determine if we need headers
	fileExists := true
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		fileExists = false
	}

	// Open file for appending
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write headers if file is new
	if !fileExists {
		headers := []string{
			"Date", "Time", "Transaction ID", "Item/Service", "Description",
			"Quantity", "Unit Price", "Tax", "Total", "Payment Method", "Customer Email",
		}
		if err := writer.Write(headers); err != nil {
			return err
		}
	}

	// Write each service as a separate line
	for _, service := range transaction.Services {
		// TODO: Replace hardcoded tax calculation with Stripe Tax API
		const taxRate = 0.0625 // Temporary until Stripe Tax API integration
		tax := service.Price * taxRate
		total := service.Price + tax

		record := []string{
			transaction.Date,
			transaction.Time,
			transaction.ID,
			service.Name,
			service.Description,
			"1", // Quantity
			fmt.Sprintf("%.2f", service.Price),
			fmt.Sprintf("%.2f", tax),
			fmt.Sprintf("%.2f", total),
			transaction.PaymentType,
			transaction.CustomerEmail,
		}

		if err := writer.Write(record); err != nil {
			return err
		}
	}

	return nil
}

// logoutHandler handles user logout
func logoutHandler(w http.ResponseWriter, r *http.Request) {
	// Clear authentication cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "auth",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func main() {
	// Set up HTTP server
	mux := http.NewServeMux()

	// Static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	// Auth routes
	mux.HandleFunc("/login", loginHandler)
	mux.HandleFunc("/logout", logoutHandler)

	// API routes
	mux.HandleFunc("/services", servicesHandler)
	mux.HandleFunc("/cart", cartHandler)
	mux.HandleFunc("/checkout-form", checkoutFormHandler)
	mux.HandleFunc("/add-to-cart", addToCartHandler)
	mux.HandleFunc("/add-custom-service", addCustomServiceHandler)
	mux.HandleFunc("/remove-from-cart", removeFromCartHandler)
	mux.HandleFunc("/process-payment", processPaymentHandler)
	mux.HandleFunc("/generate-qr-code", generateQRCodeHandler)
	mux.HandleFunc("/manual-card-form", manualCardFormHandler)

	// Main route
	mux.HandleFunc("/", posHandler)

	// Apply auth middleware
	handler := authMiddleware(mux)

	// Start server using port from config or default
	port := config.Config.Port
	if port == "" {
		port = PORT
	}
	log.Printf("Starting server on port %s...", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}
