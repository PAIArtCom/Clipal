# Config Reference

## Config File Location

Default directory:

- macOS / Linux: `~/.clipal/`
- Windows: `%USERPROFILE%\\.clipal\\`

Default files:

```text
config.yaml
claude.yaml
openai.yaml
gemini.yaml
```

`config.yaml` is global config. The other three files define separate client groups.

Matching templates:

- [../../examples/config.yaml](../../examples/config.yaml)
- [../../examples/claude.yaml](../../examples/claude.yaml)
- [../../examples/openai.yaml](../../examples/openai.yaml)
- [../../examples/gemini.yaml](../../examples/gemini.yaml)

## Minimal Example

`config.yaml`:

```yaml
listen_addr: 127.0.0.1
port: 3333
log_level: info
reactivate_after: 1h
```

`openai.yaml`:

```yaml
mode: auto
pinned_provider: ""

providers:
  - name: openai-compatible
    base_url: https://api.openai.com
    api_key: sk-xxx
    priority: 1
    enabled: true
```

OAuth-backed providers are also supported in `openai.yaml`, `claude.yaml`, and `gemini.yaml`. The recommended path is to create them from the Web UI authorization flow instead of typing them by hand.

## Global Config `config.yaml`

| Field | Type | Default | Notes |
|-------|------|---------|-------|
| `listen_addr` | string | `127.0.0.1` | Listen address |
| `port` | int | `3333` | Local proxy port |
| `log_level` | string | `info` | `debug` / `info` / `warn` / `error` |
| `reactivate_after` | duration | `1h` | Auto-reactivation delay for temporarily deactivated providers; set `0` to disable temporary deactivation for auth, billing, and quota failures |
| `upstream_idle_timeout` | duration | `3m` | Abort the current upstream attempt if no response body bytes arrive for too long |
| `response_header_timeout` | duration | `2m` | Timeout while waiting for upstream response headers |
| `upstream_proxy_mode` | string | `environment` | Default upstream proxy mode for providers that use `proxy_mode: default`; `environment` / `direct` / `custom` |
| `upstream_proxy_url` | string | empty | Required when `upstream_proxy_mode: custom`; supports `http://`, `https://`, `socks5://`, and `socks5h://` proxy URLs |
| `max_request_body_bytes` | int | `33554432` | Request body size limit, default 32 MiB |
| `log_dir` | string | `<config-dir>/logs` | Log directory |
| `log_retention_days` | int | `7` | Log retention days; `0` keeps logs forever; default is 7 days |
| `log_stdout` | bool | `true` | Also log to stdout; long-running background setups usually prefer `false` |

### `notifications`

```yaml
notifications:
  enabled: false
  min_level: error
  provider_switch: true
```

| Field | Type | Default | Notes |
|-------|------|---------|-------|
| `enabled` | bool | `false` | Enable desktop notifications |
| `min_level` | string | `error` | `debug` / `info` / `warn` / `error` |
| `provider_switch` | bool | `true` | Send notifications on provider switches |

### `circuit_breaker`

```yaml
circuit_breaker:
  failure_threshold: 4
  success_threshold: 2
  open_timeout: 60s
  half_open_max_inflight: 1
```

| Field | Type | Default | Notes |
|-------|------|---------|-------|
| `failure_threshold` | int | `4` | Open the circuit after this many consecutive failures; `0` disables it |
| `success_threshold` | int | `2` | Consecutive half-open successes needed to close the circuit |
| `open_timeout` | duration | `60s` | How long the circuit stays open before probing again |
| `half_open_max_inflight` | int | `1` | Max concurrent half-open probes |

### `routing`

```yaml
routing:
  sticky_sessions:
    enabled: true
    explicit_ttl: 30m
    cache_hint_ttl: 10m
    dynamic_feature_ttl: 10m
    dynamic_feature_capacity: 1024
    response_lookup_ttl: 15m
  busy_backpressure:
    enabled: true
    retry_delays:
      - 5s
      - 10s
    probe_max_inflight: 1
    short_retry_after_max: 3s
    max_inline_wait: 8s
```

`sticky_sessions` controls how long Clipal keeps affinity hints in memory:

- `explicit_ttl`: durable linkage keys such as OpenAI `previous_response_id`
- `cache_hint_ttl`: cache-oriented hints such as `prompt_cache_key`
- `dynamic_feature_ttl`: short-lived heuristic bindings derived from human-message history
- `dynamic_feature_capacity`: max in-memory dynamic/cache-level entries before least-recently-used eviction
- `response_lookup_ttl`: response-id lookup cache lifetime

`busy_backpressure` controls how Clipal reacts to concurrency-limit `429` responses:

- `retry_delays`: inline wait/backoff schedule before overflow
- `probe_max_inflight`: max concurrent recovery probes allowed per busy provider
- `short_retry_after_max`: only very short retry hints are eligible for busy handling
- `max_inline_wait`: hard cap for how long Clipal holds one request before overflowing to another provider

## Client Configs

All three client files share the same structure:

- `claude.yaml`
- `openai.yaml`
- `gemini.yaml`

Example:

