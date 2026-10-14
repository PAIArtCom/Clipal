## Role

Outbound owns request-route DNS selection, DoH resolution and standard proxy connections used by OAuth requests and provider consistency diagnostics. It leaves stored account identity/region preferences, operating-system DNS, language, timezone and Desktop state unchanged.

## Owns

- Google and Cloudflare DNS wireformat HTTPS queries, bounded responses and route-local TTL caches.
- Direct, HTTP/HTTPS CONNECT and SOCKS connections preserving original HTTP hosts, TLS SNI and certificate verification. All standard proxy routes retain hostnames for proxy-owned DNS/routing; DoH-enabled routes without an explicit proxy use returned target IPs.
- DoH bootstrap, cancellation and fail-closed DNS errors on routes without explicit proxies. Fixed service IPs bootstrap these routes. Proxy business requests never require public DoH, including saved Cloudflare/Google choices.
- Explicit public DoH reference lookups through the selected proxy, retaining service domains. These optional diagnostics do not control target DNS or block business requests.
- Staggered IPv4/IPv6 connection races, bounded candidate attempts and cancellation/cleanup of unused connections on resolved-IP routes.
- Effective proxy-entry and resolver facts from unchanged provider/global/environment policy; no software controller or listener discovery.

## Touchpoints

- `internal/oauth` wraps account clients and supplies the existing Anthropic TLS adapter through `WithDialContext`.
- `internal/proxy` resolves provider proxy policy before selecting a reusable account client.
- `internal/web` uses the same routing layer for anonymous connection diagnostics and clearly labeled reference observations without passing OAuth credentials.

## Change Guide

- Run `go test -race ./internal/outbound ./internal/oauth ./internal/proxy ./internal/web` after changing connection, cache, lifecycle or environment behavior.
- Verify IPv4 stalls do not block working IPv6, same-family retries share the request deadline, and unused connections close without cancelling a returned CONNECT tunnel.
- Keep original-domain TLS verification and proxy authentication intact. A failed required DoH lookup on a no-proxy route never falls back to system DNS; a failed proxy connection never falls back to a direct target connection.
- Extend the shared local TLS/tunnel test fixture for proxy variants rather than adding a second transport harness.
- Keep proxy DNS ownership explicit: no public DoH preflight gates proxy connections. Optional reference queries cannot verify the proxy's DNS backend, target IP or exit; matching entries cannot prove matching target exits.
- Honor explicit direct/custom policies, inherited global settings, environment proxies and NO_PROXY. Never decorate these policies with software discovery or automatic takeover.
- Distinguish target DNS from first-hop proxy hostname resolution. Proxy URLs with hostnames still need the system resolver to establish the first hop; diagnostics report this boundary.

## Decisions

- OAuth account DNS defaults to `auto`: evaluate proxy policy for each target; a standard proxy owns hostname resolution, while no-explicit-proxy routes use Cloudflare → Google DoH. This includes target-specific environment/NO_PROXY decisions. TUN is transparent and no explicit proxy does not prove physical direct routing.
- Stored `system`, `cloudflare`, `google` and `auto` values are preserved. They select no-proxy DNS and optional reference services, without changing account identity/region or enabling strict proxy preflight. Successful DoH answers are cached by route and TTL.
- Reference observations use the selected entry and contain no account credentials. They prove only the reference service response, not the target exit, registration country or billing country. OAuth regions come from defaults/user choices; observations never initialize them.
- The Web UI's public DoH reference diagnostic is opt-in per request and defaults off. A failure is reported separately after reachability checks; no account fields are written and no strict mode is exposed.
- DoH requests use fixed service endpoints and contain no account identity, language, cookies or access tokens. Every HTTP/HTTPS/SOCKS5 proxy retains service/target domains, independently of its software. VMess/VLESS and TUN are outside this layer.
