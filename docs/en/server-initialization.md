# Server Initialization

`clipal init` is a one-time helper for a server account. It detects selected
AI coding CLIs, invokes their official installers only when missing, starts the
Clipal user service, and applies Clipal's existing safe user-level takeover.
It is not a CLI version manager or a remote terminal host.

```bash
clipal init
clipal init --tools codex,claude,antigravity
clipal init --dry-run
```

To migrate an existing local setup, export its full backup, copy it to the
server, and restore it during initialization:

```bash
# local machine
clipal export -o clipal-data.json
scp clipal-data.json user@server:/tmp/

# server
clipal init --import /tmp/clipal-data.json
```

`--import` restores Clipal providers, OAuth credentials, and runtime settings in
replace mode. Initialization then applies takeover for the current server user;
it does not treat the local machine's CLI files as active server configuration.

The default tools are Codex CLI, Claude Code, and Antigravity CLI (`agy`).
Gemini CLI remains supported when explicitly selected with `--tools gemini`.

For Codex, Claude, and Gemini, takeover continues to use the existing preview,
backup, apply, and rollback implementation. Antigravity is installed and ready
for its official remote-login flow, but Clipal does not yet claim proxy takeover
for `agy`: that requires a verified official Antigravity configuration contract.

## Remote Access

The management UI remains localhost-only. Tunnel it instead of exposing the
management port:

```bash
ssh -L 3333:127.0.0.1:3333 user@server
```

Then open `http://127.0.0.1:3333/` locally to configure a provider or complete
an OAuth flow. Run `agy` over SSH to follow Antigravity's remote login URL.

To deliberately expose the proxy or management UI, set the corresponding
`allow_remote_proxy` or `allow_remote_web_ui` setting in Global Settings. Both
are disabled by default; remote access has no built-in authentication.

## Safety

- Run `clipal init` as the same Unix user that will run the AI CLIs.
- Existing CLI binaries are not reinstalled.
- Clipal never prints or transfers API keys or OAuth credentials during init.
- Use the CLI Takeover page to inspect its preview or roll back a completed
  Codex, Claude, or Gemini takeover.
