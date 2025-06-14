package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

// Config holds the application configuration
var Config templates.AppConfig

// SettingsMap provides a centralized map of all settings for easy access and searching
var SettingsMap = map[string]map[string]interface{}{
	"stripe": {
		"Stripe Secret Key":     Config.StripeSecretKey,
		"Stripe Public Key":     Config.StripePublicKey,
		"Stripe Webhook Secret": Config.StripeWebhookSecret,
		"Terminal Location":     Config.StripeTerminalLocationID,
	},
	"business": {
		"Business Name":  Config.BusinessName,
		"Street Address": Config.BusinessStreet,
		"City":           Config.BusinessCity,
		"State":          Config.BusinessState,
		"ZIP Code":       Config.BusinessZIP,
	},
	"tax": {
		"Business Tax ID":  Config.BusinessTaxID,
		"Sales Tax Number": Config.SalesTaxNumber,
		"VAT Number":       Config.VATNumber,
	},
	"system": {
		"Port":             Config.Port,
		"Data Directory":   Config.DataDir,
		"Transactions Dir": Config.TransactionsDir,
		"Website Name":     Config.WebsiteName,
	},
	"tipping": {
		"Tipping Enabled":      Config.TippingEnabled,
		"Min Amount":           Config.TippingMinAmount,
		"Max Amount":           Config.TippingMaxAmount,
		"Allow Custom Amounts": Config.TippingAllowCustomAmount,
	},
	"sms": {
		"AWS Access Key": Config.AWSAccessKeyID,
		"AWS Region":     Config.AWSRegion,
	},
}

// UpdateSettingsMap updates the settings map with current config values
func UpdateSettingsMap() {
	SettingsMap["stripe"]["Stripe Secret Key"] = Config.StripeSecretKey
	SettingsMap["stripe"]["Stripe Public Key"] = Config.StripePublicKey
	SettingsMap["stripe"]["Stripe Webhook Secret"] = Config.StripeWebhookSecret
	SettingsMap["stripe"]["Terminal Location"] = Config.StripeTerminalLocationID

	SettingsMap["business"]["Business Name"] = Config.BusinessName
	SettingsMap["business"]["Street Address"] = Config.BusinessStreet
	SettingsMap["business"]["City"] = Config.BusinessCity
	SettingsMap["business"]["State"] = Config.BusinessState
	SettingsMap["business"]["ZIP Code"] = Config.BusinessZIP

	SettingsMap["tax"]["Business Tax ID"] = Config.BusinessTaxID
	SettingsMap["tax"]["Sales Tax Number"] = Config.SalesTaxNumber
	SettingsMap["tax"]["VAT Number"] = Config.VATNumber

	SettingsMap["system"]["Port"] = Config.Port
	SettingsMap["system"]["Data Directory"] = Config.DataDir
	SettingsMap["system"]["Transactions Dir"] = Config.TransactionsDir
	SettingsMap["system"]["Website Name"] = Config.WebsiteName

	SettingsMap["tipping"]["Tipping Enabled"] = Config.TippingEnabled
	SettingsMap["tipping"]["Min Amount"] = Config.TippingMinAmount
	SettingsMap["tipping"]["Max Amount"] = Config.TippingMaxAmount
	SettingsMap["tipping"]["Allow Custom Amounts"] = Config.TippingAllowCustomAmount

	SettingsMap["sms"]["AWS Access Key"] = Config.AWSAccessKeyID
	SettingsMap["sms"]["AWS Region"] = Config.AWSRegion
}

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

	// Initialize the settings map with current values
	UpdateSettingsMap()

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
	Config.TippingServiceCategoriesOnly = []string{}        // Empty = all categories

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

	// Update the settings map with current values
	UpdateSettingsMap()

	return nil
}

// GetSetting retrieves a setting value from the settings map
func GetSetting(section, key string) interface{} {
	if sectionSettings, ok := SettingsMap[section]; ok {
		if value, ok := sectionSettings[key]; ok {
			return value
		}
	}
	return nil
}

// SetSetting updates a setting value in the settings map and saves to config
func SetSetting(section, key string, value interface{}) error {
	if sectionSettings, ok := SettingsMap[section]; ok {
		if _, ok := sectionSettings[key]; ok {
			// Update the settings map
			sectionSettings[key] = value

			// Update the config struct using reflection
			configValue := reflect.ValueOf(&Config).Elem()
			fieldName := strings.ReplaceAll(key, " ", "")
			field := configValue.FieldByName(fieldName)
			if field.IsValid() && field.CanSet() {
				// Convert the value to the correct type
				fieldType := field.Type()
				valueType := reflect.TypeOf(value)

				if fieldType != valueType {
					// Handle type conversion
					switch fieldType.Kind() {
					case reflect.Bool:
						field.SetBool(value.(bool))
					case reflect.Float64:
						field.SetFloat(value.(float64))
					case reflect.String:
						field.SetString(value.(string))
					default:
						return fmt.Errorf("unsupported field type: %v", fieldType)
					}
				} else {
					field.Set(reflect.ValueOf(value))
				}
			}

			// Save the updated config
			configPath := fmt.Sprintf("%s/config.json", Config.DataDir)
			return saveConfig(configPath)
		}
		return fmt.Errorf("setting '%s' not found in section '%s'", key, section)
	}
	return fmt.Errorf("section '%s' not found", section)
}

// GetStripeKey returns the Stripe secret key from config or environment
func GetStripeKey() string {
	// First try environment variable
	if key := os.Getenv("STRIPE_SECRET_KEY"); key != "" {
		return key
	}

	// Then try config
	if key := GetSetting("stripe", "Stripe Secret Key"); key != nil {
		return key.(string)
	}
	return ""
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
	if secret := GetSetting("stripe", "Stripe Webhook Secret"); secret != nil {
		return secret.(string)
	}
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

// GetTippingConfig returns tipping configuration
func GetTippingConfig(locationID string) (bool, float64, float64, bool) {
	tippingEnabled := GetSetting("tipping", "Tipping Enabled").(bool)
	minAmount := GetSetting("tipping", "Min Amount").(float64)
	maxAmount := GetSetting("tipping", "Max Amount").(float64)
	allowCustom := GetSetting("tipping", "Allow Custom Amounts").(bool)

	// Check location override
	if locationOverride, exists := Config.TippingLocationOverrides[locationID]; exists {
		tippingEnabled = locationOverride
	}

	return tippingEnabled, minAmount, maxAmount, allowCustom
}
