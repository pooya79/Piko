# Keep Builder observability optional

The Builder uses Google Genkit for Go with OpenRouter and the default model `openai/gpt-6-luna`; model selection is configurable through environment variables. Missing OpenRouter credentials leave the app and manual Bot management available while Builder chat is unavailable.

Langfuse export is optional and requires its base URL, public key, and secret key. Incomplete configuration produces an operator warning and disables export, and monitoring outages do not fail Builder requests. We choose this independence over making the monitoring service a condition of building Bots, accepting that external traces can be incomplete while local usage and cost accounting remains available.

Export model and tool traces, latency, errors, usage, and supported cost data. Prompt/content capture requires a separate explicit configuration opt-in, and credentials must be excluded. Removing a local Builder chat does not remove its previously exported Langfuse history; operators retain responsibility for Langfuse retention. Go tracing uses native OpenTelemetry, following [Langfuse's integration guidance](https://langfuse.com/integrations/native/opentelemetry).
