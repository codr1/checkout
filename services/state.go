package services

import (
	"checkout/templates"
)

// State holds application state
type State struct {
	Products    []templates.Product
	CurrentCart []templates.Product

	// Stripe Terminal state
	AvailableStripeLocations []templates.StripeLocation
	SelectedStripeLocation   templates.StripeLocation
	SiteStripeReaders        []templates.StripeReader
	SelectedReaderID         string // ID of the reader selected by the user

	// Layout context for shared UI state
	LayoutContext templates.LayoutContext
}

// AppState is the global application state instance
var AppState State