```yaml
mode: auto
pinned_provider: ""

providers:
  - name: primary
    base_url: https://api.example.com
    api_key: sk-xxx
    priority: 1
    enabled: true

  - name: backup
    base_url: https://backup.example.com
    api_keys:
      - sk-a
      - sk-b
    priority: 2
    enabled: true
```

### Top-Level Fields

| Field | Type | Default | Notes |
|-------|------|---------|-------|
| `mode` | string | `auto` | `auto` or `manual` |
| `pinned_provider` | string | empty | Provider name to lock to when `mode: manual` |
| `providers` | array | none | Provider list |

### `providers[]`

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `name` | string | yes | Provider name |
| `auth_type` | string | no | `api_key` by default; set `oauth` for OAuth-backed providers |
| `base_url` | string | API-key only | Upstream API base URL; not allowed for OAuth providers |
| `api_key` | string | API-key only | Single API key |
| `api_keys` | array | API-key only | Multiple API keys, used in order |
| `oauth_provider` | string | OAuth only | Supported combinations are `codex` in `openai.yaml`, `claude` in `claude.yaml`, and `gemini` in `gemini.yaml` |
| `oauth_ref` | string | OAuth only | Reference to the locally stored OAuth credential |
| `proxy_mode` | string | no | Upstream proxy mode for this provider; `default` follows the global default |
| `proxy_url` | string | no | Required when `proxy_mode: custom`; supports `http://`, `https://`, `socks5://`, and `socks5h://` proxy URLs |
| `priority` | int | no | Lower number = higher priority; omitted or `0` is treated as `1` |
| `enabled` | bool | no | Defaults to `true` |
| `model` | string | no | Force this provider to use a specific upstream model name for supported OpenAI and Claude requests |
| `reasoning_effort` | string | no | OpenAI only. For `/v1/responses*`, Clipal writes `reasoning.effort`; for chat/completions it only replaces an existing `reasoning_effort` field |
| `thinking_budget_tokens` | int | no | Claude only. Clipal writes `thinking = {type: "enabled", budget_tokens: ...}` on supported requests |

### OAuth Providers

OAuth providers stay in the same `providers[]` list as API-key providers. They participate in the same ordering, pinning, enable/disable, and failover behavior.

- Supported today: `openai.yaml` with `auth_type: oauth` and `oauth_provider: codex`; `claude.yaml` with `auth_type: oauth` and `oauth_provider: claude`; `gemini.yaml` with `auth_type: oauth` and `oauth_provider: gemini`
- Current protocol scope: Codex OAuth supports OpenAI `/v1/responses*`; Claude OAuth supports `/v1/messages` and `/v1/messages/count_tokens`; Gemini OAuth supports `generateContent`, `streamGenerateContent`, and `countTokens`
- Claude OAuth requests use a lightweight Agent SDK-compatible envelope by default, with current transport headers and billing fingerprints generated by Clipal.
- Codex OAuth Responses requests are normalized into the lightweight Agent SDK-compatible shape, with required transport fields handled by Clipal.
- Defaults are non-destructive when the target model supports the field. If the client already sends `tools`, Claude `thinking` / `context_management` / `output_config`, or Codex `reasoning` / `tool_choice` / `parallel_tool_calls`, Clipal preserves those fields instead of overwriting them; unsupported Claude models may still drop incompatible thinking/output controls.
- Do not set `base_url`, `api_key`, or `api_keys` on an OAuth provider
- Create OAuth providers from the Web UI by choosing `Add Provider -> OAuth -> Codex`, `Claude`, or `Gemini` on the matching client page
- The Add Provider dialog can also import existing OAuth credential files: Codex CLI `auth.json` (`~/.codex/auth.json`), CLIProxyAPI single-account OAuth JSON files, and sub2api export JSON bundles. Imported accounts are filtered by the selected OAuth service.
- The backend generates a stable internal provider `name` from the authorized account identity; the UI shows the account email as the display label
- OAuth credentials are stored outside YAML under `~/.clipal/oauth/<provider>/<email>--<oauth_ref>.json`
- Clipal refreshes access tokens automatically shortly before expiry when a `refresh_token` is available, and retries once after an upstream `401` by forcing a refresh
- Existing OAuth providers can be reordered, pinned, enabled, or disabled like normal providers, but credential edits go through re-authorization instead of the generic edit form

## Practical Defaults

- Use `api_key` when you only have one key
- Use `api_keys` when you want retries across multiple keys within the same provider
- Use global `upstream_proxy_mode` / `upstream_proxy_url` to define the default proxy for providers that use `proxy_mode: default`
- Use provider `proxy_mode: direct` to bypass both the global default proxy and environment proxy settings
- Use `model` when different upstream providers expose the same family under different model IDs
- Use `reasoning_effort` and `thinking_budget_tokens` only when you want Clipal to override the client-sent defaults for that provider
- For long-running background setups, this is a good default:

```yaml
log_stdout: false
log_retention_days: 7 # 0 keeps logs forever
```

- For safety, keep this unless you explicitly need network exposure:

```yaml
listen_addr: 127.0.0.1
```

## Related Docs

- [Routing and Failover](routing-and-failover.md)
- [Web UI Guide](web-ui.md)
- [Services, Status, and Updates](services.md)
