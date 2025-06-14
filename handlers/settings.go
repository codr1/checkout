package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"checkout/config"
	"checkout/templates/settings"
	"checkout/utils"
)

// SettingsHandler handles the settings page
func SettingsHandler(w http.ResponseWriter, r *http.Request) {
	component := settings.SettingsPage()
	component.Render(r.Context(), w)
}

// SettingsSearchHandler handles searching settings
func SettingsSearchHandler(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(r.URL.Query().Get("q"))
	if query == "" {
		// If no query, show all sections
		component := settings.SettingsSections()
		component.Render(r.Context(), w)
		return
	}

	// Search through settings and mark matches
	matches := make(map[string]bool)
	for section, sectionSettings := range config.SettingsMap {
		for key, value := range sectionSettings {
			// Convert value to string for searching
			valueStr := fmt.Sprintf("%v", value)
			if strings.Contains(strings.ToLower(key), query) || strings.Contains(strings.ToLower(valueStr), query) {
				matches[section] = true
				break
			}
		}
	}

	// Render sections with matches
	component := settings.SettingsSections()
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

	// Get the setting name and value
	settingName := r.Form.Get("name")
	settingValue := r.Form.Get("value")

	// Find the section and key in the settings map
	for section, sectionSettings := range config.SettingsMap {
		for key := range sectionSettings {
			if key == settingName {
				// Convert value to appropriate type based on current value
				currentValue := sectionSettings[key]
				var newValue interface{}
				switch currentValue.(type) {
				case bool:
					newValue = settingValue == "true"
				case float64:
					var floatValue float64
					if _, err := fmt.Sscanf(settingValue, "%f", &floatValue); err != nil {
						http.Error(w, "Invalid numeric value", http.StatusBadRequest)
						return
					}
					newValue = floatValue
				default:
					newValue = settingValue
				}

				// Update the setting
				if err := config.SetSetting(section, key, newValue); err != nil {
					utils.Error("settings", "Error updating setting", "error", err)
					http.Error(w, "Error updating setting", http.StatusInternalServerError)
					return
				}

				w.WriteHeader(http.StatusOK)
				return
			}
		}
	}

	http.Error(w, "Unknown setting", http.StatusBadRequest)
}
