package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"checkout/services"
	"checkout/templates"
	"checkout/templates/checkout"
	"checkout/templates/pos"
	"checkout/utils"
)

// ServicesHandler renders the services list
func ServicesHandler(w http.ResponseWriter, r *http.Request) {
	component := pos.ServicesList(services.AppState.Services)
	err := component.Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// CartHandler renders the cart contents
func CartHandler(w http.ResponseWriter, r *http.Request) {
	utils.Debug("cart", "CartHandler called", "cart_items", len(services.AppState.CurrentCart))

	summary := services.CalculateCartSummary()

	component := pos.CartView(services.AppState.CurrentCart, summary)
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

	for _, service := range services.AppState.Services {
		if service.ID == serviceID {
			services.AppState.CurrentCart = append(services.AppState.CurrentCart, service)
			w.Header().Set("HX-Trigger", "cartUpdated")
			return
		}
	}

	http.Error(w, "Service not found", http.StatusNotFound)
}

// AddCustomServiceHandler adds a custom service to the cart
func AddCustomServiceHandler(w http.ResponseWriter, r *http.Request) {
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
	customService := templates.Service{
		ID:          fmt.Sprintf("custom-%d", time.Now().UnixNano()),
		Name:        name,
		Description: description,
		Price:       price,
	}

	// Add to cart
	services.AppState.CurrentCart = append(services.AppState.CurrentCart, customService)
	w.Header().Set("HX-Trigger", "cartUpdated")
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
