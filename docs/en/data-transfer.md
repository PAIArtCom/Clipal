# Data Import, Export, and Backup

Clipal uses one canonical plaintext JSON document for complete backups: `clipal.data/v1`. It is a data format, not an archive, and contains the operator-owned state needed to move or restore a private Clipal installation:

- global and per-client configuration;
- API-key and OAuth provider definitions;
- OAuth credentials;
- usage counters and cost history.

OAuth credentials include access tokens and refresh tokens when present. The
plaintext export is a complete, operator-owned migration copy of the instance.
The data API is `/api/data/export`; the former configuration-only
`/api/config/export` endpoint is not part of this new contract.

The top-level contract is:

```json
{
  "schema": "clipal.data",
  "schema_version": 1,
  "exported_at": "2026-07-12T12:00:00Z",
  "producer": { "name": "clipal", "version": "v0.x" },
  "data": {
    "global": {},
    "clients": {
      "claude": { "mode": "auto", "providers": [] },
      "openai": { "mode": "auto", "providers": [] },
      "gemini": { "mode": "auto", "providers": [] }
    },
    "credentials": [],
    "usage": { "clients": {} }
  }
}
```

This is a new contract. Older Web UI configuration-only JSON exports are intentionally not accepted.

## CLI

```bash
# Complete backup; the output file is created with mode 0600.
clipal export -o clipal-data.json

# Detect the format, print a plan, then ask before applying.
clipal import clipal-data.json

# Inspect without writing anything.
clipal import --dry-run clipal-data.json

# Explicit native merge and unattended apply.
clipal import --format clipal --mode merge --yes clipal-data.json
```

Native imports default to `replace`. `merge` replaces global settings, merges providers by name, merges credentials by account identity, and adds usage counters.

Schema version 1 defines exactly three client keys: `claude`, `openai`, and `gemini`. Unknown client keys and trailing JSON documents are rejected instead of being silently dropped. All integer usage and cost fields are transported as raw file text by the Web UI, so full signed 64-bit values remain exact.

The same import command accepts CLIProxyAPI single-account OAuth JSON, sub2api export JSON, and Codex `auth.json`. External formats are credentials-only and always use `merge`; Clipal links imported accounts to the appropriate client without replacing configuration or usage.

Imports accept regular files (including symlinks whose targets are regular files), at most 512 files, 16 MiB per file, and 64 MiB of original file data in total. These limits are identical for local CLI imports and imports delegated to a running instance.

## Web UI

Open **Global Settings → Import & Backup**. Select one native backup or one or more external credential JSON files, preview the detected format and change summary, choose the import mode when appropriate, and apply the reviewed plan. Export downloads `clipal-data.json` from the same panel.

Preview never writes data. Apply snapshots configuration, credentials, and usage first; if persistence or runtime reload fails, Clipal restores all three snapshots.

When the CLI finds a running Clipal instance for the same configuration directory, it sends export, preview, and apply requests through that instance. This keeps backups current and puts runtime telemetry and imported usage in one serialized transaction. If the configured address is occupied but the instance cannot be verified, the CLI refuses to fall back to an independent local write. Web imports use the same live telemetry store; requests that finish during an import wait for the transaction and are retained afterward. OAuth storage symlinks remain symlinks during replace and rollback.
