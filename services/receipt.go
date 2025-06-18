package services

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"checkout/config"
	"checkout/templates"
	"checkout/utils"
)

// SaveReceiptRecord saves a receipt record to append-only JSON log
func SaveReceiptRecord(record templates.ReceiptRecord) error {
	// Get receipts directory
	receiptsDir := getReceiptsDir()

	// Create filename with current date
	today := time.Now().Format("2006-01-02")
	filename := filepath.Join(receiptsDir, "receipts-"+today+".json")

	// Ensure directory exists
	if err := os.MkdirAll(receiptsDir, 0755); err != nil {
		return fmt.Errorf("failed to create receipts directory: %v", err)
	}

	// Open file for appending
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open receipts log file: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			utils.Error("receipt", "Error closing receipts log file", "error", err)
		}
	}()

	// Marshal to JSON and append
	jsonData, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("error marshaling receipt record: %v", err)
	}

	// Write with newline
	if _, err := file.Write(append(jsonData, '\n')); err != nil {
		return fmt.Errorf("error writing receipt record: %v", err)
	}

	utils.Info("receipt", "Receipt record saved", "payment_id", record.ID, "delivery_method", record.DeliveryMethod)
	return nil
}

// SavePaymentUpdateRecord saves a payment update record to append-only JSON log
func SavePaymentUpdateRecord(record templates.PaymentUpdateRecord) error {
	// Get updates directory
	updatesDir := getUpdatesDir()

	// Create filename with current date
	today := time.Now().Format("2006-01-02")
	filename := filepath.Join(updatesDir, "payment-updates-"+today+".json")

	// Ensure directory exists
	if err := os.MkdirAll(updatesDir, 0755); err != nil {
		return fmt.Errorf("failed to create updates directory: %v", err)
	}

	// Open file for appending
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open updates log file: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			utils.Error("payment", "Error closing updates log file", "error", err)
		}
	}()

	// Marshal to JSON and append
	jsonData, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("error marshaling payment update record: %v", err)
	}

	// Write with newline
	if _, err := file.Write(append(jsonData, '\n')); err != nil {
		return fmt.Errorf("error writing payment update record: %v", err)
	}

	utils.Info("payment", "Payment update record saved", "payment_id", record.PaymentID, "update_type", record.UpdateType)
	return nil
}

// CreateReceiptRecord creates a new receipt record with current timestamp
func CreateReceiptRecord(paymentID, email, phone, deliveryMethod, status string) templates.ReceiptRecord {
	now := time.Now()
	return templates.ReceiptRecord{
		ID:             paymentID,
		Date:           now.Format("01/02/2006"),
		Time:           now.Format("15:04:05"),
		ReceiptEmail:   email,
		ReceiptPhone:   phone,
		DeliveryMethod: deliveryMethod,
		DeliveryStatus: status,
		RetryCount:     0,
	}
}

// CreatePaymentUpdateRecord creates a new payment update record with current timestamp
func CreatePaymentUpdateRecord(paymentID, updateType, oldValue, newValue, fieldName, source, notes string) templates.PaymentUpdateRecord {
	now := time.Now()
	return templates.PaymentUpdateRecord{
		PaymentID:  paymentID,
		UpdateDate: now.Format("01/02/2006"),
		UpdateTime: now.Format("15:04:05"),
		UpdateType: updateType,
		OldValue:   oldValue,
		NewValue:   newValue,
		FieldName:  fieldName,
		Source:     source,
		Notes:      notes,
	}
}

// UpdateReceiptDeliveryStatus updates an existing receipt record's delivery status
func UpdateReceiptDeliveryStatus(paymentID, status, errorMessage string) error {
	// Create an update record for the status change
	updateRecord := CreatePaymentUpdateRecord(
		paymentID,
		"receipt_delivery_status",
		"",
		status,
		"delivery_status",
		"receipt_system",
		errorMessage,
	)

	return SavePaymentUpdateRecord(updateRecord)
}

// LogStripeCustomerInfo logs when Stripe provides customer information (e.g., from QR payments)
func LogStripeCustomerInfo(paymentID, stripeEmail string) error {
	updateRecord := CreatePaymentUpdateRecord(
		paymentID,
		"stripe_customer_info",
		"",
		stripeEmail,
		"stripe_customer_email",
		"stripe_payment_link",
		"Email collected by Stripe during QR payment",
	)

	return SavePaymentUpdateRecord(updateRecord)
}

// Helper functions

func getReceiptsDir() string {
	if config.Config.TransactionsDir != "" {
		return filepath.Join(config.Config.TransactionsDir, "receipts")
	}
	return filepath.Join(config.DefaultTransactionsDir, "receipts")
}

func getUpdatesDir() string {
	if config.Config.TransactionsDir != "" {
		return filepath.Join(config.Config.TransactionsDir, "updates")
	}
	return filepath.Join(config.DefaultTransactionsDir, "updates")
}
