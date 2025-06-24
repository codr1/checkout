package services

import (
	"checkout/templates"
	"strings"
)

// CategoryData holds the parsed category navigation structure
type CategoryData struct {
	// Current navigation path (e.g., ["cat1", "cat2"])
	CurrentPath []string

	// Quick lookup: path -> direct subcategories
	// "cat1" -> ["cat2", "cat3"]
	// "" -> ["cat1", "cat4"] (root level)
	Subcategories map[string][]string

	// Quick lookup: path -> products directly in this category
	// "cat1" -> [prod5]
	// "" -> [prod1, prod2, prod3, prod4] (uncategorized)
	DirectProducts map[string][]templates.Product
}

// State holds application state
type State struct {
	Products    []templates.Product
	CurrentCart []templates.Product

	// Category navigation state
	CategoryData CategoryData

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

// BuildCategoryData builds the category navigation structure from products
func BuildCategoryData(products []templates.Product) CategoryData {
	data := CategoryData{
		CurrentPath:    []string{},
		Subcategories:  make(map[string][]string),
		DirectProducts: make(map[string][]templates.Product),
	}

	for _, product := range products {
		categoryPath := product.Category

		if categoryPath == "" {
			// Uncategorized product goes to root
			data.DirectProducts[""] = append(data.DirectProducts[""], product)
		} else {
			// Parse category path (e.g., "cat1/cat2/cat3")
			parts := strings.Split(categoryPath, "/")

			// Add product to its direct category
			data.DirectProducts[categoryPath] = append(data.DirectProducts[categoryPath], product)

			// Build subcategory relationships
			for i := 0; i < len(parts); i++ {
				currentPath := strings.Join(parts[:i], "/")
				nextCategory := parts[i]

				// Add this category to parent's subcategories if not already there
				found := false
				for _, existing := range data.Subcategories[currentPath] {
					if existing == nextCategory {
						found = true
						break
					}
				}
				if !found {
					data.Subcategories[currentPath] = append(data.Subcategories[currentPath], nextCategory)
				}
			}
		}
	}

	return data
}

// GetCurrentSubcategories returns subcategories for the current path
func GetCurrentSubcategories() []string {
	currentPath := strings.Join(AppState.CategoryData.CurrentPath, "/")
	return AppState.CategoryData.Subcategories[currentPath]
}

// GetCurrentProducts returns products for the current path
func GetCurrentProducts() []templates.Product {
	currentPath := strings.Join(AppState.CategoryData.CurrentPath, "/")
	return AppState.CategoryData.DirectProducts[currentPath]
}
