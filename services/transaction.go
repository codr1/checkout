package services

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"checkout/config"
	"checkout/templates"
	"checkout/utils"
)

// Save transaction to CSV in QuickBooks-friendly format
func SaveTransactionToCSV(transaction templates.Transaction) error {
	// Create filename with current date (same date format as the transaction date)
	today := time.Now().Format("2006-01-02")

	// Use transactions directory from config or fallback to constant
	transactionsDir := config.Config.TransactionsDir
	if transactionsDir == "" {
		transactionsDir = "./data/transactions"
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
		return fmt.Errorf("failed to open log file: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			utils.Error("services", "Error closing transaction log file", "error", err)
		}
	}()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write headers if file is new
	if !fileExists {
		headers := []string{
			"Date", "Time", "Transaction ID", "Item/Service", "Description",
			"Quantity", "Unit Price", "Tax", "Total", "Payment Method", "Customer Email",
			"Payment Link ID", "Payment Link Status", "Confirmation Code", "Failure Reason",
		}
		if err := writer.Write(headers); err != nil {
			return err
		}
	}

	// For payment link events without services (like cancellations or expirations)
	if len(transaction.Services) == 0 && transaction.PaymentLinkID != "" {
		record := []string{
			transaction.Date,
			transaction.Time,
			transaction.ID,
			"", // Service name
			"Payment Link " + transaction.PaymentLinkStatus, // Description
			"",                                     // Quantity
			"",                                     // Unit Price
			"",                                     // Tax
			fmt.Sprintf("%.2f", transaction.Total), // Total (may be 0 for cancellations)
			transaction.PaymentType,
			transaction.CustomerEmail,
			transaction.PaymentLinkID,
			transaction.PaymentLinkStatus,
			transaction.ConfirmationCode,
			transaction.FailureReason,
		}

		if err := writer.Write(record); err != nil {
			return err
		}

		return nil
	}

	// Write each service as a separate line
	for _, service := range transaction.Services {
		// Calculate tax and total for this service
		var tax, total float64

		// If we have calculated tax
		if transaction.Tax > 0 && transaction.Subtotal > 0 {
			// Distribute the tax proportionally based on this service's price
			taxRate := transaction.Tax / transaction.Subtotal
			tax = service.Price * taxRate
			total = service.Price + tax
		} else {
			// Fallback
			const taxRate = 0.0625 // Temporary
			tax = service.Price * taxRate
			total = service.Price + tax
		}

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
			transaction.PaymentLinkID,
			transaction.PaymentLinkStatus,
			transaction.ConfirmationCode,
			transaction.FailureReason,
		}

		if err := writer.Write(record); err != nil {
			return err
		}
	}

	return nil
}

// LoadServices loads services from the JSON file
func LoadServices() error {
	utils.Info("services", "Loading services")

	// Use data directory from config or fallback to constant
	dataDir := config.Config.DataDir
	if dataDir == "" {
		dataDir = "./data"
	}
	servicesFilePath := filepath.Join(dataDir, "services.json")

	// Check if services file exists
	if _, err := os.Stat(servicesFilePath); os.IsNotExist(err) {
		utils.Error("services", "No services defined", "error", "services.json file not found")
		AppState.Services = []templates.Service{} // Initialize empty services
		return fmt.Errorf("no services defined: services.json file not found")
	}

	// Read existing services
	data, err := os.ReadFile(servicesFilePath)
	if err != nil {
		return fmt.Errorf("error reading services: %w", err)
	}

	var services []templates.Service
	if err := json.Unmarshal(data, &services); err != nil {
		return fmt.Errorf("error parsing services: %w", err)
	}

	// Ensure each service has a Stripe Product ID and a default Price ID.
	// Update the services.json file if any changes were made.
	var actualUpdatesMade bool // Correctly named flag
	for i := range services {
		// Assuming EnsureServiceHasPriceID is the one from services/stripe.go
		// which now returns (bool, error)
		updated, err := EnsureServiceHasPriceID(&services[i])
		if err != nil {
			utils.Error("services", "Error ensuring Stripe IDs", "service", services[i].Name, "id", services[i].ID, "error", err)
		}
		if updated {
			actualUpdatesMade = true
		}
	}

	if actualUpdatesMade {
		utils.Debug("services", "Services updated with Stripe IDs, saving changes")
		for _, s := range services { // Log current state of all services before saving
			utils.Debug("services", "Before SaveServices", "service", s.Name, "id", s.ID, "stripe_product_id", s.StripeProductID, "price_id", s.PriceID)
		}
		if err := SaveServices(services); err != nil {
			return fmt.Errorf("error saving updated services to services.json: %w", err)
		}
		utils.Debug("services", "Successfully saved services.json with updated Stripe IDs")
	}

	// Log the state of services before assigning to AppState
	for _, s := range services {
		utils.Debug("services", "Before AppState assignment", "service", s.Name, "id", s.ID, "stripe_product_id", s.StripeProductID, "price_id", s.PriceID)
	}
	AppState.Services = services
	utils.Debug("services", "Finished LoadServices, AppState.Services populated")
	// Log the state of AppState.Services after assignment
	for _, s_app := range AppState.Services {
		utils.Debug("services", "After AppState assignment", "service", s_app.Name, "id", s_app.ID, "stripe_product_id", s_app.StripeProductID, "price_id", s_app.PriceID)
	}
	return nil
}

// SaveServices saves the services to the JSON file
func SaveServices(services []templates.Service) error {
	// Use data directory from config or fallback to constant
	dataDir := config.Config.DataDir
	if dataDir == "" {
		dataDir = "./data"
	}
	servicesFilePath := filepath.Join(dataDir, "services.json")

	// Ensure the directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("error creating data directory: %w", err)
	}

	// Marshal the services to JSON
	jsonData, err := json.MarshalIndent(services, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling services: %w", err)
	}

	// Write the JSON to file
	if err := os.WriteFile(servicesFilePath, jsonData, 0644); err != nil {
		return fmt.Errorf("error writing services file: %w", err)
	}

	return nil
}
