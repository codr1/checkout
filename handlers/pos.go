package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"checkout/services"
	"checkout/templates"
	"checkout/templates/checkout"
	"checkout/templates/pos"
	"checkout/utils"
)

// ProductsHandler renders the products list
func ProductsHandler(w http.ResponseWriter, r *http.Request) {
	products := services.GetCurrentProducts()
	subcategories := services.GetCurrentSubcategories()
	currentPath := services.AppState.CategoryData.CurrentPath

	component := pos.ProductsList(products, subcategories, currentPath)
	err := component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// NavigateCategoryHandler handles category navigation
func NavigateCategoryHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	// Debug: log all form values
	utils.Debug("category", "NavigateCategoryHandler called", "form", r.Form)

	// Parse the path from form data
	// HTMX sends array values as multiple form fields with the same name
	pathValues := r.Form["path"]
	var path []string

	utils.Debug("category", "Raw path values", "pathValues", pathValues)

	// Handle different ways HTMX might send the array
	if len(pathValues) > 0 {
		for _, pathValue := range pathValues {
			if pathValue != "" && pathValue != "[]" {
				// Try to parse as JSON array first
				if strings.HasPrefix(pathValue, "[") && strings.HasSuffix(pathValue, "]") {
					// Remove brackets and split by comma
					pathStr := strings.Trim(pathValue, "[]")
					if pathStr != "" {
						parts := strings.Split(pathStr, ",")
						for _, part := range parts {
							trimmed := strings.Trim(strings.TrimSpace(part), `"`)
							if trimmed != "" {
								path = append(path, trimmed)
							}
						}
					}
				} else {
					// Single value, add it directly
					path = append(path, pathValue)
				}
			}
		}
	}

	utils.Debug("category", "Parsed path", "path", path)

	// Navigate to the category
	services.AppState.CategoryData.CurrentPath = path

	utils.Debug("category", "Updated current path", "currentPath", services.AppState.CategoryData.CurrentPath)

	// Return updated products view
	w.Header().Set("HX-Trigger", "categoryChanged")
	ProductsHandler(w, r)
}

// CartItemsHandler renders only the cart items (for scrollable area)
func CartItemsHandler(w http.ResponseWriter, r *http.Request) {
	utils.Debug("cart", "CartItemsHandler called", "cart_items", len(services.AppState.CurrentCart))

	component := pos.CartItems(services.AppState.CurrentCart)
	err := component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// CartSummaryHandler renders only the cart summary (for fixed bottom area)
func CartSummaryHandler(w http.ResponseWriter, r *http.Request) {
	utils.Debug("cart", "CartSummaryHandler called", "cart_items", len(services.AppState.CurrentCart))

	summary := services.CalculateCartSummary()

	component := pos.CartSummary(summary)
	err := component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// CheckoutFormHandler renders the checkout form
func CheckoutFormHandler(w http.ResponseWriter, r *http.Request) {
	component := checkout.Form()
	err := component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// AddToCartHandler adds a service to the cart
func AddToCartHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	serviceID := r.FormValue("id")

	for _, product := range services.AppState.Products {
		if product.ID == serviceID {
			services.AppState.CurrentCart = append(services.AppState.CurrentCart, product)
			w.Header().Set("HX-Trigger", `{"cartUpdated": true, "scrollCartToBottom": true}`)
			return
		}
	}

	http.Error(w, "Service not found", http.StatusNotFound)
}

// AddCustomProductHandler adds a custom product to the cart
func AddCustomProductHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	description := r.FormValue("description")
	priceStr := r.FormValue("price")

	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		http.Error(w, "Invalid price", http.StatusBadRequest)
		return
	}

	// Create custom service
	customProduct := templates.Product{
		ID:          fmt.Sprintf("custom-%d", time.Now().UnixNano()),
		Name:        name,
		Description: description,
		Price:       price,
	}

	// Add to cart
	services.AppState.CurrentCart = append(services.AppState.CurrentCart, customProduct)
	w.Header().Set("HX-Trigger", `{"cartUpdated": true, "scrollCartToBottom": true, "closeModal": true}`)
}

// RemoveFromCartHandler removes an item from the cart
func RemoveFromCartHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	indexStr := r.FormValue("index")
	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 || index >= len(services.AppState.CurrentCart) {
		http.Error(w, "Invalid index", http.StatusBadRequest)
		return
	}

	// Remove item at index
	services.AppState.CurrentCart = append(
		services.AppState.CurrentCart[:index],
		services.AppState.CurrentCart[index+1:]...)
	w.Header().Set("HX-Trigger", "cartUpdated")
}

// TriggerCartUpdateHandler sends a cartUpdated event to refresh the cart display
// This is used by SSE events when payment completes to refresh the cart
func TriggerCartUpdateHandler(w http.ResponseWriter, r *http.Request) {
	utils.Debug("cart", "Triggering cart update event")
	w.Header().Set("HX-Trigger", "cartUpdated")
	w.WriteHeader(http.StatusOK)
}
