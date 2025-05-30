package services

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"checkout/config"
	"checkout/templates"
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
	// Use data directory from config or fallback to constant
	dataDir := config.Config.DataDir
	if dataDir == "" {
		dataDir = "./data"
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
			EnsureServiceHasPriceID(&defaultServices[i])
		}

		// Save the default services to file
		if err := SaveServices(defaultServices); err != nil {
			return err
		}

		AppState.Services = defaultServices
		return nil
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
			log.Printf(
				"[LoadServices] Error ensuring Stripe IDs for service %s (ID: %s): %v",
				services[i].Name,
				services[i].ID,
				err,
			)
		}
		if updated {
			actualUpdatesMade = true
		}
	}

	if actualUpdatesMade {
		log.Println(
			"[LoadServices] One or more services were updated with Stripe Product/Price IDs. Saving changes to services.json...",
		)
		for _, s := range services { // Log current state of all services before saving
			log.Printf(
				"[LoadServices] Before SaveServices - Service: %s, ID: %s, StripeProductID: '%s', PriceID: '%s'",
				s.Name,
				s.ID,
				s.StripeProductID,
				s.PriceID,
			)
		}
		if err := SaveServices(services); err != nil {
			return fmt.Errorf("error saving updated services to services.json: %w", err)
		}
		log.Println("[LoadServices] Successfully saved services.json with updated Stripe IDs.")
	}

	// Log the state of services before assigning to AppState
	for _, s := range services {
		log.Printf(
			"[LoadServices] Before AppState assignment - Service: %s, ID: %s, StripeProductID: '%s', PriceID: '%s'",
			s.Name,
			s.ID,
			s.StripeProductID,
			s.PriceID,
		)
	}
	AppState.Services = services
	log.Println("[LoadServices] Finished LoadServices. AppState.Services populated.")
	// Log the state of AppState.Services after assignment
	for _, s_app := range AppState.Services {
		log.Printf(
			"[LoadServices] After AppState assignment (from AppState.Services) - Service: %s, ID: %s, StripeProductID: '%s', PriceID: '%s'",
			s_app.Name,
			s_app.ID,
			s_app.StripeProductID,
			s_app.PriceID,
		)
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
