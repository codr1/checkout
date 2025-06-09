package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"checkout/templates"
)

// Default configuration values
const (
	DefaultPort            = "3000"
	DefaultDataDir         = "./data"
	DefaultTransactionsDir = "./data/transactions"
)

// Payment configuration constants - consolidated from handlers/payment_config.go
const (
	// Polling intervals
	PaymentPollingInterval = "2s"
	
	// Timeout durations - unified for all payment types  
	// Backend timeout for progress calculations and polling logic
	PaymentTimeout = 120 * time.Second
	
	// Frontend HTMX auto-expire delay (same as timeout for consistency)
	// This acts as a safety net if browser closes or polling stops
	PaymentExpireDelay = "120s"
	
	// Failsafe timeout for client-side safety net (server timeout + 3 seconds)
	// If SSE doesn't send completion event, client triggers hard refresh
	PaymentFailsafeTimeout = 123 * time.Second
	
	// Polling endpoints
	QRPollEndpoint      = "/check-paymentlink-status"
	TerminalPollEndpoint = "/check-terminal-payment-status"
	
	// Expiration endpoints
	QRExpireEndpoint      = "/expire-payment-link"
	TerminalExpireEndpoint = "/expire-terminal-payment"
	
	// Cancel endpoints
	QRCancelEndpoint      = "/cancel-payment-link"
	TerminalCancelEndpoint = "/cancel-terminal-payment"
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

// Config holds the application configuration
var Config templates.AppConfig

// Load loads the application configuration from file or prompts user to create it
func Load() error {
	configPath := filepath.Join(DefaultDataDir, "config.json")

	// Create data directory if it doesn't exist
	if err := os.MkdirAll(DefaultDataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// Check if config file exists
	_, err := os.Stat(configPath)
	if os.IsNotExist(err) {
		fmt.Println("Configuration file not found at:", configPath)
		// Config file doesn't exist, ask user if they want to create it
		fmt.Print("Would you like to create a configuration file? (y/n): ")
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("error reading input: %w", err)
		}

		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			return fmt.Errorf("configuration file required to run the application")
		}

		// Prompt for configuration values
		if err := promptForConfig(); err != nil {
			return fmt.Errorf("error creating configuration: %w", err)
		}

		// Save configuration to file
		if err := saveConfig(configPath); err != nil {
			return fmt.Errorf("error saving configuration: %w", err)
		}

		fmt.Println("Configuration file created successfully at:", configPath)
		return nil
	} else if err != nil {
		return fmt.Errorf("error checking configuration file: %w", err)
	}

	// Read existing config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("error reading configuration file: %w", err)
	}

	// Parse config
	if err := json.Unmarshal(data, &Config); err != nil {
		return fmt.Errorf("error parsing configuration file: %w", err)
	}

	// Apply fallbacks for critical values
	if Config.Port == "" {
		Config.Port = DefaultPort
	}
	if Config.DataDir == "" {
		Config.DataDir = DefaultDataDir
	}
	if Config.TransactionsDir == "" {
		Config.TransactionsDir = DefaultTransactionsDir
	}

	// Check for environment variable override for Stripe Secret Key
	envStripeKey := os.Getenv("STRIPE_SECRET_KEY")
	if envStripeKey != "" && envStripeKey != Config.StripeSecretKey {
		fmt.Println("Using environment variable for Stripe Secret Key (overrides config file).")
		Config.StripeSecretKey = envStripeKey
	}

	return nil
}

