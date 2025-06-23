package services

import (
	"errors"
	"fmt"

	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/paymentlink"
	"github.com/stripe/stripe-go/v74/price"
	"github.com/stripe/stripe-go/v74/product"

	"checkout/config"
	"checkout/templates"
	"checkout/utils"

	"github.com/stripe/stripe-go/v74/checkout/session"
)

// GetStripePublicKey returns the Stripe public key
func GetStripePublicKey() string {
	return config.GetStripePublicKey()
}

// EnsureServiceHasPriceID ensures the service has a valid Stripe Product ID and a valid default Price ID.
// It validates existing IDs and creates new ones if they are missing or invalid.
// It returns true if the service struct was updated.
func EnsureServiceHasPriceID(service *templates.Product) (bool, error) {
	originalStripeProductID := service.StripeProductID
	originalPriceID := service.PriceID
	var sErr *stripe.Error

	// --- Validate or Create Stripe Product ID ---
	if service.StripeProductID != "" {
		p, err := product.Get(service.StripeProductID, nil)
		if err != nil {
			if stripeErr, ok := err.(*stripe.Error); ok && stripeErr.Code == stripe.ErrorCodeResourceMissing {
				utils.Debug("stripe", "Stripe Product ID not found, will create new one", "product_id", service.StripeProductID, "service", service.Name)
				service.StripeProductID = "" // Mark for creation
				service.PriceID = ""         // Old PriceID is definitely invalid
			} else {
				// Other Stripe error or network issue
				return false, fmt.Errorf("error validating Stripe Product ID '%s' for service '%s': %w", service.StripeProductID, service.Name, err)
			}
		} else if p == nil || !p.Active {
			// Product found but is nil (shouldn't happen if no error) or inactive
			utils.Debug("stripe", "Stripe Product ID is inactive or invalid, will create new one", "product_id", service.StripeProductID, "service", service.Name)
			service.StripeProductID = ""
			service.PriceID = ""
		}
		// If product is valid and active, keep service.StripeProductID
	}

	if service.StripeProductID == "" {
		utils.Info("stripe", "Creating new Stripe Product for service", "service", service.Name, "original_product_id", originalStripeProductID)
		productParams := &stripe.ProductParams{
			Name:        stripe.String(service.Name),
			Description: stripe.String(service.Description),
		}
		newProduct, err := product.New(productParams)
		if err != nil {
			return false, fmt.Errorf("error creating new Stripe product for service '%s': %w", service.Name, err)
		}
		service.StripeProductID = newProduct.ID
		service.PriceID = "" // Ensure new price is created for this new product
		utils.Info("stripe", "Created new Stripe Product", "service", service.Name, "product_id", service.StripeProductID)
	}

	// --- Validate or Create Stripe Price ID ---
	if service.PriceID != "" {
		if service.StripeProductID == "" { // Should have a product ID by now
			service.PriceID = "" // Cannot validate price without product
			utils.Warn("stripe", "Cleared PriceID because StripeProductID is missing before price validation", "service", service.Name)
		} else {
			pr, err := price.Get(service.PriceID, nil)
			if err != nil {
				if stripeErr, ok := err.(*stripe.Error); ok && stripeErr.Code == stripe.ErrorCodeResourceMissing {
					utils.Debug("stripe", "Stripe Price ID not found, will create new one", "price_id", service.PriceID, "service", service.Name, "product_id", service.StripeProductID)
					service.PriceID = "" // Mark for creation
				} else {
					return false, fmt.Errorf("error validating Stripe Price ID '%s' for service '%s': %w", service.PriceID, service.Name, err)
				}
			} else if pr == nil || !pr.Active || pr.Product == nil || pr.Product.ID != service.StripeProductID {
				// Price found but is nil, inactive, or doesn't belong to the service's StripeProduct
				priceProductID := SafeStrPtr(pr.Product, func(p *stripe.Product) string { return p.ID })
				utils.Debug("stripe", "Stripe Price ID is inactive, invalid, or mismatched, will create new one",
					"price_id", service.PriceID, "service", service.Name, "expected_product_id", service.StripeProductID, "actual_product_id", priceProductID)
				service.PriceID = ""
			}
			// If price is valid, active, and matches product, keep service.PriceID
		}
	}

	if service.PriceID == "" {
		if service.StripeProductID == "" { // Safeguard
			return false, fmt.Errorf(
				"cannot create PriceID for service '%s': StripeProductID is still missing",
				service.Name,
			)
		}
		utils.Info("stripe", "Creating new Stripe Price for service", "service", service.Name, "product_id", service.StripeProductID, "original_price_id", originalPriceID)
		priceParams := &stripe.PriceParams{
			Currency:   stripe.String(string(stripe.CurrencyUSD)),
			UnitAmount: stripe.Int64(int64(service.Price * 100)),
			Product:    stripe.String(service.StripeProductID),
			Nickname:   stripe.String(fmt.Sprintf("Default price for %s", service.Name)),
		}
		newPrice, err := price.New(priceParams)
		if err != nil {
			if errors.As(err, &sErr) && sErr.Code == stripe.ErrorCode("price_missing_product") {
				utils.Error("stripe", "Attempted to create price for non-existent product", "product_id", service.StripeProductID, "service", service.Name)
			}
			return false, fmt.Errorf(
				"error creating new Stripe price for service '%s' (Product: %s): %w",
				service.Name,
				service.StripeProductID,
				err,
			)
		}
		service.PriceID = newPrice.ID
		utils.Info("stripe", "Created new Stripe Price", "service", service.Name, "price_id", service.PriceID)
	}

	// Determine if any IDs were actually changed or assigned
	updated := (service.StripeProductID != originalStripeProductID && service.StripeProductID != "") ||
		(service.PriceID != originalPriceID && service.PriceID != "") ||
		(originalStripeProductID == "" && service.StripeProductID != "") || // Case: ProductID was initially empty and got set
		(originalPriceID == "" && service.PriceID != "") // Case: PriceID was initially empty and got set

	return updated, nil
}

