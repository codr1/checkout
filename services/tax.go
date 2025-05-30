package services

import (
	"checkout/config"
	"checkout/templates"
)

// Calculate cart summary using local tax rates
func CalculateCartSummary() (templates.CartSummary, error) {
	var subtotal float64
	for _, service := range AppState.CurrentCart {
		subtotal += service.Price
	}

	// Calculate tax using local tax rates
	tax := calculateLocalTaxes()
	total := subtotal + tax

	return templates.CartSummary{
		Subtotal: subtotal,
		Tax:      tax,
		Total:    total,
	}, nil
}

// calculateLocalTaxes calculates taxes for the current cart using local tax rates
func calculateLocalTaxes() float64 {
	if len(AppState.CurrentCart) == 0 {
		return 0
	}

	var totalTax float64

	for _, service := range AppState.CurrentCart {
		taxRate := GetTaxRateForService(service)
		tax := service.Price * taxRate
		totalTax += tax
	}

	return totalTax
}

// GetTaxRateForService returns the applicable tax rate for a service
func GetTaxRateForService(service templates.Service) float64 {
	// If service has a category, look up the category tax rate
	if service.Category != "" {
		for _, category := range config.Config.TaxCategories {
			if category.ID == service.Category {
				return category.TaxRate
			}
		}
	}

	// Fall back to default tax rate
	return config.Config.DefaultTaxRate
}