// promptForConfig prompts the user for configuration values
func promptForConfig() error {
	reader := bufio.NewReader(os.Stdin)

	// Initialize config with defaults
	Config = templates.AppConfig{
		Port:            DefaultPort,
		DataDir:         DefaultDataDir,
		TransactionsDir: DefaultTransactionsDir,
	}

	// Password (prompt first for security)
	var password string
	valid := false
	for !valid {
		fmt.Print("Enter Admin Password (min 8 characters): ")
		passwordInput, err := reader.ReadString('\n')
		if err != nil {
			return err
		}

		password = sanitizeInput(strings.TrimSpace(passwordInput))
		if len(password) < 8 {
			fmt.Println("Password must be at least 8 characters long. Please try again.")
			continue
		}

		// Check for valid characters (letters, numbers, and printable symbols)
		validChars := true
		for _, char := range password {
			// ASCII printable range (32-126) includes letters, numbers, and symbols
			if char < 32 || char > 126 {
				validChars = false
				break
			}
		}

		if !validChars {
			fmt.Println("Password contains invalid characters. Please use only letters, numbers, and keyboard symbols.")
			continue
		}

		valid = true
	}
	Config.Password = password

	// Stripe Keys
	fmt.Print("Enter Stripe Secret Key: ")
	stripeSecretKey, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.StripeSecretKey = sanitizeInput(strings.TrimSpace(stripeSecretKey))

	fmt.Print("Enter Stripe Publishable Key: ")
	stripePublicKey, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.StripePublicKey = sanitizeInput(strings.TrimSpace(stripePublicKey))

	fmt.Print("Enter Stripe Webhook Secret: ")
	stripeWebhookSecret, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.StripeWebhookSecret = sanitizeInput(strings.TrimSpace(stripeWebhookSecret))

	// Business Information
	fmt.Print("Enter Business Name: ")
	businessName, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.BusinessName = sanitizeInput(strings.TrimSpace(businessName))

	fmt.Print("Enter Business Street Address: ")
	street, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.BusinessStreet = sanitizeInput(strings.TrimSpace(street))

	fmt.Print("Enter Business City: ")
	city, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.BusinessCity = sanitizeInput(strings.TrimSpace(city))

	fmt.Print("Enter Business State: ")
	state, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.BusinessState = sanitizeInput(strings.TrimSpace(state))

	fmt.Print("Enter Business ZIP: ")
	zip, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.BusinessZIP = sanitizeInput(strings.TrimSpace(zip))

	// Tax Information
	fmt.Print("Enter Business Tax ID (EIN): ")
	taxID, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.BusinessTaxID = sanitizeInput(strings.TrimSpace(taxID))

	fmt.Print("Enter Sales Tax Registration Number: ")
	salesTax, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.SalesTaxNumber = sanitizeInput(strings.TrimSpace(salesTax))

	fmt.Print("Enter VAT Number (if applicable): ")
	vat, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.VATNumber = sanitizeInput(strings.TrimSpace(vat))

	// Website Information
	fmt.Print("Enter Website Name (for future HTTPS support): ")
	website, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.WebsiteName = sanitizeInput(strings.TrimSpace(website))

	// Default Customer Location
	fmt.Print("Enter Default Customer City: ")
	defaultCity, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.DefaultCity = sanitizeInput(strings.TrimSpace(defaultCity))

	fmt.Print("Enter Default Customer State: ")
	defaultState, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.DefaultState = sanitizeInput(strings.TrimSpace(defaultState))

	// Tax Configuration
	fmt.Print("Enter Default Tax Rate (as decimal, e.g., 0.0625 for 6.25%): ")
	taxRateStr, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	taxRateStr = sanitizeInput(strings.TrimSpace(taxRateStr))

	// Parse the tax rate
	if taxRateStr == "" {
		Config.DefaultTaxRate = 0.0625 // Default to 6.25%
	} else {
		// Simple parsing - you might want to add more validation
		fmt.Sscanf(taxRateStr, "%f", &Config.DefaultTaxRate)
		if Config.DefaultTaxRate < 0 || Config.DefaultTaxRate > 1 {
			fmt.Println("Invalid tax rate, using default 6.25%")
			Config.DefaultTaxRate = 0.0625
		}
	}

	// Initialize empty tax categories - these can be managed through the admin interface later
	Config.TaxCategories = []templates.TaxCategory{}

	// Tipping Configuration - Set defaults
	fmt.Println("\n=== Tipping Configuration ===")
	fmt.Print("Enable tipping globally? (y/n) [default: n]: ")
	tippingInput, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	tippingInput = strings.TrimSpace(strings.ToLower(tippingInput))
	Config.TippingEnabled = (tippingInput == "y" || tippingInput == "yes")

	// Initialize tipping defaults
	Config.TippingLocationOverrides = make(map[string]bool)
	Config.TippingMinAmount = 0.0 // No minimum by default
	Config.TippingMaxAmount = 0.0 // No maximum by default (0 = unlimited)
	Config.TippingPresetPercentages = []int{15, 18, 20, 25} // Common preset percentages
	Config.TippingAllowCustomAmount = true // Allow custom amounts by default
	Config.TippingServiceCategoriesOnly = []string{} // Empty = all categories

	if Config.TippingEnabled {
		fmt.Print("Minimum transaction amount for tipping (in dollars, 0 for no minimum): ")
		minAmountStr, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		minAmountStr = strings.TrimSpace(minAmountStr)
		if minAmountStr != "" {
			fmt.Sscanf(minAmountStr, "%f", &Config.TippingMinAmount)
			if Config.TippingMinAmount < 0 {
				Config.TippingMinAmount = 0.0
			}
		}

		fmt.Print("Maximum transaction amount for tipping (in dollars, 0 for no maximum): ")
		maxAmountStr, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		maxAmountStr = strings.TrimSpace(maxAmountStr)
		if maxAmountStr != "" {
			fmt.Sscanf(maxAmountStr, "%f", &Config.TippingMaxAmount)
			if Config.TippingMaxAmount < 0 {
				Config.TippingMaxAmount = 0.0
			}
		}

		fmt.Print("Allow custom tip amounts? (y/n) [default: y]: ")
		customTipInput, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		customTipInput = strings.TrimSpace(strings.ToLower(customTipInput))
		Config.TippingAllowCustomAmount = !(customTipInput == "n" || customTipInput == "no")
	}

	fmt.Printf("Tipping configuration: Enabled=%v, MinAmount=%.2f, MaxAmount=%.2f\n", 
		Config.TippingEnabled, Config.TippingMinAmount, Config.TippingMaxAmount)

	return nil
}