// SafeStrPtr safely dereferences a pointer to a struct and then a string field from it.
// Used for logging potentially nil product pointers from price objects.
func SafeStrPtr[T any, R any](ptr *T, fieldGetter func(*T) R) string {
	if ptr == nil {
		return "<nil>"
	}
	val := fieldGetter(ptr)
	// Assuming R is a string or can be stringified.
	// If R could also be a pointer, more indirection checks would be needed.
	return fmt.Sprintf("%v", val)
}

// PaymentLinkStatus represents status of a payment link
type PaymentLinkStatus struct {
	Active        bool
	Completed     bool
	CustomerEmail string
}

// CreatePaymentLink creates a payment link for the current cart
func CreatePaymentLink(totalAmount float64, email string) (*stripe.PaymentLink, error) {
	utils.Debug("stripe", "Creating payment link - cart contents", "total_amount", totalAmount, "email", email)
	for i, cartItem := range AppState.CurrentCart {
		utils.Debug("stripe", "Cart item", "index", i, "name", cartItem.Name, "id", cartItem.ID, "stripe_product_id", cartItem.StripeProductID, "price_id", cartItem.PriceID)
	}

	// Create payment link params
	params := &stripe.PaymentLinkParams{}

	// DO NOT enable automatic tax calculation - we calculate locally
	// params.AutomaticTax = &stripe.PaymentLinkAutomaticTaxParams{
	//     Enabled: stripe.Bool(true),
	// }

	// Add line items by creating a new Price object for each service
	for _, service := range AppState.CurrentCart {
		taxRate := GetTaxRateForService(service)
		serviceTotalWithTax := service.Price * (1 + taxRate)

		// Create a temporary Price object for this service with tax included,
		// linked to the actual Stripe Product.
		if service.StripeProductID == "" {
			utils.Error("stripe", "Service missing StripeProductID, cannot create payment link line item", "service", service.Name)
			return nil, fmt.Errorf("service '%s' is missing StripeProductID", service.Name)
		}

		priceParams := &stripe.PriceParams{
			Currency:    stripe.String(string(stripe.CurrencyUSD)),
			UnitAmount:  stripe.Int64(int64(serviceTotalWithTax * 100)),          // Price in cents, includes local tax
			Product:     stripe.String(service.StripeProductID),                  // Link to the existing Stripe Product
			TaxBehavior: stripe.String(string(stripe.PriceTaxBehaviorInclusive)), // Indicates UnitAmount includes tax
			// Nickname can be useful for identifying these temporary prices in Stripe logs/dashboard
			Nickname: stripe.String(fmt.Sprintf("Payment Link item for %s (tax incl.)", service.Name)),
		}
		tempPrice, err := price.New(priceParams)
		if err != nil {
			utils.Error("stripe", "Error creating temporary Stripe price for payment link", "service", service.Name, "product_id", service.StripeProductID, "error", err)
			return nil, fmt.Errorf("error creating temporary price for service %s: %w", service.Name, err)
		}

		// Add line item using the ID of the temporary Price
		params.LineItems = append(params.LineItems, &stripe.PaymentLinkLineItemParams{
			Price:    stripe.String(tempPrice.ID),
			Quantity: stripe.Int64(1),
		})
	}

	// Only set custom success URL in webhook mode
	// In polling mode, let Stripe use their default success page
	if config.GetCommunicationStrategy() == "webhooks" {
		baseURL := "https://" + config.Config.WebsiteName
		params.AfterCompletion = &stripe.PaymentLinkAfterCompletionParams{
			Type: stripe.String(string(stripe.PaymentLinkAfterCompletionTypeRedirect)),
			Redirect: &stripe.PaymentLinkAfterCompletionRedirectParams{
				URL: stripe.String(baseURL + "/payment-success"),
			},
		}
		utils.Debug("stripe", "Using custom success URL for webhook mode", "url", baseURL+"/payment-success")
	} else {
		// Polling mode - don't set AfterCompletion, let Stripe use default success page
		utils.Debug("stripe", "Using default Stripe success page for polling mode")
	}

	// Create the payment link
	return paymentlink.New(params)
}

// CheckPaymentLinkStatus checks the status of a payment link
func CheckPaymentLinkStatus(paymentLinkID string) (PaymentLinkStatus, error) {
	// Retrieve the payment link from Stripe to check status
	pl, err := paymentlink.Get(paymentLinkID, nil)
	if err != nil {
		return PaymentLinkStatus{}, fmt.Errorf("error retrieving payment link: %w", err)
	}

	// Query for checkout sessions associated with this payment link
	params := &stripe.CheckoutSessionListParams{}
	params.PaymentLink = stripe.String(paymentLinkID)

	// Check for completed checkout sessions and extract customer email
	i := session.List(params)
	hasCompletedPayment := false
	var customerEmail string

	// Check if we find any completed checkout sessions for this payment link
	for i.Next() {
		s := i.CheckoutSession()
		if s.Status == "complete" {
			hasCompletedPayment = true
			// Extract customer email from the checkout session
			if s.CustomerDetails != nil && s.CustomerDetails.Email != "" {
				customerEmail = s.CustomerDetails.Email
			}
			break
		}
	}

	if err := i.Err(); err != nil {
		utils.Error("stripe", "Error checking checkout sessions", "error", err)
	}

	// Return the status
	return PaymentLinkStatus{
		Active:        pl.Active,
		Completed:     hasCompletedPayment,
		CustomerEmail: customerEmail,
	}, nil
}
