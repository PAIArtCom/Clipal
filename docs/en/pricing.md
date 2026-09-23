# Usage Cost Calculation

Clipal records usage costs in integer micro-US dollars (`1 USD = 1,000,000 micros`). The stored value is the cost calculated when the request completes; updating the price catalog does not reprice historical usage. Stored aggregates retain whether prices came from the upstream response or the embedded catalog, plus every catalog version represented in the aggregate. Costs created before attribution was introduced are marked `legacy_unknown`.

## Cost Source Precedence

For every completed generation request, Clipal uses the first available source:

1. A cost returned directly by the upstream response.
2. A cost calculated from token usage and the embedded `PAIArtCom/model-specs` catalog.
3. No cost value when the response has neither a usable direct cost nor a resolvable priced model.

Direct upstream cost fields include micro-USD fields, USD numeric fields, and `{amount, currency}` cost objects. Non-USD objects are not converted. A single request cost above 1,000,000 USD is treated as invalid and is not recorded, preventing a malformed upstream response from saturating persistent aggregates.

Cost tracking is auxiliary to proxying. Malformed, negative, incomplete, or internally inconsistent usage components cause only that request's cost calculation to be skipped; they never turn an otherwise successful upstream response into a proxy failure. Invalid stored cost attribution is downgraded to `legacy_unknown`, while a negative cost is ignored.

## Catalog Resolution

The catalog is embedded in the Clipal binary, so request handling never downloads mutable pricing data. Model matching is exact after removing protocol wrappers such as `models/`, `openai/`, `anthropic/`, or `gemini/`. Native Claude and Gemini requests must match their catalog platform. OpenAI-compatible requests may use any catalog platform; Clipal uses the provider base URL or provider name to prefer provider-specific entries such as `openrouter/deepseek/deepseek-chat` before the unqualified model entry. It does not inherit prices from a model-name prefix; for example, `gpt-5.4-pro` is priced from its own entry and an unknown `gpt-5.4-*` name remains unpriced.

The calculator supports standard, batch, priority, and flex token prices, including tier-specific cache rates; cached input and cache creation; audio input and reasoning output rates; 200K and 272K context tiers; and per-query search charges when the response reports a query count. A missing standalone specialized price falls back only to the corresponding base rate defined for the same exact model, never to another model. If a request combines a service tier and a long-context tier but the catalog does not define the required combination price, Clipal leaves that request unpriced instead of mixing rates from different tiers.

Anthropic reports fast mode through `usage.speed=fast` and the inference geography through `usage.inference_geo` rather than through separately priced model IDs. For fast mode, Clipal multiplies every catalog token rate, including cache rates, by the model's catalog `fast_mode_uplift_multiplier` (2 for Claude Opus 4.8, Opus 5, and Opus 5.5). Claude Opus 4.6 (`claude-opus-4-6` and `claude-opus-4-6-20260205`) has no catalog multiplier, so the calculator keeps a local 6x fallback for it; a catalog value always takes precedence. A model without either value keeps its base rates. When `usage.inference_geo` is `us`, token rates are also multiplied by the model's `regional_processing_uplift_multiplier_us` (1.1 for Claude 4.6 and later); both premiums stack. Per-query search charges are not multiplied by either premium.

The current catalog version and source commit are returned by `GET /api/status` in `pricing_catalog`.

## Updating the Vendored Catalog

Maintainers can validate and vendor the latest upstream catalog with:

```bash
MODEL_SPECS_REF=<reviewed-40-character-commit> make update-model-specs
```

The commit SHA is mandatory so the reviewed source is immutable. The script reads only the committed `dist/catalog.json`, `dist/catalog.sha`, and `LICENSE` files without executing upstream code. It validates required fields, recomputes the version from `models` and `clients`, and updates the embedded catalog, full-file integrity hash, license, and source commit together.
