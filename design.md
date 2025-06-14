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

## TODOs for Production-Ready Stripe Integration

1. **Webhook Enhancement**
   - Add persistent storage for webhook events to prevent duplicate processing
   - Add monitoring and alerting for payment system health

2. **Security Enhancements**
   - Add IP-based rate limiting for the webhook endpoint
   - Implement idempotency keys for all Stripe API calls
   - Regularly rotate Stripe API keys and webhook secrets

3. **Infrastructure**
   - Set up a separate webhook URL for test vs. production mode
   - Add metrics collection for payment success/failure rates
