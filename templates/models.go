package templates

// Service represents a service item that can be sold
type Service struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	PriceID     string  `json:"priceID,omitempty"` // Stripe Price ID
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
}

// AppConfig represents the application configuration
type AppConfig struct {
	// Stripe configuration
	StripeSecretKey string `json:"stripeSecretKey"`
	
	// Business information
	BusinessName    string `json:"businessName"`
	BusinessStreet  string `json:"businessStreet"`
	BusinessCity    string `json:"businessCity"`
	BusinessState   string `json:"businessState"`
	BusinessZIP     string `json:"businessZIP"`
	
	// Tax information
	BusinessTaxID   string `json:"businessTaxID"`
	SalesTaxNumber  string `json:"salesTaxNumber"`
	VATNumber       string `json:"vatNumber"`
	
	// Website information
	WebsiteName     string `json:"websiteName"`
	
	// Customer default location
	DefaultCity     string `json:"defaultCity"`
	DefaultState    string `json:"defaultState"`
	
	// System configuration
	Port            string `json:"port"`
	DataDir         string `json:"dataDir"`
	TransactionsDir string `json:"transactionsDir"`
}

