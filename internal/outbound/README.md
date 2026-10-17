## Role

Outbound owns request-route DNS selection, DoH resolution and standard proxy connections used by OAuth requests and provider consistency diagnostics. It leaves stored account identity/region preferences, operating-system DNS, language, timezone and Desktop state unchanged.

## Owns

- Google and Cloudflare DNS wireformat HTTPS queries, bounded responses, account/route-local TTL and failure caches, and per-host concurrent lookup coalescing.
- Direct, HTTP/HTTPS CONNECT and SOCKS connections preserving original HTTP hosts, TLS SNI and certificate verification. All standard proxy routes retain hostnames for proxy-owned DNS/routing; Explicit Cloudflare/Google routes without a proxy use DoH-returned target IPs. Automatic routes try Cloudflare → Google DoH and resolved-IP connections first, then fall back to the OS hostname path if lookup, TCP or standard TLS establishment fails.
- DoH bootstrap, cancellation and fail-closed target DNS errors for explicit Cloudflare/Google policies. The public DoH service hostname uses system bootstrap DNS, with its fixed IP as a connection fallback; both verify the original TLS name. For explicit DoH policies, target hostnames remain exclusively inside DoH queries. Proxy business requests never require public DoH, including saved Cloudflare/Google choices.
- Explicit public DoH reference lookups through the selected proxy, retaining service domains. These optional diagnostics do not control target DNS or block business requests.
- Staggered IPv4/IPv6 connection races, bounded candidate attempts and cancellation/cleanup of unused connections on resolved-IP routes.
- Effective proxy-entry and resolver facts from unchanged provider/global/environment/system policy; no software controller or listener discovery.
- Read-only OS proxy selection: native CFNetwork on CGO-enabled macOS (static, SOCKS, exceptions and PAC), WinHTTP on Windows, and manual GNOME settings on Linux. Environment proxy variables take precedence per target scheme; ALL_PROXY is supported and NO_PROXY also bypasses system proxies. macOS non-CGO builds support static settings through scutil; PAC requires native support. GNOME PAC is reported unsupported rather than silently bypassed. Other Linux desktops use environment variables. OS settings are read for each request, not frozen at startup.

## Touchpoints

- `internal/proxy` uses `NetworkProxy` for inherited routing, while explicit direct/custom policies retain ownership. OAuth refresh, business calls and diagnostics use the same per-request selection.
- `internal/oauth` wraps account clients and supplies the existing Anthropic TLS adapter through `WithDialContext`.
- `internal/proxy` resolves provider proxy policy before selecting a reusable account client.
- `internal/web` uses the same routing layer for anonymous connection diagnostics and clearly labeled reference observations without passing OAuth credentials.

## Change Guide

- Verify environment/system precedence, NO_PROXY and OS exceptions, absent proxies, settings changes and fail-closed proxy errors. Cross-build Windows/Linux and macOS without CGO after platform changes.
- Run `go test -race ./internal/outbound ./internal/oauth ./internal/proxy ./internal/web` after changing connection, cache, lifecycle or environment behavior.
- Verify DoH hostname bootstrap succeeds when fixed service IPs are blocked, fixed-IP bootstrap survives hostname DNS/TLS failure or stalls, untrusted certificates fail, and request deadlines remain shared.
- Verify positive TTL/zero-TTL behavior, five-second failure expiry and recovery, earliest-response expiry, caller-owned address copies, bounded eviction, concurrent cold lookups, independent waiter cancellation and cancelled-leader retry. Caller cancellation or a short deadline must not create backoff; exhaustion of the full automatic DNS lookup budget may.
- Verify DoH is preferred when healthy and lookup/TCP/TLS failures or stalls fall back to the system hostname path, successful business requests are sent once, and explicit DNS policies remain strict.
- Verify IPv4 stalls do not block working IPv6, same-family retries share the request deadline, and unused connections close without cancelling a returned CONNECT tunnel.
- Keep original-domain TLS verification and proxy authentication intact. Explicit Cloudflare/Google policies never fall back to system target DNS; a failed proxy connection never falls back to a direct target connection.
- Extend the shared local TLS/tunnel test fixture for proxy variants rather than adding a second transport harness.
- Keep proxy DNS ownership explicit: no public DoH preflight gates proxy connections. Optional reference queries cannot verify the proxy's DNS backend, target IP or exit; matching entries cannot prove matching target exits.
- Honor explicit direct/custom policies, inherited global settings, environment/system proxies and NO_PROXY. Never decorate these policies with software discovery or automatic takeover.
- Distinguish target DNS from first-hop proxy hostname resolution. Proxy URLs with hostnames still need the system resolver to establish the first hop; diagnostics report this boundary.

## Decisions

- OAuth account DNS defaults to `auto`: evaluate provider/global/environment/system proxy policy and NO_PROXY for each target. A standard proxy owns hostname resolution. Without a proxy, first try Cloudflare → Google DoH and returned target IPs; if lookup or connection establishment fails, use the system hostname route. The DoH stage has a five-second cap and reserves at least a quarter of a shorter request deadline for fallback. The complete system fallback connection, including TLS, is capped at 30 seconds and shortened by any request deadline. Standard HTTPS retries finish TLS before sending any HTTP data, so requests are not replayed. The native Anthropic TLS adapter retains its profile and receives the same TCP routing fallback. TUN remains transparent; Clipal does not detect or manage proxy software, listeners, OS DNS or network settings.
- Stored `system`, `cloudflare`, `google` and `auto` values are preserved. They select no-proxy DNS and optional reference services, without changing account identity/region or enabling strict proxy preflight. Successful DoH answers are cached per account/route and normalized hostname, using each response's receipt time and returned TTL, capped at five minutes; combined A/AAAA results expire at the earliest answer expiry. Returned address slices are copied so callers cannot mutate cached or coalesced results. Positive and failed entries share a 256-entry bound, evicting the earliest expiry instead of clearing warm entries. Concurrent cold lookups share one A/AAAA sequence, including Cloudflare → Google fallback. Lookup failures back off for five seconds; automatic connections can immediately use the system hostname during backoff, while explicit DoH policies remain strict. After expiry, requests try DoH again. Waiters cancel independently; when a leader is cancelled, remaining waiters retry without inheriting its cancellation. Caller cancellation and short caller deadlines are not cached. An exhausted full five-second automatic DNS lookup budget also establishes backoff. Target TCP/TLS connection failures do not invalidate a successful DNS answer or become DNS failure entries. No business response is cached.
- Reference observations use the selected entry and contain no account credentials. They prove only the reference service response, not the target exit, registration country or billing country. OAuth regions come from defaults/user choices; observations never initialize them.
- The Web UI's public DoH reference diagnostic is opt-in per request and defaults off. A failure is reported separately after reachability checks; no account fields are written and no strict mode is exposed.
- DoH requests use fixed service endpoints and contain no account identity, language, cookies or access tokens. Every HTTP/HTTPS/SOCKS5 proxy retains service/target domains, independently of its software. VMess/VLESS and TUN are outside this layer. VPN/TUN presence is never inferred from DNS answers or listener scans.
