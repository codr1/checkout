package handlers

import (
	"fmt"
	"log"
	"net/http"

	"checkout/services"
	"checkout/templates/pos"
	"github.com/stripe/stripe-go/v74/terminal/reader"
)

// POSHandler renders the main Point of Sale page.
// It now also handles the logic for selecting a default terminal reader.
func POSHandler(w http.ResponseWriter, r *http.Request) {
	availableReaders := services.AppState.SiteStripeReaders
	currentSelectedReaderID := services.AppState.SelectedReaderID
	isCurrentSelectionValid := false

	if currentSelectedReaderID != "" {
		for _, reader := range availableReaders {
			if reader.ID == currentSelectedReaderID {
				isCurrentSelectionValid = true
				break
			}
		}
	}

	if !isCurrentSelectionValid {
		newSelectedReaderID := ""
		// Try to find the first online reader
		for _, reader := range availableReaders {
			if reader.Status == "online" {
				newSelectedReaderID = reader.ID
				break
			}
		}
		// If no online reader, and readers are available, select the first one
		if newSelectedReaderID == "" && len(availableReaders) > 0 {
			newSelectedReaderID = availableReaders[0].ID
		}

		if newSelectedReaderID != "" {
			log.Printf(
				"[POSHandler] No valid reader selected or previous selection invalid. Defaulting to reader ID: %s",
				newSelectedReaderID,
			)
			services.AppState.SelectedReaderID = newSelectedReaderID
			currentSelectedReaderID = newSelectedReaderID
		} else if len(availableReaders) > 0 {
			// This case means a reader was selected (first in list) but might be offline.
			// services.AppState.SelectedReaderID would have been set above.
			// currentSelectedReaderID is already updated.
			log.Printf("[POSHandler] No online readers. Defaulted to first available reader ID: %s", currentSelectedReaderID)
		} else {
			log.Println("[POSHandler] No readers available to select.")
			// currentSelectedReaderID remains ""
			services.AppState.SelectedReaderID = "" // Ensure it's cleared if no readers
		}
	} else {
		log.Printf("[POSHandler] Using previously selected and valid reader ID: %s", currentSelectedReaderID)
	}

	component := pos.Page(availableReaders, currentSelectedReaderID)
	component.Render(r.Context(), w)
}

// SetSelectedReaderHandler handles the request to change the currently selected Stripe Terminal reader.
func SetSelectedReaderHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Printf("[SetSelectedReaderHandler] Error parsing form: %v", err)
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	readerID := r.FormValue("reader_id")
	if readerID == "" {
		log.Println("[SetSelectedReaderHandler] reader_id is empty")
		w.Header().Set("HX-Trigger", `{"showToast": "No reader ID provided"}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	isValidReader := false
	var selectedReaderLabel string
	for _, reader := range services.AppState.SiteStripeReaders {
		if reader.ID == readerID {
			isValidReader = true
			selectedReaderLabel = reader.Label
			if selectedReaderLabel == "" {
				selectedReaderLabel = reader.ID // Fallback to ID if label is empty
			}
			break
		}
	}

	if !isValidReader {
		log.Printf("[SetSelectedReaderHandler] Invalid reader_id: %s", readerID)
		w.Header().Set("HX-Trigger", `{"showToast": "Invalid reader selected"}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	services.AppState.SelectedReaderID = readerID
	log.Printf("[SetSelectedReaderHandler] Stripe Terminal reader selected: %s (ID: %s)", selectedReaderLabel, readerID)

	toastMessage := fmt.Sprintf("Reader '%s' selected.", selectedReaderLabel)
	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": %q}`, toastMessage))
	w.WriteHeader(http.StatusOK)
	// Optionally, could also trigger a refresh of a part of the page if needed,
	// but for now, just a toast. The POSHandler will pick up the new selection on next full page load/navigation.
	// To make the dropdown visually update immediately without full reload, it would need its own HX-Target.
}

// ClearTerminalTransactionHandler handles clearing any pending terminal transactions
func ClearTerminalTransactionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	selectedReaderID := services.AppState.SelectedReaderID
	if selectedReaderID == "" {
		w.Header().Set("HX-Trigger", `{"showToast": "No terminal reader selected"}`)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Cancel any pending payment intents on the terminal reader
	// This attempts to cancel any ongoing transaction
	_, err := reader.CancelAction(selectedReaderID, nil)
	if err != nil {
		log.Printf("[ClearTerminalTransactionHandler] Error canceling terminal action: %v", err)
		// Even if there's an error (e.g., no action to cancel), we'll still clear our internal state
	}

	// Clear any pending payment intent or transaction state using unified state manager
	// This clears all payment states and the cart
	GlobalPaymentStateManager.ClearAllAndClearCart()

	log.Printf("[ClearTerminalTransactionHandler] Terminal transaction cleared for reader: %s", selectedReaderID)
	
	w.Header().Set("HX-Trigger", `{"showToast": "Terminal transaction cleared successfully"}`)
	w.WriteHeader(http.StatusOK)
}
