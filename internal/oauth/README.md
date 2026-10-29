## Role

OAuth owns account credentials, stable account identity matching, account-scoped request environment settings, and the transport wrapper that applies those settings without changing the host operating system.

## Owns

- `Credential` persistence, refresh-safe updates, and the account identity key used to recognize the same upstream account.
- `AccountEnvironmentProfile`, which joins a non-secret account identity with the stored request environment and its local configuration state.
- Validation and normalization of country, language, locale, IANA timezone, DNS policy, and environment-derived request headers.
- HTTP client construction that applies account headers and delegates route and DNS policy to `internal/outbound`.

## Touchpoints

- `internal/web` loads and edits account environments and runs credential-free diagnostics.
- `internal/proxy` prepares OAuth clients with the selected account environment and provider route policy.
- `internal/transfer` serializes request environment settings while keeping credentials in the existing protected transfer flow.
- `internal/outbound` owns proxy selection, DNS resolution, and connection routing; OAuth does not discover or mutate host networking.

## Change Guide

- Preserve the distinction between account-owned request preferences, live route observations, and upstream authentication results.
- Never infer or persist account country, timezone, or identity from an exit service or proxy observation.
- Keep access and refresh tokens out of `AccountEnvironmentProfile` and diagnostic responses.
- Run `go test ./internal/oauth ./internal/proxy ./internal/web` after changing environment normalization, transport construction, identity matching, or persistence.
- Verify legacy credentials, token refresh, account identity changes, DNS policy selection, and credential-free diagnostics together.

## Decisions

- `Credential` remains the only persisted credential record. `AccountEnvironmentProfile` is a derived, non-secret domain view and diagnostic observations are in-memory evidence.
- A configured request environment belongs to the OAuth account, not a provider display name or conversation. It never mutates host language, timezone, locale, browser state, or proxy settings.
- `configured`, `needs_confirmation`, `partial`, and `unconfigured` describe local request-profile state only; they do not claim network reachability or upstream account acceptance.
- Evidence entries use `unknown`, `observed`, `verified`, or `blocked` and remain separate from the stored profile because routes and upstream availability can change independently.
- Evidence entries are scoped to `request_profile`, `target_connection`, `reference_exit`, or `oauth_auth`; one diagnostic can return multiple entries, and a successful reference observation never implies OAuth authentication.
- `AccountEnvironmentProfile.Fingerprint()` is the stable generation key for environment-aware transport pools; it hashes only the effective request environment and excludes unconfirmed region fields.
- `AccountRequestContext` binds the refreshed credential, derived account profile, and effective request environment for one provider attempt; it is internal and never serialized with credential fields.
