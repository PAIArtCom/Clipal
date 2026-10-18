# CLI takeover candidates

Checked against official public documentation, repositories and installed clients on **2026-10-18**. Eleven new clients passed isolated real requests through Clipal plus repeated apply, byte-exact rollback with original permissions, and a second cycle preserving intervening user edits. No real client credentials were copied and the watched standard user configuration paths retained their content, permissions and symlinks. Configuration inspection in the UI still reports runtime verification separately; this validation covers the versions and launch conditions recorded below.

## Unified candidate list

These are the 15 requested additions to the existing Claude, Codex, OpenCode, Antigravity, Continue, Aider, and Goose integrations. The first eleven share the same preview, baseline, apply and rollback lifecycle as the existing seven. All selected model IDs come from explicit user input or existing configuration; no product model name or context window is invented.

| Candidate | Canonical official source | Disposition |
| --- | --- | --- |
| Pi | [earendil-works/pi](https://github.com/earendil-works/pi) (the former `badlogic/pi-mono` URL redirects here) | Implemented: user models and settings JSON; preserve model metadata and authentication. |
| Qwen Code | [QwenLM/qwen-code](https://github.com/QwenLM/qwen-code) | Implemented: JSONC modelProviders catalog and OpenAI auth selection; project catalogs can replace user definitions. |
| Kimi Code CLI | [MoonshotAI/kimi-code](https://github.com/MoonshotAI/kimi-code) | Implemented: current kimi-code TOML. Requires positive existing or explicitly supplied context metadata; archived kimi-cli is a different product. |
| Copilot CLI | [github/copilot-cli](https://github.com/github/copilot-cli) | Implemented: documented shell BYOK contract; populated providers.json blocks takeover. Official npm help checked at 1.0.98 and 1.0.100-1. |
| Kilo CLI | [Kilo-Org/kilocode](https://github.com/Kilo-Org/kilocode) | Implemented: CLI OpenCode-compatible schema with Kilo-specific multi-file precedence. |
| CodeBuddy Code | [official CLI environment reference](https://www.codebuddy.ai/docs/cli/env-vars) | Implemented: settings.json plus models.json custom model with full Chat Completions URL. |
| Crush | [charmbracelet/crush](https://github.com/charmbracelet/crush) | Bounded implementation: supported legacy user JSON only; executable crushrc or overriding machine-state routes block apply. |
| iFlow CLI | [iflow-ai/iflow-cli](https://github.com/iflow-ai/iflow-cli) | Implemented: settings.json OpenAI-compatible endpoint, model and auth type. |
| TRAE Agent | [bytedance/trae-agent](https://github.com/bytedance/trae-agent) | Implemented: explicitly selected YAML file; launch standalone agent with --config-file. No invented global config directory. |
| Grok Build | [xai-org/grok-build](https://github.com/xai-org/grok-build) | Implemented: official xAI Grok Build custom model TOML; model inference only. |
| DeepSeek Harness | [deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness) | Implemented: official deepseek-harness Cordis home patch; not third-party dshline. |
| Qoder CLI | [CLI custom models](https://docs.qoder.com/cli/custom-models) | **Uncertain automatic takeover**; account-catalog wizard contract conflicts with broader official forum announcement. |
| Amp | [Model Routing](https://ampcode.com/docs/customize/model-routing) | **Supported custom endpoint; uncertain local automatic takeover**; managed connections are not a documented local settings-file routing schema. |
| Cursor CLI | [CLI configuration](https://cursor.com/docs/cli/reference/configuration) | **Unsupported for the currently proved Clipal-compatible takeover contract**; endpoint override does not establish an OpenAI/Anthropic inference API. |
| Kiro CLI | [settings](https://kiro.dev/docs/reference/settings/) | **Unsupported for the currently proved Clipal-compatible takeover contract**; no documented custom inference-provider contract was found. |

## Implemented configuration contracts

`config_dir` always selects an absolute directory and requires the client to launch with the corresponding directory/environment. Editing a file cannot update an already-running client's environment. `config_file` is offered only where the documented client launch contract can use that file. Existing unrelated models, credentials and options remain in place; rollback restores every managed file's exact original content, mode and absence.

| Product / API ID | Default and override | Managed routing / conditions | Official contract |
| --- | --- | --- | --- |
| Pi / `pi` | `~/.pi/agent`, `PI_CODING_AGENT_DIR` | `models.json` providers.clipal with per-model endpoint and OpenAI completions; `settings.json` defaultProvider/defaultModel | [models](https://github.com/earendil-works/pi/blob/482c1381a00d8d00bc81697e4a980c593db6803e/packages/coding-agent/docs/models.md) |
| Qwen / `qwen` | `~/.qwen`, `QWEN_HOME`; explicit file | `settings.json` modelProviders arrays, providerProtocol, model and security.auth.selectedType; project/CLI/env overrides require launch verification | [providers](https://github.com/QwenLM/qwen-code/blob/2f2e7256fb134513490eeefbf7743e8564bba459/docs/users/configuration/model-providers.md) |
| Kimi / `kimi` | `~/.kimi-code`, `KIMI_CODE_HOME` | `config.toml` managed provider/model, context metadata required; optional `context_window` target supplies a confirmed positive token count | [current source](https://github.com/MoonshotAI/kimi-code/tree/4a82a07f08d0b135c4349c20aae53a2d51e5c3fa) |
| Copilot / `copilot` | `~/.copilot`, `COPILOT_HOME`; selected shell startup directory | Shell BYOK exports, literal model and `/v1` endpoint. Higher-priority Copilot-only credential commands/headers are unset inside the managed block; original shell content and auth storage remain preserved. Nonempty providers/models in providers.json block apply; `COPILOT_PROVIDERS_CONFIG` selects its read-only guard | [BYOK](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-byok-models), [registry precedence](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference) |
| Kilo / `kilo` | XDG config `kilo`; `KILO_CONFIG`, `KILO_CONFIG_DIR`; explicit file | `provider.clipal`, model and optional small_model; global order config.json → kilo.json → kilo.jsonc → opencode.json → opencode.jsonc; custom file; custom-dir kilo.jsonc → kilo.json → opencode.jsonc → opencode.json; inline KILO_CONFIG_CONTENT last. Known later route overrides block apply; sources remain read-only dependencies | [loader](https://github.com/Kilo-Org/kilocode/blob/2a1903f29186e25adec0bace14f6ee589095f7eb/packages/opencode/src/kilocode/config/config.ts) |
| CodeBuddy / `codebuddy` | `~/.codebuddy`, `CODEBUDDY_CONFIG_DIR` | settings.json model + models.json registry; preserve availableModels and credentials; custom URL is the full `/v1/chat/completions` URL | [models](https://www.codebuddy.ai/docs/cli/models), [settings](https://www.codebuddy.ai/docs/cli/settings) |
| Crush / `crush` | XDG config `crush`; `CRUSH_GLOBAL_CONFIG` | crush.json legacy JSON, large/small selections and provider catalog; refuse sibling executable crushrc/.crushrc and overriding machine state; state path follows CRUSH_GLOBAL_DATA/XDG_DATA_HOME. System/project layers still require runtime verification | [source](https://github.com/charmbracelet/crush/tree/14ac9bc49eb9702c7618b9e199fae05f23b788f9/internal/config) |
| iFlow / `iflow` | `~/.iflow`; explicit file | settings.json baseUrl/apiKey/modelName/selectedAuthType; project config and case variants of IFLOW environment fields may override | [settings](https://github.com/iflow-ai/iflow-cli/blob/4642808afbc6580ac117d930f6c64ac0d84955c7/docs_en/configuration/settings.md) |
| TRAE Agent / `trae` | **Explicit absolute YAML file or directory required** | trae_config.yaml agents/models/model_providers; use --config-file; preserve auxiliary providers and existing model metadata | [config](https://github.com/bytedance/trae-agent/blob/e839e559ac61bdd0e057c375dd1dee391fee797d/trae_agent/utils/config.py) |
| Grok Build / `grok` | `~/.grok`, `GROK_HOME` | config.toml [models].default and [model.clipal], OpenAI Chat Completions; GROK_CONFIG_PATH is a restricted overlay, not a routing replacement file | [custom models](https://github.com/xai-org/grok-build/blob/2bdd1d6a6369de0e8c68132ea4539e9abd9e14a8/crates/codegen/xai-grok-pager/docs/user-guide/11-custom-models.md) |
| DeepSeek Harness / `deepseek-harness` | `~/.dsh`, `DSH_HOME` | cordis.patch.yml **root sequence**; llm-pi-ai provider + agent-default-model; preserve plugins and credential references; refuse conflicting modelOverrides | [provider](https://github.com/deepseek-ai/deepseek-harness/blob/687f282e8625c6b8d11ff58211a90524be2e4317/packages/llm/llm-pi-ai/README.md) |

All API cards include `target_capabilities`. The UI derives configuration controls from this metadata rather than maintaining a second product list. Inspection remains `not_verified`: project, session, enterprise and auxiliary-model overrides cannot be proved without the actual client launch context. Shell takeovers require a fresh terminal after apply **and rollback**; sourcing a restored file does not unset values exported by an old process.

## Installed-client validation

The official default `latest` npm channel or latest official release was used. DeepSeek has no stable release, so its official `latest` is a release candidate. TRAE publishes no release; the current official source commit was installed without source changes.

| Client | Executed version | Installation source |
| --- | --- | --- |
| Pi | 1.3.0 | `@earendil-works/pi-coding-agent` |
| Qwen Code | 0.25.2 | `@qwen-code/qwen-code` |
| Kimi Code CLI | 2.2.0 | `@moonshot-ai/kimi-code` |
| Copilot CLI | 1.0.98 | `@github/copilot` |
| Kilo CLI | 7.8.12 | `@kilocode/cli` |
| CodeBuddy Code | 2.168.2 | `@tencent-ai/codebuddy-code` |
| Crush | v0.98.1 | Official Darwin arm64 release; archive checksum verified |
| iFlow CLI | 0.5.19 | `@iflow-ai/iflow-cli` |
| TRAE Agent | 0.1.0 | Official commit `e839e559ac61bdd0e057c375dd1dee391fee797d` |
| Grok Build | 1.0.62 (`1474f9cbdd91`) | `@xai-official/grok` |
| DeepSeek Harness | 0.2.0-rc.2 | `@deepseek-ai/dsh` official `latest`, not `next`/`alpha` |

Every row returned its unique assistant marker and a fresh HTTP 200 recorded by the isolated gateway using an account-supported model selected from the upstream catalog. The complete cycle was apply → repeated apply → real request → original bytes/permissions restored → user edit → second apply/rollback preserving that edit. Clients launched with private configuration directories and no copied account credentials. The watched standard user configuration files remained unchanged. This proves these minimal requests, not arbitrary tools, long sessions or enterprise settings.

TRAE startup requires the official lockfile-compatible `pydantic==2.11.5` for its pinned MCP dependency, plus the declared evaluation-extra dependencies `docker==7.1.0` and `pexpect==4.9.0` because the CLI imports them unconditionally. Use the official source/lockfile environment; a bare unconstrained base-package installation did not start. The smoke task allows only the non-mutating `task_done` completion tool and requires trajectory `success: true`; a printed reply followed by task failure is rejected.

The reusable runner accepts repeated `--client-manifest` files. Each manifest is a JSON object with `products` keyed by product ID; an entry contains absolute `binary`, actual `version`, optional `version_args` and installation `source`. Install each client in a separate prefix, then use an explicitly confirmed model and Kimi context budget:

```sh
go build -o /tmp/clipal-takeover ./cmd/clipal
python3 scripts/cli_takeover_smoke.py --binary /tmp/clipal-takeover \
  --products pi,qwen,kimi,copilot,kilo,codebuddy,crush,iflow,trae,grok,deepseek-harness \
  --client-manifest /absolute/npm-a-manifest.json \
  --client-manifest /absolute/npm-b-manifest.json \
  --client-manifest /absolute/native-manifest.json \
  --model "$VERIFIED_GATEWAY_MODEL" --context-window "$CONFIRMED_CONTEXT_BUDGET" \
  --output /tmp/clipal-takeover-results.json
```

The runner uses private HOME/XDG/config/workspace directories, bypasses inherited external proxies only for loopback management HTTP, and passes subprocess arguments without interpolation. Timeouts terminate the client process group before rollback. Failed requests still exercise recovery and the next cycle; individual failures remain failures and do not prevent other clients from being checked. The report fails on cleanup errors or changes to watched user configuration fingerprints. This is configuration isolation, not an OS filesystem sandbox: Copilot requires its documented `--allow-all-tools` for noninteractive mode, while the fixed verification prompt requests no tools; TRAE permits only its completion tool. The runner does not prove denial of arbitrary host paths. Dependency-free negative controls run in `make test-unit`, including prompt-echo rejection, TRAE final-state checks, literal arguments, descendant termination, proxy isolation, permission-only changes and persisted cleanup-failure status.

## Qoder CLI

**Judgment: uncertain; defer automatic takeover.** The current [CLI custom-model documentation](https://docs.qoder.com/cli/custom-models) requires `/model` → Custom → wizard, with provider, model and credential fields determined by the account catalog. It explicitly prohibits manually configuring BYOK in `settings.json`. This is stronger evidence for supported configuration than inferring a writable schema from the IDE.

The official [September 12, 2026 forum announcement](https://forum.qoder.com/t/qoder-cli-now-supports-custom-models/12444) describes **v1.1.50** accepting OpenAI-compatible or Anthropic-compatible models with Base URL and Model ID. The current wizard documentation does not promise that arbitrary endpoints remain available for every account, and publishes no stable serialized BYOK schema. That conflict prevents a supported automatic file edit.

The [CLI reference](https://docs.qoder.com/cli/cli-reference) documents `--config-dir` (default `~/.qoder`), `--model`, and `--list-models`. The [model documentation](https://docs.qoder.com/cli/model) identifies `~/.qoder/settings.json` for selected-model preferences; this does not authorize writing BYOK credentials there. Exact inference paths, environment endpoint overrides, and serialization are **not proved**. The separate [IDE custom-model page](https://docs.qoder.com/qoder/custom-models) supports richer compatible providers but is not CLI evidence.

Reconsider when official CLI documentation/source publishes the writable provider contract, or a supported export/import interface. Until then, use the product wizard manually. Current CLI docs are unversioned; the forum supplies the only checked minimum version above.

## Amp

**Judgment: supported arbitrary custom endpoints; uncertain reversible local automatic takeover.** Current [Model Routing](https://ampcode.com/docs/customize/model-routing) explicitly supports Custom URL connections, managed in personal/workspace account settings or through `amp config model-providers`. It defines the following path composition:

| Format | Request path appended to base URL |
| --- | --- |
| `chat-completions` | `/chat/completions` |
| `responses` | `/responses` |
| `anthropic-messages` | `/v1/messages` (an existing trailing `/v1` is not duplicated) |
| `google-genai` | `/models/<model>:streamGenerateContent?alt=sse`, with the documented Vertex variant |

OpenAI and Anthropic formats use bearer authorization. Model mapping can translate an exact canonical Amp model to a provider model ID; personal connections precede workspace connections, then Amp fallback. Models outside a connection's mapping may still use Amp. These properties rule out both hardcoded model assumptions and a claim that one custom connection necessarily captures all traffic. The endpoint must be reachable **from Amp**; local loopback reachability from a CLI inference process is not established by these docs.

[CLI configuration](https://ampcode.com/docs/cli/settings) documents `~/.config/amp/settings.json` or `.jsonc`, nearest workspace `.amp/settings.json` or `.jsonc`, and `--settings-file`; workspace settings override user settings. It does not establish a local model-provider connection schema. Do not invent `amp.*` endpoint settings or write account connections through the existing file-only rollback lifecycle. Reconsider with a documented local routing override or an explicitly designed account-connection lifecycle and verified inference location. Docs are unversioned and were checked on the date above. The [BYOK removal announcement](https://ampcode.com/news/no-more-byok) now contains an update restoring managed provider connections and cannot support blanket exclusion.

## Cursor CLI

**Judgment: unsupported under the currently proved compatible-provider contract; defer integration.** [Authentication](https://cursor.com/docs/cli/reference/authentication) documents Cursor login and Cursor-issued `CURSOR_API_KEY` / `--api-key`, not an arbitrary upstream provider key. The [configuration schema](https://cursor.com/docs/cli/reference/configuration) documents `~/.cursor/cli-config.json`, `CURSOR_CONFIG_DIR`, and Linux/BSD `$XDG_CONFIG_HOME/cursor/cli-config.json`; project `.cursor/cli.json` supports permissions only. Its current schema version is **1**, and it has no documented compatible-provider base URL field.

[ACP documentation](https://cursor.com/docs/cli/acp) shows `agent -e https://api2.cursor.sh acp` as an endpoint option. That proves Cursor service endpoint selection, not OpenAI Chat Completions, Responses, or Anthropic Messages compatibility. ACP's stdio JSON-RPC interface is an agent integration protocol, not the upstream model API. Neither an endpoint flag, HTTP proxy support, nor IDE API-key features establishes the missing request-body/path contract. Exact compatible inference paths and provider environment overrides remain **unproved**. Do not point `--endpoint` at Clipal by assumption. Current docs are unversioned; no installed binary version was inspected.

## Kiro CLI

**Judgment: unsupported under the currently proved compatible-provider contract; defer integration.** Current [settings reference](https://kiro.dev/docs/reference/settings/) places CLI preferences in `~/.kiro/settings/cli.json`, supports `kiro-cli settings` and workspace overrides, and documents `chat.defaultModel`; it supplies no arbitrary OpenAI/Anthropic provider URL, credentials, or request schema. [Models](https://kiro.dev/docs/models/) describes Kiro's supplied model catalog rather than custom providers.

[Authentication](https://kiro.dev/docs/getting-started/authentication/) (page updated **2026-10-17**) describes browser identity providers and `KIRO_API_KEY` for headless access, with browser-session precedence. This is a Kiro-issued key consuming subscription credits, not proof of a custom inference provider. No official inference protocol/path or supported endpoint environment override was established. Internal-looking service overrides mentioned in issue reports and third-party Kiro gateways do not prove Clipal compatibility. Current documentation covers CLI **V3**; no source revision or installed binary was inspected. Reconsider on an official provider contract or an independently validated adapter for its actual service protocol; do not implement a guessed OpenAI URL rewrite.

## Acceptance boundary

This catalog records current research, including negative evidence and unresolved contracts. The four deferred automatic integrations have no permission to modify user credentials, use undocumented settings, or switch protocols implicitly. Any accepted integration must reuse the existing takeover plan/apply/rollback lifecycle, preserve unknown settings and authentication, derive model choices from configured models, and validate isolated temporary configurations before claiming support.
