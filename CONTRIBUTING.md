# Contributing

## Toolchain

- Go version: use the version pinned in [`go.mod`](go.mod)
- Python 3.9+ for isolated CLI verification and its dependency-free harness regressions
- Lint: `golangci-lint` v2.13.2 (matching CI)
- Security scan: `govulncheck`

## Common Commands

```bash
make install-hooks
make test-unit
make test-smoke
make lint
make lint-fix
make vuln
make ci
```

## Git Hooks

Install the repository hooks once after cloning:

```bash
make install-hooks
```

The pre-commit hook runs `gofmt` on staged Go files, stages formatting changes, runs `golangci-lint run --fix ./...`, stages auto-fixed Go files, and finishes with `golangci-lint run ./...`. If a staged Go file also has unstaged changes, the hook blocks first so it does not accidentally add unrelated work to the commit. If `golangci-lint` is installed outside the usual locations, set `GOLANGCI_LINT=/path/to/golangci-lint` before committing.

Make defaults `GOTOOLCHAIN` to the exact Go version declared in `go.mod`,
matching CI for builds, tests, lint and vulnerability scans. This keeps a newer
host compiler from producing export data or source syntax unsupported by the
analysis tools. Go downloads that toolchain if it is not already installed.
An explicit `GOTOOLCHAIN` overrides this default; ensure the selected compiler
is supported by the installed analysis tools.

`make lint` and `make lint-fix` share tool discovery in `scripts/lint.sh`.
The pre-commit hook invokes these Make targets, using the same toolchain and
`GOLANGCI_LINT` override as manual checks.

## Code Organization

- `cmd/clipal`: CLI entrypoints only
- `internal/app`: application bootstrap and runtime assembly
- `internal/config`: config loading, normalization, validation
- `internal/proxy`: request routing and failover runtime
- `internal/web`: localhost-only management UI and HTTP API
- `internal/service`: OS background service planning/execution

## Dependency Rules

- `cmd/clipal/main.go` assembles runtime through `internal/app`
- `internal/config` must stay independent from runtime packages
- `internal/service` must not depend on `internal/proxy`, `internal/web`, or `internal/app`

These rules are enforced by [`.golangci.yml`](.golangci.yml).

Web UI changes follow the [CSS guidelines](docs/web-ui-css-guidelines.md) for
style ownership, shared controls, spacing, responsive layout, and visual review.

## Client Configuration Tests

The integration and Web API test binaries share a `TestMain` environment setup
from `internal/testutil`. It creates a private HOME and clears inherited client
directory, inline configuration, and routing overrides before parallel tests
start. Tests of environment precedence should set their overrides explicitly
with `t.Setenv` in non-parallel tests. Do not clear environment variables inside
parallel test helpers or disable production environment lookup to isolate tests.

Subprocess regression checks rerun the existing client lifecycle and API tests
with inherited host configuration, verify each selected test ran, and check that
the host files and permissions remain unchanged. Extend the shared environment
setup and these checks when a client gains another environment override.

The real-client runner `scripts/cli_takeover_smoke.py` accepts repeated
`--client-manifest` install manifests with a `products` object keyed by product
ID. Each entry supplies an absolute `binary`, actual `version`, and optional
`version_args`. It checks fresh gateway activity, assistant output and successful
client completion, restores baseline bytes and permissions, and verifies a
second cycle. It makes real model requests; harness unit tests in `make test-unit`
use no upstream accounts. Kimi validation requires an explicit positive
`--context-window` budget.

## Before Opening a PR

```bash
make test-unit
make lint
make vuln
make test-smoke
```

If you change configuration handling or startup behavior, also verify:

- `clipal status`
- `clipal service status`
- Web UI config save/reload flows

## Release Checks

Push the final candidate commit, including its release notes, and verify CI and
Security Scan succeeded for that exact SHA before pushing a version tag.
The tag-triggered release workflow also calls the same CI and security workflows
for its commit. Its build and publication job depends on both calls succeeding;
failed, cancelled, or skipped checks prevent GitHub Release and npm publication.
Check jobs have read-only repository access; publication permissions belong to
the dependent publication job.

The manual npm recovery workflow resolves its input tag to an immutable commit
once, checks that commit with the same workflows, and checks out that SHA for
both package publications. It cannot bypass failed checks or silently switch to
another commit if the tag moves during the run.

After publication, verify the GitHub Release assets and checksums and query both
`clipal` and `@paiart/clipal` on the public npm registry for the intended version
and dist-tag. Local builds and successful uploads alone do not establish that
the release is publicly available.
