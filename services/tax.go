package services

import (
	"checkout/config"
	"checkout/templates"
)

// Calculate cart summary using local tax rates
func CalculateCartSummary() templates.CartSummary {
	summary, _ := CalculateCartSummaryWithItemTaxes()
	return summary
}

// CalculateCartSummaryWithItemTaxes calculates cart summary and returns per-item tax amounts
func CalculateCartSummaryWithItemTaxes() (templates.CartSummary, []float64) {
	var subtotal float64
	var itemTaxes []float64

	for _, product := range AppState.CurrentCart {
		subtotal += product.Price

		// Calculate tax for this specific product
		taxRate := GetTaxRateForService(product)
		tax := product.Price * taxRate
		itemTaxes = append(itemTaxes, tax)
	}

	// Calculate total tax by summing individual taxes
	var totalTax float64
	for _, tax := range itemTaxes {
		totalTax += tax
	}

	total := subtotal + totalTax

	summary := templates.CartSummary{
		Subtotal: subtotal,
		Tax:      totalTax,
		Total:    total,
	}

	return summary, itemTaxes
}

// GetTaxRateForService returns the applicable tax rate for a service
func GetTaxRateForService(service templates.Product) float64 {
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
