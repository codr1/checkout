# POS (Point of Sale) System

A simple, modern Point of Sale system built with Go and HTMX, integrated with Stripe for payment processing.

## System Overview

This application provides a straightforward Point of Sale system with the following features:

- Product/service catalog management
- Shopping cart functionality
- Multiple payment options:
  - Terminal payments
  - Manual card entry
  - QR code payments
- Transaction recording in QuickBooks-friendly CSV format
- Minimal JavaScript - uses HTMX for a responsive experience

## Prerequisites

- Go 1.18 or higher
- Stripe account with API keys
- Internet connection for Stripe API communication

## Installation

1. Clone the repository
   ```bash
   git clone <repository-url>
   cd checkout
   ```

2. Install dependencies
   ```bash
   go mod download
   ```

3. Generate templ files (if you've modified .templ files)
   ```bash
   go install github.com/a-h/templ/cmd/templ@latest
   templ generate
   ```

## Configuration

### Environment Variables

The application uses the following environment variables:

- `STRIPE_SECRET_KEY`: Your Stripe secret key (takes precedence over the config file)
- `STRIPE_PUBLIC_KEY`: Your Stripe publishable key (takes precedence over the config file)
- `STRIPE_WEBHOOK_SECRET`: Your Stripe webhook signing secret (takes precedence over the config file)

### Initial Setup

1. When you start the application for the first time, it will prompt you to create a configuration file.
2. You'll need to provide:
   - Stripe Secret Key
   - Business information (name, address, tax IDs)
   - Website information
   - Default customer location
   - Tax configuration (default tax rate)
   - Tipping configuration (global settings)
   - System configuration (port, data directories)

The configuration will be stored in `./data/config.json`. You can edit this file directly if needed.

## Running the Application

1. Start the server
   ```bash
   go run main.go
   ```

2. The application automatically chooses HTTP or HTTPS based on your configuration:

   **For Local Development/Testing:**
   - If `websiteName` is empty or set to "localhost" in your config
   - Runs on **HTTPS** with self-signed certificate at `https://localhost:3000`
   - You'll need to accept the browser security warning once
   - Required for Stripe.js to work properly in manual card entry

   **For Production with Cloudflare:**
   - If `websiteName` is set to your domain (e.g., "mystore.example.com")
   - Runs on **HTTP** at `http://localhost:3000`
   - Expected to be accessed via cloudflared tunnel or reverse proxy
   - Cloudflare handles SSL termination

3. Login with the password you configured during setup

### HTTPS Certificate Details

When running in HTTPS mode (local development), the application:
- Automatically generates a self-signed certificate valid for 1 year
- Certificate includes both `localhost` and `127.0.0.1`
- Certificate is regenerated on each startup (no persistent storage)
- Enables proper Stripe.js functionality for manual card payments

### Server Configuration

- **Port**: The port the server listens on (default: 3000)
- **Server Address**: The address to bind the server to (default: 0.0.0.0)
  - Use `0.0.0.0` to allow connections from any interface
  - Use `127.0.0.1` to only allow local connections
- **Data Directory**: Where to store application data (default: ./data)
- **Transactions Directory**: Where to store transaction files (default: ./data/transactions)
- **Website Name**: Domain name for HTTPS support (optional)

## Settings Management

The application provides a web-based settings interface accessible from the POS system:

- **Access**: Click the actions menu (⋮) in the top-right corner of the POS interface and select "Settings"
- **Auto-Save**: All setting changes are automatically saved when you modify any field - no save button required
- **Search**: Use the search bar to quickly find specific settings across all categories
- **Categories**: Settings are organized into sections (Stripe, Business, Tax, System, Tipping, SMS)

Settings can also be manually edited in the `./data/config.json` file when the application is stopped.

## Directory Structure

- `/data`: Contains configuration and data files
  - `/data/config.json`: Application configuration including tax rates
  - `/data/services.json`: Product catalog with optional category assignments
  - `/data/transactions`: Contains daily transaction CSV files
- `/templates`: HTMX templates for the UI
- `/static`: Static assets like CSS
- `/config`: Configuration handling code

## Tax Configuration

The system uses a simple local tax calculation system that's cost-effective and easy to manage:

### Default Tax Rate
- Set during initial configuration setup
- Applied to all products/services unless they have a specific category tax rate
- Stored as a decimal value (e.g., 0.0625 for 6.25%)

### Tax Categories (Optional)
- Create product categories with specific tax rates
- Assign categories to products/services
- Products without categories use the default tax rate
- Categories can be managed through the configuration file

### Tax Calculation
- Tax is calculated locally without external API calls
- Each item in the cart uses either its category tax rate or the default rate
- Total tax is the sum of individual item taxes

## Tipping Configuration

The system supports configurable tipping for Stripe Terminal payments:

- **Global Setting**: Enable/disable tipping system-wide (defaults to disabled)
- **Location Overrides**: Override global setting for specific terminal locations
- **Amount Thresholds**: Set minimum/maximum transaction amounts for tipping eligibility
- **Service Restrictions**: Optionally restrict tipping to specific service categories

Tipping settings are configured during initial setup and can be managed through the configuration file.

## Stripe Integration

The system requires Stripe keys to function properly. You'll need:

1. **Secret Key** (server-side API calls)
2. **Publishable Key** (client-side integrations)
3. **Webhook Secret** (for verifying webhook events)

### Setting Up Stripe Keys

1. Create a Stripe account at [stripe.com](https://stripe.com) if you don't have one
2. Go to the Stripe Dashboard → Developers → API Keys
3. Copy your Secret Key and Publishable Key
4. Set up keys either:
   - As environment variables:
     ```bash
     export STRIPE_SECRET_KEY=sk_test_...
     export STRIPE_PUBLIC_KEY=pk_test_...
     ```
   - Or during the initial configuration setup

### Setting Up Webhooks

1. Go to Stripe Dashboard → Developers → Webhooks
TODO:  THIS SHOULD ALL BE AUTOMATED!!!  And you cloudflared.  IT is free and gives you free https
2. Add a new endpoint with URL: `https://your-domain.com/stripe-webhook`
   - For local testing, use a service like [ngrok](https://ngrok.com)
3. Select these events at minimum:
   - `payment_intent.created`
   - `payment_intent.succeeded`
   - `payment_intent.payment_failed`
   - `checkout.session.completed`
4. Copy the Signing Secret and set it:
   - As an environment variable: `export STRIPE_WEBHOOK_SECRET=whsec_...`
   - Or during the initial configuration setup

**Important**: The Stripe keys must be set correctly before services are loaded, as the system creates Stripe products and prices for each service in your catalog.

### Stripe Terminal Setup

For using physical Stripe Terminal readers for in-person payments:

**1. Register Your Terminal Reader(s) in Stripe:**
   - If you have a new Stripe Terminal device, it will display a registration code (e.g., "word-word-word").
   - Go to your Stripe Dashboard -> Terminal -> Readers.
   - Click "Register Reader" and enter the code from your device. Give your reader a label (e.g., "Front Counter").
   - The reader will then be associated with your Stripe account and receive a Reader ID (e.g., `tmr_xxxxxx`).
   - **TODO:** Implement functionality within this application to guide users through the reader registration process or directly register readers via the Stripe API if possible.

**2. Set Up Stripe Terminal Location(s) (Sites):**
   - Stripe Terminal readers operate within "Locations." A Location typically represents a physical store or a distinct point of sale area.
   - In your Stripe Dashboard -> Terminal -> Locations, create at least one Location (e.g., "Main Store"). Each Location will have an ID (e.g., `tml_yyyyyy`).
   - Ensure your registered readers are assigned to the correct Location in the Stripe Dashboard.

**3. Configure the Application with a Terminal Location ID:**
   - This POS application needs to know which Stripe Terminal Location it is associated with.
   - Open your configuration file: `./data/config.json`.
   - Add or update the `stripeTerminalLocationID` field with the ID of the Location you want this POS instance to use:
     ```json
     {
       // ... other config ...
       "stripeTerminalLocationID": "tml_your_location_id_here"
     }

For testing, use Stripe's test card numbers:
- `4242 4242 4242 4242` - Successful payment
- `4000 0000 0000 9995` - Requires authentication
- `4000 0000 0000 0341` - Payment fails

## Receipt System

The system provides automatic email receipts via Stripe and optional SMS receipts via AWS SNS.

### Email Receipts
- Automatically sent by Stripe when customer email is provided
- No configuration required

### SMS Receipts (Optional)
To enable SMS receipts, configure AWS SNS credentials:

1. [Create AWS Account](https://aws.amazon.com/getting-started/)
2. [Create IAM User with SNS permissions](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_users_create.html)
3. Add credentials to your configuration during setup or in `./data/config.json`:
   ```json
   {
     "awsAccessKeyId": "your_access_key",
     "awsSecretAccessKey": "your_secret_key", 
     "awsRegion": "us-east-1"
   }
   ```

When configured, customers can choose email, SMS, or both after completing payment.

## Transaction Recording

All transactions are saved in CSV files compatible with QuickBooks:
- One file per day (format: YYYY-MM-DD.csv)
- Files are stored in the transactions directory specified in your config
- Each transaction includes date, time, ID, item details, payment method, etc.

## Troubleshooting

### Stripe Authentication Errors

If you see errors like:
```
[ERROR] Request error from Stripe (status 401): {"status":401,"message":"You did not provide an API key..."}
```

Solutions:
1. Ensure your Stripe secret key is correct
2. Check that the environment variable is set correctly
3. Verify the key in your config file if not using an environment variable

### Data Directory Issues

If you encounter errors related to data files:
1. Ensure the application has write permissions to the data directory
2. Check that the configured paths exist or can be created

### Tax Configuration Issues

If you need to update tax rates:
1. Stop the application
2. Edit `./data/config.json` directly
3. Update the `default_tax_rate` field (as decimal, e.g., 0.0625 for 6.25%)
4. Add or modify tax categories in the `tax_categories` array
5. Restart the application

Example tax category in config.json:
```json
{
  "tax_categories": [
    {
      "id": "food",
      "name": "Food Items", 
      "tax_rate": 0.0
    },
    {
      "id": "services",
      "name": "Professional Services",
      "tax_rate": 0.0625
    }
  ]
}
```

## Communication Strategy

The backend automatically selects the optimal communication method with Stripe based on your domain configuration:

### Polling Mode (Development)
- **When**: No domain configured or domain is "localhost"
- **Protocol**: HTTPS with self-signed certificate
- **Method**: Frontend polls backend every 2s, backend polls Stripe API
- **Benefits**: Works without external webhooks, simple setup

### Webhook Mode (Production)
- **When**: Domain is configured (not localhost/empty)
- **Protocol**: HTTP (expects SSL termination by proxy)
- **Method**: Frontend polls backend every 2s, backend receives Stripe webhooks
- **Benefits**: More efficient, real-time updates, reduced API calls

### Automatic Webhook Registration
When using webhook mode, the application automatically:
1. Registers a webhook endpoint with Stripe on startup
2. Configures the necessary payment events
3. Falls back to polling if registration fails

### Events Handled
- `payment_intent.*` (created, succeeded, failed, canceled, requires_action)
- `payment_link.*` (completed, updated)
- `terminal.reader.action_*` (succeeded, failed)
- `charge.*` (succeeded, failed - backup confirmation)

### State Caching
- Webhook events cached for 120 seconds (matches UI timeout)
- Automatic cleanup of expired states every 30 seconds
- Thread-safe with RWMutex protection
- Webhook signature verification for security

## Security Considerations

For production use:
2. Consider implementing proper user authentication
3. Use HTTPS by configuring a reverse proxy like Nginx (Right now we are using cloudflared which may be ok???)
4. Secure your Stripe API keys
5. Regularly backup your transaction data
6. Add a real database.  Start with SQLite potentially with Turso as a backup.
7. At some point of time make the config files editable
8. At some point add Tailwind


