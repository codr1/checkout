package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"checkout/templates"
	"checkout/utils"
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

	// Timeout durations for all payment types
	// Backend timeout for progress calculations and polling logic
	PaymentTimeout = 120 * time.Second

	// Frontend HTMX auto-expire delay (same as timeout for consistency)
	// This acts as a safety net if browser closes or polling stops
	PaymentExpireDelay = "120s"

	// Failsafe timeout for client-side safety net (server timeout + 3 seconds)
	// If SSE doesn't send completion event, client triggers hard refresh
	PaymentFailsafeTimeout = (120 + 3) * time.Second

	// Payment status endpoints
	PollEndpoint          = "/get-payment-status"
	CancelRefreshEndpoint = "/cancel-or-refresh-payment"
)

// PaymentProgressMessages provides consistent status messages
var PaymentProgressMessages = map[string]map[string]string{
	"qr": {
		"default":    "Waiting for QR code scan...",
		"processing": "Processing QR payment...",
		"scanning":   "Please scan the QR code with your camera app",
	},
	"terminal": {
		"default":    "Processing on terminal...",
		"processing": "Please complete the transaction on the payment terminal",
		"waiting":    "Waiting for terminal interaction...",
		"receipt":    "Please take your receipt from the terminal",
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

// GetPaymentTimeoutSeconds returns the payment timeout as an integer (for JavaScript/templates)
func GetPaymentTimeoutSeconds() int {
	return int(PaymentTimeout.Seconds())
}

// GetFailsafeTimeoutSeconds returns the failsafe timeout as an integer (for JavaScript/templates)
func GetFailsafeTimeoutSeconds() int {
	return int(PaymentFailsafeTimeout.Seconds())
}

// GetCommunicationStrategy determines whether to use polling or webhooks
func GetCommunicationStrategy() string {
	websiteName := strings.TrimSpace(Config.WebsiteName)
	if websiteName != "" && websiteName != "localhost" {
		return "webhooks"
	}
	return "polling"
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
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		utils.Info("config", "Configuration file not found", "config_path", configPath)
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

		utils.Info("config", "Configuration file created successfully", "config_path", configPath)
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

	// Override with environment variable if available
	envStripeKey := os.Getenv("STRIPE_SECRET_KEY")
	if envStripeKey != "" && envStripeKey != Config.StripeSecretKey {
		utils.Info("config", "Using environment variable for Stripe Secret Key (overrides config file)")
		Config.StripeSecretKey = envStripeKey
	}

	// Parse tax rate
	if taxRateStr := os.Getenv("DEFAULT_TAX_RATE"); taxRateStr != "" {
		if _, err := fmt.Sscanf(taxRateStr, "%f", &Config.DefaultTaxRate); err != nil {
			utils.Warn("config", "Invalid DEFAULT_TAX_RATE value, using default", "value", taxRateStr, "error", err)
		}
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
		if _, err := fmt.Sscanf(taxRateStr, "%f", &Config.DefaultTaxRate); err != nil {
			utils.Warn("config", "Invalid tax rate format, using default 6.25%", "value", taxRateStr, "error", err)
			Config.DefaultTaxRate = 0.0625
		} else if Config.DefaultTaxRate < 0 || Config.DefaultTaxRate > 1 {
			utils.Warn("config", "Invalid tax rate, using default 6.25%")
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
	Config.TippingMinAmount = 0.0                           // No minimum by default
	Config.TippingMaxAmount = 0.0                           // No maximum by default (0 = unlimited)
	Config.TippingPresetPercentages = []int{15, 18, 20, 25} // Common preset percentages
	Config.TippingAllowCustomAmount = true                  // Allow custom amounts by default
	Config.TippingProductCategoriesOnly = []string{}        // Empty = all categories

	if Config.TippingEnabled {
		fmt.Print("Minimum transaction amount for tipping (in dollars, 0 for no minimum): ")
		minAmountStr, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		minAmountStr = strings.TrimSpace(minAmountStr)
		if minAmountStr != "" {
			if _, err := fmt.Sscanf(minAmountStr, "%f", &Config.TippingMinAmount); err != nil {
				utils.Warn("config", "Invalid TIPPING_MIN_AMOUNT value, using default", "value", minAmountStr, "error", err)
			}
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
			if _, err := fmt.Sscanf(maxAmountStr, "%f", &Config.TippingMaxAmount); err != nil {
				utils.Warn("config", "Invalid TIPPING_MAX_AMOUNT value, using default", "value", maxAmountStr, "error", err)
			}
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
		Config.TippingAllowCustomAmount = (customTipInput != "n" && customTipInput != "no")
	}

	utils.Info("config", "Tipping configuration complete", "enabled", Config.TippingEnabled, "min_amount", Config.TippingMinAmount, "max_amount", Config.TippingMaxAmount)

	// AWS SNS Configuration for SMS receipts (optional)
	fmt.Println("\n=== SMS Receipt Configuration (Optional) ===")
	fmt.Print("Configure AWS SNS for SMS receipts? (y/n) [default: n]: ")
	smsInput, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	smsInput = strings.TrimSpace(strings.ToLower(smsInput))

	if smsInput == "y" || smsInput == "yes" {
		fmt.Print("Enter AWS Access Key ID: ")
		awsKeyID, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		Config.AWSAccessKeyID = sanitizeInput(strings.TrimSpace(awsKeyID))

		fmt.Print("Enter AWS Secret Access Key: ")
		awsSecret, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		Config.AWSSecretAccessKey = sanitizeInput(strings.TrimSpace(awsSecret))

		fmt.Print("Enter AWS Region (e.g., us-east-1): ")
		awsRegion, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		Config.AWSRegion = sanitizeInput(strings.TrimSpace(awsRegion))
		if Config.AWSRegion == "" {
			Config.AWSRegion = "us-east-1" // Default region
		}
	} else {
		// Initialize with empty values so they appear in config.json
		Config.AWSAccessKeyID = ""
		Config.AWSSecretAccessKey = ""
		Config.AWSRegion = ""
	}

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

// saveConfig saves the configuration to a file
func saveConfig(path string) error {
	// Create data directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// Marshal config to JSON
	data, err := json.MarshalIndent(Config, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling configuration: %w", err)
	}

	// Write to file
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("error writing configuration file: %w", err)
	}

	return nil
}

// GetStripeKey returns the Stripe secret key from config or environment
func GetStripeKey() string {
	// First try environment variable
	if key := os.Getenv("STRIPE_SECRET_KEY"); key != "" {
		return key
	}

	// Then try config
	return Config.StripeSecretKey
}

// GetStripePublicKey returns the Stripe publishable key
func GetStripePublicKey() string {
	// Environment variable takes precedence
	envKey := os.Getenv("STRIPE_PUBLIC_KEY")
	if envKey != "" {
		utils.Info("config", "Using Stripe publishable key from environment variable")
		return envKey
	}

	if Config.StripePublicKey != "" {
		utils.Info("config", "Using Stripe publishable key from config file")
		return Config.StripePublicKey
	}

	utils.Error("config", "Stripe publishable key not found", "checked", "environment and config")
	return ""
}

// GetStripeWebhookSecret returns the webhook secret from config or environment
func GetStripeWebhookSecret() string {
	// First try environment variable
	if secret := os.Getenv("STRIPE_WEBHOOK_SECRET"); secret != "" {
		return secret
	}

	// Then try config
	return Config.StripeWebhookSecret
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

// GetTippingConfig returns tipping configuration
func GetTippingConfig(locationID string) (bool, float64, float64, bool) {
	tippingEnabled := Config.TippingEnabled
	minAmount := Config.TippingMinAmount
	maxAmount := Config.TippingMaxAmount
	allowCustom := Config.TippingAllowCustomAmount

	// Check location override
	if locationOverride, exists := Config.TippingLocationOverrides[locationID]; exists {
		tippingEnabled = locationOverride
	}

	return tippingEnabled, minAmount, maxAmount, allowCustom
}

// IsSMSEnabled returns true if AWS SNS is configured for SMS receipts
func IsSMSEnabled() bool {
	return Config.AWSAccessKeyID != "" && Config.AWSSecretAccessKey != "" && Config.AWSRegion != ""
}

// GetConfigFields returns config fields with their metadata for template generation
func GetConfigFields() map[string][]map[string]interface{} {
	return map[string][]map[string]interface{}{
		"stripe": {
			{"name": "StripeSecretKey", "label": "Stripe Secret Key", "type": "password", "id": "stripe-secret-key", "value": Config.StripeSecretKey},
			{"name": "StripePublicKey", "label": "Stripe Public Key", "type": "text", "id": "stripe-public-key", "value": Config.StripePublicKey},
			{"name": "StripeWebhookSecret", "label": "Stripe Webhook Secret", "type": "password", "id": "stripe-webhook-secret", "value": Config.StripeWebhookSecret},
			{"name": "StripeTerminalLocationID", "label": "Terminal Location", "type": "text", "id": "stripe-terminal-location", "value": Config.StripeTerminalLocationID},
		},
		"business": {
			{"name": "BusinessName", "label": "Business Name", "type": "text", "id": "business-name", "value": Config.BusinessName},
			{"name": "BusinessStreet", "label": "Street Address", "type": "text", "id": "business-street", "value": Config.BusinessStreet},
			{"name": "BusinessCity", "label": "City", "type": "text", "id": "business-city", "value": Config.BusinessCity},
			{"name": "BusinessState", "label": "State", "type": "text", "id": "business-state", "value": Config.BusinessState},
			{"name": "BusinessZIP", "label": "ZIP Code", "type": "text", "id": "business-zip", "value": Config.BusinessZIP},
		},
		"tax": {
			{"name": "BusinessTaxID", "label": "Business Tax ID", "type": "text", "id": "business-tax-id", "value": Config.BusinessTaxID},
			{"name": "SalesTaxNumber", "label": "Sales Tax Number", "type": "text", "id": "sales-tax-number", "value": Config.SalesTaxNumber},
			{"name": "VATNumber", "label": "VAT Number", "type": "text", "id": "vat-number", "value": Config.VATNumber},
			{"name": "DefaultTaxRate", "label": "Default Tax Rate", "type": "number", "id": "default-tax-rate", "value": Config.DefaultTaxRate * 100, "step": "0.0001", "min": "0", "max": "100"},
		},
		"system": {
			{"name": "ServerAddress", "label": "Server Address", "type": "text", "id": "server-address", "value": Config.ServerAddress},
			{"name": "Port", "label": "Port", "type": "text", "id": "port", "value": Config.Port},
			{"name": "DataDir", "label": "Data Directory", "type": "text", "id": "data-dir", "value": Config.DataDir},
			{"name": "TransactionsDir", "label": "Transactions Dir", "type": "text", "id": "transactions-dir", "value": Config.TransactionsDir},
			{"name": "WebsiteName", "label": "Website Name", "type": "text", "id": "website-name", "value": Config.WebsiteName},
		},
		"tipping": {
			{"name": "TippingEnabled", "label": "Tipping Enabled", "type": "checkbox", "id": "tipping-enabled", "value": Config.TippingEnabled},
			{"name": "TippingMinAmount", "label": "Min Amount", "type": "number", "id": "tipping-min-amount", "value": Config.TippingMinAmount, "step": "0.01", "min": "0"},
			{"name": "TippingMaxAmount", "label": "Max Amount", "type": "number", "id": "tipping-max-amount", "value": Config.TippingMaxAmount, "step": "0.01", "min": "0"},
			{"name": "TippingAllowCustomAmount", "label": "Allow Custom Amounts", "type": "checkbox", "id": "tipping-allow-custom", "value": Config.TippingAllowCustomAmount},
		},
		"sms": {
			{"name": "AWSAccessKeyID", "label": "AWS Access Key", "type": "text", "id": "aws-access-key", "value": Config.AWSAccessKeyID},
			{"name": "AWSSecretAccessKey", "label": "AWS Secret Access Key", "type": "password", "id": "aws-secret-key", "value": Config.AWSSecretAccessKey},
			{"name": "AWSRegion", "label": "AWS Region", "type": "text", "id": "aws-region", "value": Config.AWSRegion},
		},
	}
}

// UpdateConfigField updates a config field by name using reflection
func UpdateConfigField(fieldName string, value interface{}) error {
	configValue := reflect.ValueOf(&Config).Elem()
	field := configValue.FieldByName(fieldName)

	if !field.IsValid() {
		return fmt.Errorf("field %s not found", fieldName)
	}
	if !field.CanSet() {
		return fmt.Errorf("field %s cannot be set", fieldName)
	}

	// Convert value to appropriate type
	switch field.Kind() {
	case reflect.String:
		if str, ok := value.(string); ok {
			field.SetString(str)
		} else {
			field.SetString(fmt.Sprintf("%v", value))
		}
	case reflect.Float64:
		if str, ok := value.(string); ok {
			if floatVal, err := strconv.ParseFloat(str, 64); err == nil {
				// Handle percentage conversion for DefaultTaxRate
				if fieldName == "DefaultTaxRate" {
					floatVal = floatVal / 100.0 // Convert percentage to decimal
				}
				field.SetFloat(floatVal)
			} else {
				return fmt.Errorf("cannot convert %s to float64", str)
			}
		}
	case reflect.Bool:
		if str, ok := value.(string); ok {
			boolVal := str == "true" || str == "on" || str == "1"
			field.SetBool(boolVal)
		}
	default:
		return fmt.Errorf("unsupported field type: %s", field.Kind())
	}

	// Save config
	configPath := filepath.Join(Config.DataDir, "config.json")
	return saveConfig(configPath)
}
