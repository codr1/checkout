package services

import (
	"fmt"
	"log"
	"strings"

	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/terminal/location"
	"github.com/stripe/stripe-go/v74/terminal/reader"

	"checkout/config"
	"checkout/templates"
)

// ShouldEnableTipping determines if tipping should be enabled for a given transaction
// based on the global configuration, location overrides, transaction amount, and cart contents
func ShouldEnableTipping(transactionAmount float64, cart []templates.Service, locationID string) bool {
	// Check if tipping is globally disabled
	if !config.Config.TippingEnabled {
		// Check for location-specific override that enables tipping
		if locationOverride, exists := config.Config.TippingLocationOverrides[locationID]; exists {
			if !locationOverride {
				return false // Location specifically disables tipping
			}
			// Location enables tipping even though global is disabled
		} else {
			return false // Global disabled and no location override
		}
	} else {
		// Global tipping is enabled, check for location-specific override that disables it
		if locationOverride, exists := config.Config.TippingLocationOverrides[locationID]; exists && !locationOverride {
			return false // Location specifically disables tipping
		}
	}

	// Check minimum amount threshold
	if config.Config.TippingMinAmount > 0 && transactionAmount < config.Config.TippingMinAmount {
		return false
	}

	// Check maximum amount threshold (0 means no maximum)
	if config.Config.TippingMaxAmount > 0 && transactionAmount > config.Config.TippingMaxAmount {
		return false
	}

	// Check service category restrictions
	if len(config.Config.TippingServiceCategoriesOnly) > 0 {
		// Only enable tipping if at least one item in cart matches allowed categories
		hasAllowedCategory := false
		for _, service := range cart {
			for _, allowedCategory := range config.Config.TippingServiceCategoriesOnly {
				if service.Category == allowedCategory {
					hasAllowedCategory = true
					break
				}
			}
			if hasAllowedCategory {
				break
			}
		}
		if !hasAllowedCategory {
			return false
		}
	}

	return true
}

// LoadStripeLocationsAndSelect fetches Stripe Terminal Locations and selects one based on config.
// This function is expected to be called during application initialization.
// It will log.Fatal if a configured location is not found, or if no location is configured
// and zero or multiple locations exist.
func LoadStripeLocationsAndSelect() {
	log.Println("[Terminal] Fetching Stripe Terminal Locations...")
	params := &stripe.TerminalLocationListParams{}
	params.Filters.AddFilter("limit", "", "100") // Adjust limit as needed

	var allLocations []templates.StripeLocation
	i := location.List(params)
	for i.Next() {
		loc := i.TerminalLocation()
		allLocations = append(allLocations, templates.StripeLocation{
			ID:          loc.ID,
			DisplayName: loc.DisplayName,
			Livemode:    loc.Livemode,
		})
	}
	if err := i.Err(); err != nil {
		log.Fatalf("[Terminal] Error listing Stripe Terminal Locations: %v", err)
	}

	AppState.AvailableStripeLocations = allLocations
	log.Printf("[Terminal] Found %d Stripe Terminal Location(s).", len(allLocations))
	for _, loc := range allLocations {
		log.Printf("[Terminal]   - Location: %s (ID: %s, Livemode: %t)", loc.DisplayName, loc.ID, loc.Livemode)
	}

	configuredLocationID := config.Config.StripeTerminalLocationID

	if configuredLocationID != "" {
		log.Printf("[Terminal] Attempting to use configured StripeTerminalLocationID: %s", configuredLocationID)
		for _, loc := range AppState.AvailableStripeLocations {
			if loc.ID == configuredLocationID {
				AppState.SelectedStripeLocation = loc
				log.Printf(
					"[Terminal] Successfully selected Stripe Terminal Location '%s' (ID: %s) from configuration.",
					loc.DisplayName,
					loc.ID,
				)
				return
			}
		}
		// Configured location ID not found
		var availableIDs []string
		for _, loc := range AppState.AvailableStripeLocations {
			availableIDs = append(availableIDs, fmt.Sprintf("'%s' (%s)", loc.DisplayName, loc.ID))
		}
		log.Fatalf(
			"[Terminal] Error: Configured StripeTerminalLocationID '%s' not found in your Stripe account. Available locations: [%s]. Please check your config.json.",
			configuredLocationID,
			strings.Join(availableIDs, ", "),
		)
	} else {
		// No StripeTerminalLocationID configured
		log.Println("[Terminal] StripeTerminalLocationID is not set in config.json.")
		if len(AppState.AvailableStripeLocations) == 0 {
			log.Fatal("[Terminal] Error: No Stripe Terminal Locations found in your Stripe account. Please create a Location in the Stripe Dashboard (Terminal > Locations) and then set its ID as 'stripeTerminalLocationID' in your config.json.")
		} else if len(AppState.AvailableStripeLocations) == 1 {
			AppState.SelectedStripeLocation = AppState.AvailableStripeLocations[0]
			log.Printf("[Terminal] Automatically selected the only available Stripe Terminal Location: '%s' (ID: %s). Consider setting this as 'stripeTerminalLocationID' in your config.json for explicitness.",
				AppState.SelectedStripeLocation.DisplayName, AppState.SelectedStripeLocation.ID)
		} else {
			// Multiple locations found, and none configured
			var availableIDs []string
			for _, loc := range AppState.AvailableStripeLocations {
				availableIDs = append(availableIDs, fmt.Sprintf("'%s' (%s)", loc.DisplayName, loc.ID))
			}
			log.Fatalf("[Terminal] Error: Multiple Stripe Terminal Locations found and 'stripeTerminalLocationID' is not set in config.json. Please set 'stripeTerminalLocationID' to one of the following: [%s].",
				strings.Join(availableIDs, ", "))
		}
	}
}

