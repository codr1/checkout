package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"checkout/templates"
)

// Default configuration values
const (
	DefaultPort            = "3000"
	DefaultDataDir         = "./data"
	DefaultTransactionsDir = "./data/transactions"
)

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
		// Config file doesn't exist, ask user if they want to create it
		fmt.Print("Configuration file not found. Would you like to create one? (y/n): ")
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
		
		fmt.Println("Configuration file created successfully.")
		return nil
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
	
	// Stripe Secret Key
	fmt.Print("Enter Stripe Secret Key: ")
	stripeKey, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.StripeSecretKey = strings.TrimSpace(stripeKey)
	
	// Business Information
	fmt.Print("Enter Business Name: ")
	businessName, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.BusinessName = strings.TrimSpace(businessName)
	
	fmt.Print("Enter Business Street Address: ")
	street, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.BusinessStreet = strings.TrimSpace(street)
	
	fmt.Print("Enter Business City: ")
	city, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.BusinessCity = strings.TrimSpace(city)
	
	fmt.Print("Enter Business State: ")
	state, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.BusinessState = strings.TrimSpace(state)
	
	fmt.Print("Enter Business ZIP: ")
	zip, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.BusinessZIP = strings.TrimSpace(zip)
	
	// Tax Information
	fmt.Print("Enter Business Tax ID (EIN): ")
	taxID, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.BusinessTaxID = strings.TrimSpace(taxID)
	
	fmt.Print("Enter Sales Tax Registration Number: ")
	salesTax, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.SalesTaxNumber = strings.TrimSpace(salesTax)
	
	fmt.Print("Enter VAT Number (if applicable): ")
	vat, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.VATNumber = strings.TrimSpace(vat)
	
	// Website Information
	fmt.Print("Enter Website Name (for future HTTPS support): ")
	website, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.WebsiteName = strings.TrimSpace(website)
	
	// Default Customer Location
	fmt.Print("Enter Default Customer City: ")
	defaultCity, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.DefaultCity = strings.TrimSpace(defaultCity)
	
	fmt.Print("Enter Default Customer State: ")
	defaultState, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	Config.DefaultState = strings.TrimSpace(defaultState)
	
	return nil
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
		return envKey
	}
	
	return Config.StripeSecretKey
}

