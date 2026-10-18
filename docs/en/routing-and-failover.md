# Routing and Failover

## Route Prefixes

Clipal standardizes client ingress on these local paths:

| Path | Use |
|------|-----|
| `/` | Web UI |
| `/health` | Health check |
| `/clipal/*` | Preferred unified ingress for Claude-, OpenAI-, and Gemini-style requests |
| `/claudecode/*` | Legacy Claude-compatible alias |
| `/codex/*` | Legacy OpenAI-compatible alias |
| `/google/*` | Google channel ingress for Gemini-compatible requests |
| `/gemini/*` | Legacy Gemini-compatible alias |

Clipal detects the request family from the upstream path under `/clipal/*`. The compatibility aliases remain available for older client configs. Each backend client group still keeps its own provider list and runtime state.

For ambiguous paths under `/clipal`, Clipal uses explicit protocol rules instead of implicit detector order. In particular, `/clipal/v1/files` is treated as OpenAI-compatible, while Gemini-specific file flows stay on Gemini-style paths such as `/clipal/v1beta/files` and `/clipal/upload/v1beta/files`.

## Provider Selection Order

For each client group, Clipal selects upstreams with these rules:

1. Filter out providers with `enabled: false`
2. Sort by `priority` ascending
3. Keep file order for equal priorities
4. Prefer the most recently successful provider to avoid unnecessary hopping

## `mode: auto`

This is the default.

Behavior:

- Keep using the current provider while it succeeds
- Move to the next available provider on failure
- If a provider has multiple keys, retry the next available key in the same provider first

Best for:

- primary / backup provider setups
- automatic local failover across multiple upstreams

## `mode: manual`

Behavior:

- Only sends requests to `pinned_provider`
- Never fails over to another provider
- Does not silently switch to another key within the same provider
- Returns the pinned upstream response directly

Best for:

- debugging
- forcing a stable provider choice for repeatable behavior

## Temporary Deactivation and Cooldown

Clipal classifies upstream failures and may temporarily skip a provider:

- `401` / `403`: auth or permission failures, usually deactivate
- `402`: quota or billing failures, usually deactivate
- `429`: handled more carefully
  - quota/auth-like cases: deactivate
  - rate-limit or overload cases: try the next upstream, but not always as a long deactivation

Temporarily skipped providers come back after `reactivate_after`.

## Multi-Key Behavior

A provider can use either:

- `api_key`
- or `api_keys`

When `api_keys` is configured:

- Clipal normalizes, deduplicates, and preserves order
- In auto mode, it retries the next available key in the same provider first
- It only moves to the next provider after the current provider runs out of usable keys

## Circuit Breaker

If `circuit_breaker` is enabled:

- repeated failures can open the provider circuit
- after `open_timeout`, the provider moves to `half_open`
- enough successful probes closes the circuit again

This helps avoid repeatedly hitting clearly unhealthy upstreams.

## When All Providers Are Unavailable

If no provider is currently usable for a client group:

- Clipal returns an error
- some retryable cases also include `Retry-After`

## Hot Reload

Config changes are reloaded automatically in normal operation.

What this means:

- provider lists are rebuilt from the new config
- temporary runtime state from the previous config is replaced by the reloaded configuration

## Related Docs

- [Config Reference](config-reference.md)
- [Web UI Guide](web-ui.md)
- [Troubleshooting](troubleshooting.md)
