# Contributing

## Toolchain

- Go version: use the version pinned in [`go.mod`](go.mod)
- Lint: `golangci-lint`
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
