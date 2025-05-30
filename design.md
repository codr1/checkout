# This is a POS application
## Stack 
Go and HTMX.  As little JS as possible to get the job done.  This means idiomatic HTMX to the MAX.  As little JS Slop as possible.
We should use templ templates as much as possible.  Minimize inline HTML/JS like it is your job.  Because it is


## We are using Stripe

## We are storing all transactions in a QuickBooks friendly CSV file.  There is one file per day.  Files are append only

## We have a config file and we also store our secrets in ENV variables for now

## We should store our product catalog in a file called ./data/products.json

## If we come up with more hard requirements we will add them here.

## If you are an AI, and you are making mods, and you have failed (Especially in HTMX related areas) use your web search to find how it's supposed to be done.  Don't keep trying to do the same thing over and over again expecting a different result.

## Payment Success URL Considerations

When handling payment completion, we have two options:

1. **Custom Success URL (Current Implementation)**
   - Advantages:
     - Full integration with our application
     - Consistent branding and UX
     - Ability to clear cart and update UI state
     - Custom receipt options
   - Disadvantages:
     - Requires maintaining our own success page
     - Must handle all edge cases

2. **Default Stripe Hosted Pages**
   - Advantages:
     - Simpler implementation (omit `AfterCompletion` parameter)
     - Maintained by Stripe
     - No need to handle success UI
   - Disadvantages:
     - No application integration (cart won't clear automatically)
     - Stripe branding instead of custom branding
     - No custom receipt options
     - Relies solely on webhooks for backend processing

**Decision**: We're using a custom success URL to maintain consistent UX and provide receipt options.

## Stripe Tax Implementation

To replace the hardcoded tax rate with Stripe Tax:

1. **Setup in Stripe Dashboard**
   - Enable Stripe Tax in the dashboard
   - Configure tax registration information
   - Set up tax calculation rules

2. **Code Implementation**
   - Use tax calculation APIs when creating PaymentIntents/PaymentLinks
   - Include customer location data for proper tax calculation
   - Utilize tax rates returned by Stripe instead of hardcoded values

## TODOs for Production-Ready Stripe Integration

1. **Webhook Enhancement**
   - Implement proper error handling and retries for webhook processing
   - Add persistent storage for webhook events to prevent duplicate processing
   - Implement background worker for cleanup of abandoned payment intents
   - Add monitoring and alerting for payment system health

2. **Security Enhancements**
   - Implement HTTPS for all payment-related traffic
   - Add IP-based rate limiting for the webhook endpoint
   - Implement idempotency keys for all Stripe API calls
   - Regularly rotate Stripe API keys and webhook secrets

3. **User Experience**
   - Improve error handling and user feedback for failed payments
   - Add automatic receipt generation and delivery
   - Implement payment confirmation emails/SMS

4. **Infrastructure**
   - Set up a separate webhook URL for test vs. production mode
   - Implement proper logging and monitoring for payment flows
   - Add metrics collection for payment success/failure rates

5. **Production Requirements**
   - Replace hardcoded localhost URL with configurable domain
   - Implement proper authentication instead of hardcoded PIN
   - Replace `http` with `https` for all payment-related URLs
   - Ensure proper error handling for production traffic
