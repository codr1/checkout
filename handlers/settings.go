package handlers

import (
	"net/http"
	"strings"

	"checkout/config"
	"checkout/templates/settings"
	"checkout/utils"
)

// SettingsHandler handles the settings page
func SettingsHandler(w http.ResponseWriter, r *http.Request) {
	// Send HX-Trigger header to show the modal
	w.Header().Set("HX-Trigger", "showModal")
	component := settings.SettingsPage()
	component.Render(r.Context(), w)
}

// SettingsSearchHandler handles searching settings
func SettingsSearchHandler(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	utils.Debug("settings", "Search request received", "query", query, "url", r.URL.String())

	// If no query, show all settings
	if query == "" {
		component := settings.SettingsSections()
		component.Render(r.Context(), w)
		return
	}

	// Filter settings based on search query
	component := settings.FilteredSettingsSections(query)
	component.Render(r.Context(), w)
}

// SettingsUpdateHandler handles updating settings
func SettingsUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse form data
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	// Get the field name and value
	fieldName := r.Form.Get("name")
	fieldValue := r.Form.Get("value")

	// Update config field using reflection
	if err := config.UpdateConfigField(fieldName, fieldValue); err != nil {
		utils.Error("settings", "Error updating setting", "field", fieldName, "error", err)
		http.Error(w, "Error updating setting", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
