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
	StripeSecretKey          string `json:"stripeSecretKey" setting:"section:stripe,label:Stripe Secret Key,type:password,id:stripe-secret-key,help:Your Stripe secret key from the dashboard"`
	StripePublicKey          string `json:"stripePublicKey" setting:"section:stripe,label:Stripe Public Key,type:text,id:stripe-public-key,help:Your Stripe publishable key from the dashboard"`
	StripeWebhookSecret      string `json:"stripeWebhookSecret" setting:"section:stripe,label:Stripe Webhook Secret,type:password,id:stripe-webhook-secret,help:Webhook endpoint secret for Stripe events"`
	StripeTerminalLocationID string `json:"stripeTerminalLocationID,omitempty" setting:"section:stripe,label:Terminal Location,type:text,id:stripe-terminal-location,help:ID of the Stripe Terminal Location (tml_...)"`

	// Authentication (hidden from settings UI)
	Password string `json:"password" setting:"-"`

	// Business information
	BusinessName   string `json:"businessName" setting:"section:business,label:Business Name,type:text,id:business-name,help:Your business or company name"`
	BusinessStreet string `json:"businessStreet" setting:"section:business,label:Street Address,type:text,id:business-street,help:Street address for your business"`
	BusinessCity   string `json:"businessCity" setting:"section:business,label:City,type:text,id:business-city,help:City where your business is located"`
	BusinessState  string `json:"businessState" setting:"section:business,label:State,type:text,id:business-state,help:State or province where your business is located"`
	BusinessZIP    string `json:"businessZIP" setting:"section:business,label:ZIP Code,type:text,id:business-zip,help:ZIP or postal code for your business"`

	// Tax information
	BusinessTaxID  string  `json:"businessTaxID" setting:"section:tax,label:Business Tax ID,type:text,id:business-tax-id,help:Business Tax ID (EIN)"`
	SalesTaxNumber string  `json:"salesTaxNumber" setting:"section:tax,label:Sales Tax Number,type:text,id:sales-tax-number,help:Sales tax registration number"`
	VATNumber      string  `json:"vatNumber" setting:"section:tax,label:VAT Number,type:text,id:vat-number,help:VAT registration number (if applicable)"`
	DefaultTaxRate float64 `json:"defaultTaxRate" setting:"section:tax,label:Default Tax Rate,type:number,id:default-tax-rate,help:Default tax rate as percentage (e.g. 8.25),step:0.0001,min:0,max:100,format:percentage"`

	// Website information
	WebsiteName string `json:"websiteName" setting:"section:system,label:Website Name,type:text,id:website-name,help:Name displayed in the browser title and headers"`

	// Customer default location (hidden from settings UI - used internally)
	DefaultCity  string `json:"defaultCity" setting:"-"`
	DefaultState string `json:"defaultState" setting:"-"`

	// Tax configuration (complex types hidden from simple settings UI)
	TaxCategories []TaxCategory `json:"taxCategories" setting:"-"`

	// System configuration
	Port            string `json:"port" setting:"section:system,label:Port,type:text,id:port,help:Port number for the web server"`
	ServerAddress   string `json:"serverAddress" setting:"section:system,label:Server Address,type:text,id:server-address,help:Address to bind the server to (e.g. 127.0.0.1 or 0.0.0.0)"`
	DataDir         string `json:"dataDir" setting:"section:system,label:Data Directory,type:text,id:data-dir,help:Directory where application data is stored"`
	TransactionsDir string `json:"transactionsDir" setting:"section:system,label:Transactions Dir,type:text,id:transactions-dir,help:Directory where transaction records are stored"`

	// AWS SNS Configuration (for SMS receipts)
	AWSAccessKeyID     string `json:"awsAccessKeyId" setting:"section:sms,label:AWS Access Key,type:text,id:aws-access-key,help:AWS Access Key ID for SMS functionality"`
	AWSSecretAccessKey string `json:"awsSecretAccessKey" setting:"section:sms,label:AWS Secret Access Key,type:password,id:aws-secret-key,help:AWS Secret Access Key for SMS functionality"`
	AWSRegion          string `json:"awsRegion" setting:"section:sms,label:AWS Region,type:text,id:aws-region,help:AWS Region (e.g. us-east-1)"`

	// Tipping Configuration
	TippingEnabled           bool    `json:"tippingEnabled" setting:"section:tipping,label:Tipping Enabled,type:checkbox,id:tipping-enabled,help:Enable or disable tipping functionality"`
	TippingMinAmount         float64 `json:"tippingMinAmount" setting:"section:tipping,label:Min Amount,type:number,id:tipping-min-amount,help:Minimum transaction amount to show tipping (in dollars),step:0.01,min:0"`
	TippingMaxAmount         float64 `json:"tippingMaxAmount" setting:"section:tipping,label:Max Amount,type:number,id:tipping-max-amount,help:Maximum transaction amount to show tipping (0 = no limit),step:0.01,min:0"`
	TippingAllowCustomAmount bool    `json:"tippingAllowCustomAmount" setting:"section:tipping,label:Allow Custom Amounts,type:checkbox,id:tipping-allow-custom,help:Allow customers to enter custom tip amounts"`

	// Complex tipping fields (hidden from simple settings UI)
	TippingLocationOverrides     map[string]bool `json:"tippingLocationOverrides" setting:"-"`     // Per-location tipping overrides (locationID -> enabled)
	TippingPresetPercentages     []int           `json:"tippingPresetPercentages" setting:"-"`     // Preset tip percentages (e.g., [15, 18, 20, 25])
	TippingServiceCategoriesOnly []string        `json:"tippingServiceCategoriesOnly" setting:"-"` // Only show tipping for specific service categories (empty = all)
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
