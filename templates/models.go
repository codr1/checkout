package templates

// Service represents a service item that can be sold
type Service struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Description     string  `json:"description"`
	Price           float64 `json:"price"`
	StripeProductID string  `json:"stripeProductID,omitempty"` // Stripe Product ID (e.g., prod_xxxxxxxxxxxxxx)
	PriceID         string  `json:"priceID,omitempty"`         // Stripe Price ID (e.g., price_xxxxxxxxxxxxxx) for the default price
	Category        string  `json:"category,omitempty"`        // Tax category ID
}

// CartSummary contains the cart totals
type CartSummary struct {
	Subtotal float64
	Tax      float64
	Total    float64
}

// Transaction represents a completed sale
type Transaction struct {
	ID            string    `json:"id"`
	Date          string    `json:"date"`
	Time          string    `json:"time"`
	Services      []Service `json:"services"`
	Subtotal      float64   `json:"subtotal"`
	Tax           float64   `json:"tax"`
	Total         float64   `json:"total"`
	PaymentType   string    `json:"paymentType"`
	CustomerEmail string    `json:"customerEmail,omitempty"`
	CustomerPhone string    `json:"customerPhone,omitempty"`
	ReceiptSent   bool      `json:"receiptSent,omitempty"`

	// Payment link tracking fields
	PaymentLinkID     string `json:"paymentLinkID,omitempty"`
	PaymentLinkStatus string `json:"paymentLinkStatus,omitempty"`
	ConfirmationCode  string `json:"confirmationCode,omitempty"`
	FailureReason     string `json:"failureReason,omitempty"`
}

// TaxCategory represents a product category with its own tax rate
type TaxCategory struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	TaxRate float64 `json:"tax_rate"` // Decimal rate (e.g., 0.0625 for 6.25%)
}

// AppConfig represents the application configuration
type AppConfig struct {
	// Stripe configuration
	StripeSecretKey     string `json:"stripeSecretKey"`
	StripePublicKey     string `json:"stripePublicKey"`
	StripeWebhookSecret string `json:"stripeWebhookSecret"`

	// Authentication
	Password string `json:"password"`

	// Business information
	BusinessName   string `json:"businessName"`
	BusinessStreet string `json:"businessStreet"`
	BusinessCity   string `json:"businessCity"`
	BusinessState  string `json:"businessState"`
	BusinessZIP    string `json:"businessZIP"`

	// Tax information
	BusinessTaxID  string `json:"businessTaxID"`
	SalesTaxNumber string `json:"salesTaxNumber"`
	VATNumber      string `json:"vatNumber"`

	// Website information
	WebsiteName string `json:"websiteName"`

	// Customer default location
	DefaultCity  string `json:"defaultCity"`
	DefaultState string `json:"defaultState"`

	// Tax configuration
	DefaultTaxRate float64       `json:"defaultTaxRate"`
	TaxCategories  []TaxCategory `json:"taxCategories"`

	// System configuration
	Port            string `json:"port"`
	DataDir         string `json:"dataDir"`
	TransactionsDir string `json:"transactionsDir"`

	// Stripe Terminal
	StripeTerminalLocationID string `json:"stripeTerminalLocationID,omitempty"` // ID of the Stripe Terminal Location (tml_...)

	// AWS SNS Configuration (for SMS receipts)
	AWSAccessKeyID     string `json:"awsAccessKeyId"`     // AWS Access Key ID
	AWSSecretAccessKey string `json:"awsSecretAccessKey"` // AWS Secret Access Key
	AWSRegion          string `json:"awsRegion"`          // AWS Region (e.g., us-east-1)

	// Tipping Configuration
	TippingEnabled               bool            `json:"tippingEnabled"`               // Global tipping enable/disable
	TippingLocationOverrides     map[string]bool `json:"tippingLocationOverrides"`     // Per-location tipping overrides (locationID -> enabled)
	TippingMinAmount             float64         `json:"tippingMinAmount"`             // Minimum transaction amount to show tipping (in dollars)
	TippingMaxAmount             float64         `json:"tippingMaxAmount"`             // Maximum transaction amount to show tipping (0 = no limit)
	TippingPresetPercentages     []int           `json:"tippingPresetPercentages"`     // Preset tip percentages (e.g., [15, 18, 20, 25])
	TippingAllowCustomAmount     bool            `json:"tippingAllowCustomAmount"`     // Allow customers to enter custom tip amounts
	TippingServiceCategoriesOnly []string        `json:"tippingServiceCategoriesOnly"` // Only show tipping for specific service categories (empty = all)
}

// StripeLocation represents a Stripe Terminal Location.
type StripeLocation struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Livemode    bool   `json:"livemode"`
	// Add other fields from stripe.TerminalLocationParams if needed
}

// StripeReader represents a Stripe Terminal reader.
type StripeReader struct {
	ID              string `json:"id"` // Reader ID (tmr_...)
	Label           string `json:"label"`
	Livemode        bool   `json:"livemode"`
	Status          string `json:"status"`      // e.g., "online", "offline"
	DeviceType      string `json:"device_type"` // e.g., "stripe_m2", "verifone_P400"
	LocationID      string `json:"location_id"` // tml_...
	SerialNumber    string `json:"serial_number"`
	IPAddress       string `json:"ip_address,omitempty"`
	DeviceSwVersion string `json:"device_sw_version,omitempty"`
}
