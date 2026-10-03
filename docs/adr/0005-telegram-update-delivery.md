# Receive Telegram updates through environment-specific adapters

Production receives Telegram updates through an authenticated HTTPS webhook, while local development uses long polling with separate development bots. Both adapters feed one shared runtime; this fits Piko's hosted execution model without requiring a public development endpoint, at the cost of maintaining two delivery adapters. Production requires a configured public HTTPS URL.

Credential verification does not change Telegram delivery. Activation requires an explicit step identifying Piko as the bot's operator, surfaces detected webhook conflicts, and preserves pending updates; each Telegram bot can be connected to only one Piko account. An empty webhook URL does not establish that another polling service is absent.

Telegram webhook deliveries use Telegram's secret-token header for machine authentication. This is a scoped exception to the repository's browser-form CSRF rule: browser-initiated state changes still require POST and CSRF validation, while the Telegram webhook is a separate authenticated ingress.
