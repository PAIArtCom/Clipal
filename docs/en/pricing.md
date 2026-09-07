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

The calculator supports standard, batch, priority, and flex token prices; cached input and cache creation; audio input and reasoning output rates; 200K and 272K context tiers; and per-query search charges when the response reports a query count. A missing standalone specialized price falls back only to the corresponding base rate defined for the same exact model, never to another model. If a request combines a service tier and a long-context tier but the catalog does not define the required combination price, Clipal leaves that request unpriced instead of mixing rates from different tiers.

Anthropic reports Claude Opus 4.6 fast mode through `usage.speed=fast` rather than a separately priced model ID. For `claude-opus-4-6` and `claude-opus-4-6-20260205`, Clipal multiplies every catalog token rate, including cache rates, by 6. Per-query search charges are not multiplied. This protocol-specific multiplier is maintained in the Claude response adapter because the catalog supplies only the base model rates.

The current catalog version and source commit are returned by `GET /api/status` in `pricing_catalog`.

## Updating the Vendored Catalog

Maintainers can validate and vendor the latest upstream catalog with:

```bash
MODEL_SPECS_REF=<reviewed-40-character-commit> make update-model-specs
```

The commit SHA is mandatory so the reviewed source is immutable. The script reads only the committed `dist/catalog.json`, `dist/catalog.sha`, and `LICENSE` files without executing upstream code. It validates required fields, recomputes the version from `models` and `clients`, and updates the embedded catalog, full-file integrity hash, license, and source commit together.