// sanitizeInput cleans input to prevent escape sequences and other problematic characters
func sanitizeInput(input string) string {
	// Replace any control characters with empty string
	result := ""
	for _, char := range input {
		// Keep only printable ASCII characters (32-126)
		if char >= 32 && char <= 126 {
			result += string(char)
		}
	}
	return result
}

// saveConfig saves the configuration to file
func saveConfig(path string) error {
	jsonData, err := json.MarshalIndent(Config, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshalling configuration: %w", err)
	}

	if err := os.WriteFile(path, jsonData, 0600); err != nil {
		return fmt.Errorf("error writing configuration file: %w", err)
	}

	return nil
}

// GetStripeKey returns the Stripe API key, checking environment variable first
func GetStripeKey() string {
	// Environment variable takes precedence
	envKey := os.Getenv("STRIPE_SECRET_KEY")
	if envKey != "" {
		fmt.Println("[INFO] Using Stripe key from environment variable")
		return envKey
	}

	if Config.StripeSecretKey != "" {
		fmt.Println("[INFO] Using Stripe key from config file")
		return Config.StripeSecretKey
	}

	fmt.Println("[ERROR] Stripe key not found in environment or config")
	return ""
}

// GetStripePublicKey returns the Stripe publishable key
func GetStripePublicKey() string {
	// Environment variable takes precedence
	envKey := os.Getenv("STRIPE_PUBLIC_KEY")
	if envKey != "" {
		fmt.Println("[INFO] Using Stripe publishable key from environment variable")
		return envKey
	}

	if Config.StripePublicKey != "" {
		fmt.Println("[INFO] Using Stripe publishable key from config file")
		return Config.StripePublicKey
	}

	fmt.Println("[ERROR] Stripe publishable key not found in environment or config")
	return ""
}

// GetStripeWebhookSecret returns the Stripe webhook secret
func GetStripeWebhookSecret() string {
	// Environment variable takes precedence
	envSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	if envSecret != "" {
		fmt.Println("[INFO] Using Stripe webhook secret from environment variable")
		return envSecret
	}

	if Config.StripeWebhookSecret != "" {
		fmt.Println("[INFO] Using Stripe webhook secret from config file")
		return Config.StripeWebhookSecret
	}

	fmt.Println("[WARN] Stripe webhook secret not found in environment or config")
	return ""
}

// SetTippingLocationOverride sets a location-specific tipping override
func SetTippingLocationOverride(locationID string, enabled bool) error {
	if Config.TippingLocationOverrides == nil {
		Config.TippingLocationOverrides = make(map[string]bool)
	}
	
	Config.TippingLocationOverrides[locationID] = enabled
	
	// Save the updated configuration
	configPath := filepath.Join(DefaultDataDir, "config.json")
	return saveConfig(configPath)
}

// RemoveTippingLocationOverride removes a location-specific tipping override
func RemoveTippingLocationOverride(locationID string) error {
	if Config.TippingLocationOverrides == nil {
		return nil // Nothing to remove
	}
	
	delete(Config.TippingLocationOverrides, locationID)
	
	// Save the updated configuration
	configPath := filepath.Join(DefaultDataDir, "config.json")
	return saveConfig(configPath)
}

// GetTippingEnabledForLocation returns whether tipping is enabled for a specific location
func GetTippingEnabledForLocation(locationID string) bool {
	// Check for location-specific override first
	if override, exists := Config.TippingLocationOverrides[locationID]; exists {
		return override
	}
	
	// Fall back to global setting
	return Config.TippingEnabled
}
