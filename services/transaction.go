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
			"Quantity", "Unit Price", "Tax", "Total", "Payment Method",
			"Stripe Customer Email", "Payment Link ID", "Payment Link Status", "Confirmation Code", "Failure Reason",
		}
		if err := writer.Write(headers); err != nil {
			return err
		}
	}

	// For payment link events without products (like cancellations or expirations)
	if len(transaction.Products) == 0 && transaction.PaymentLinkID != "" {
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
			transaction.StripeCustomerEmail,
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

	// Write each product as a separate line
	for i, product := range transaction.Products {
		// Use the stored tax amount for this product
		var tax float64

		if i < len(transaction.ProductTaxes) {
			tax = transaction.ProductTaxes[i]
		} else {
			// This shouldn't happen if taxes have been configured
			tax = 0
		}

		total := product.Price + tax

		record := []string{
			transaction.Date,
			transaction.Time,
			transaction.ID,
			product.Name,
			product.Description,
			"1", // Quantity
			fmt.Sprintf("%.2f", product.Price),
			fmt.Sprintf("%.2f", tax),
			fmt.Sprintf("%.2f", total),
			transaction.PaymentType,
			transaction.StripeCustomerEmail,
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

// LoadProducts loads products from the JSON file
func LoadProducts() error {
	utils.Info("products", "Loading products")

	// Use data directory from config or fallback to constant
	dataDir := config.Config.DataDir
	if dataDir == "" {
		dataDir = "./data"
	}
	productsFilePath := filepath.Join(dataDir, "products.json")

	// Check if products file exists
	if _, err := os.Stat(productsFilePath); os.IsNotExist(err) {
		utils.Error("products", "No products defined", "error", "products.json file not found")
		AppState.Products = []templates.Product{} // Initialize empty products
		return fmt.Errorf("no products defined: products.json file not found")
	}

	// Read existing products
	data, err := os.ReadFile(productsFilePath)
	if err != nil {
		return fmt.Errorf("error reading products: %w", err)
	}

	var products []templates.Product
	if err := json.Unmarshal(data, &products); err != nil {
		return fmt.Errorf("error parsing products: %w", err)
	}

	// Ensure each product has a Stripe Product ID and a default Price ID.
	// Update the products.json file if any changes were made.
	var actualUpdatesMade bool // Correctly named flag
	for i := range products {
		// Assuming EnsureServiceHasPriceID is the one from services/stripe.go
		// which now returns (bool, error)
		updated, err := EnsureServiceHasPriceID(&products[i])
		if err != nil {
			utils.Error("products", "Error ensuring Stripe IDs", "product", products[i].Name, "id", products[i].ID, "error", err)
		}
		if updated {
			actualUpdatesMade = true
		}
	}

	if actualUpdatesMade {
		utils.Debug("products", "Products updated with Stripe IDs, saving changes")
		for _, p := range products { // Log current state of all products before saving
			utils.Debug("products", "Before SaveServices", "product", p.Name, "id", p.ID, "stripe_product_id", p.StripeProductID, "price_id", p.PriceID)
		}
		if err := SaveProducts(products); err != nil {
			return fmt.Errorf("error saving updated products to products.json: %w", err)
		}
		utils.Debug("products", "Successfully saved products.json with updated Stripe IDs")
	}

	// Log the state of products before assigning to AppState
	for _, p := range products {
		utils.Debug("products", "Before AppState assignment", "product", p.Name, "id", p.ID, "stripe_product_id", p.StripeProductID, "price_id", p.PriceID)
	}
	AppState.Products = products
	utils.Debug("products", "Finished LoadServices, AppState.Products populated")
	// Log the state of AppState.Products after assignment
	for _, p_app := range AppState.Products {
		utils.Debug("products", "After AppState assignment", "product", p_app.Name, "id", p_app.ID, "stripe_product_id", p_app.StripeProductID, "price_id", p_app.PriceID)
	}
	return nil
}

// SaveProducts saves the products to the JSON file
func SaveProducts(products []templates.Product) error {
	// Use data directory from config or fallback to constant
	dataDir := config.Config.DataDir
	if dataDir == "" {
		dataDir = "./data"
	}
	productsFilePath := filepath.Join(dataDir, "products.json")

	// Ensure the directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("error creating data directory: %w", err)
	}

	// Marshal the products to JSON
	jsonData, err := json.MarshalIndent(products, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling products: %w", err)
	}

	// Write the JSON to file
	if err := os.WriteFile(productsFilePath, jsonData, 0644); err != nil {
		return fmt.Errorf("error writing products file: %w", err)
	}

	return nil
}