// LoadStripeReadersForLocation fetches Stripe Terminal Readers for the given Location ID.
// This function is expected to be called after a location has been selected.
func LoadStripeReadersForLocation(locationID string) {
	if locationID == "" {
		log.Println("[Terminal] No Stripe Terminal Location selected, skipping reader loading.")
		return
	}
	log.Printf(
		"[Terminal] Fetching readers for Stripe Terminal Location: %s (ID: %s)...",
		AppState.SelectedStripeLocation.DisplayName,
		locationID,
	)

	params := &stripe.TerminalReaderListParams{}
	params.Location = stripe.String(locationID)
	params.Filters.AddFilter("limit", "", "100") // Adjust limit as needed

	var readersForLocation []templates.StripeReader
	i := reader.List(params)
	for i.Next() {
		r := i.TerminalReader()

		readersForLocation = append(readersForLocation, templates.StripeReader{
			ID:              r.ID,
			Label:           r.Label,
			Livemode:        r.Livemode,
			Status:          string(r.Status),
			DeviceType:      string(r.DeviceType),
			LocationID:      r.Location.ID,
			SerialNumber:    r.SerialNumber,
			IPAddress:       r.IPAddress,
			DeviceSwVersion: r.DeviceSwVersion,
		})
	}
	if err := i.Err(); err != nil {
		// Log as an error but don't make it fatal, as per requirements.
		log.Printf("[Terminal] Error listing Stripe Terminal Readers for Location ID %s: %v", locationID, err)
		AppState.SiteStripeReaders = []templates.StripeReader{} // Ensure it's empty on error
		return
	}

	AppState.SiteStripeReaders = readersForLocation

	if len(AppState.SiteStripeReaders) == 0 {
		log.Printf(
			"[Terminal] No Stripe Terminal readers found for Location: %s (ID: %s). This is not a fatal error.",
			AppState.SelectedStripeLocation.DisplayName,
			locationID,
		)
	} else {
		log.Printf("[Terminal] Found %d reader(s) for Location: %s (ID: %s)", len(AppState.SiteStripeReaders), AppState.SelectedStripeLocation.DisplayName, locationID)
		for _, r := range AppState.SiteStripeReaders {
			log.Printf("[Terminal]   - Reader: Label='%s', ID='%s', Status='%s', DeviceType='%s', Serial='%s', IP='%s', SWVer='%s'",
				r.Label, r.ID, r.Status, r.DeviceType, r.SerialNumber, r.IPAddress, r.DeviceSwVersion)
		}
	}
}
