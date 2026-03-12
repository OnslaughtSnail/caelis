# caelis

`caelis` is a clean-slate Agent framework kernel prototype.
It is designed to be extracted as a standalone repository.

## Scope
- Kernel-first architecture.
- Minimal M1: single agent + synchronous tool loop.
- Experimental delegated child-run prototype via core `DELEGATE`.
- Compile-time plugin registration + provider-based assembly.
- Built-in mandatory `READ` tool with line/token caps.
- Modular system prompt pipeline with built-in identity and `AGENTS.md` policy assembly.
- Skills metadata auto-discovery from `~/.agents/skills` and prompt injection.
- Policy hooks (egress/audit/output) with default allow behavior.
- Unified model provider layer with API types: `openai`, `openai_compatible`, `gemini`, `anthropic`, `deepseek`.
- CLI shell and real-model eval runner.
- Tool execution runtime abstraction (`no_sandbox` / `sandbox` with extensible backend type).
- Token-budget based auto compaction (append-only event strategy).
- MCP ToolSet integration (`stdio` / `sse` / `streamable`), assembled via `mcp_tools` provider.
- Experimental CLI-only LSP plugin provider (`lsp_tools`), opt-in via `-experimental-lsp`.
- Full Bubble Tea TUI with streaming output, tool display, approval UX, reasoning blocks, and inline diff.
- Model catalog (static snapshot + remote refresh) with per-model reasoning capability discovery.
- Tool-level authorization baseline with per-tool allow/deny policy evaluation.
- Workspace boundary policy restricting filesystem tools to project root.
- `@file` / `@image` input reference parsing in user prompts; clipboard image capture.
- Headless execution mode for non-interactive single-shot runs.

## Quick Start
```bash
make build
make vet
make test
```

Run CLI:
```bash
go run ./cmd/cli \
  -tool-providers workspace_tools,shell_tools,mcp_tools \
  -policy-providers default_allow \
  -model deepseek/deepseek-chat \
  -permission-mode default \
  -experimental-lsp \
  -mcp-config ~/.agents/mcp_servers.json \
  -stream=true \
  -thinking-mode=off \
  -compact-watermark=0.7 \
  -context-window=65536 \
  -thinking-budget=1024 \
  -skills-dirs "~/.agents/skills" \
  -session demo \
  -input "hello"
```

Show version:
```bash
go run ./cmd/cli -version
```

Launcher modes:
- default: `console` (when mode keyword omitted)
- explicit: `go run ./cmd/cli console ...`
- reserved placeholders: `go run ./cmd/cli api ...`, `go run ./cmd/cli web ...`

Tool execution runtime flags:
- `-ui`: interactive UI mode `auto|tui|line`
  - default `auto`: use `tui` when stdin/stdout are TTY, otherwise fallback to `line`
  - `tui`: force terminal UI mode (requires TTY)
  - `line`: force line-editor mode
  - TUI input shortcuts (MVP): `↑/↓` history, `←/→` cursor move, `Home/End`, `Ctrl+A/E` line start/end, `Ctrl+U/W` delete, `Ctrl+C` interrupt/quit
- `-permission-mode`: `default|full_control`
  - `full_control`: run commands on host directly, no approval required
  - `default`: run commands in sandbox by default; escalated host command requires approval
- `-sandbox-type`: sandbox backend type (when `-permission-mode=default`, pluggable by runtime registry)
  - default: macOS uses `seatbelt`; Linux uses `landlock`
  - on macOS, only `seatbelt` is supported in `default` mode
  - on Linux, `landlock` and `bwrap` are supported in `default` mode
  - built-in: `seatbelt` (macOS `sandbox-exec`)
  - built-in: `landlock` (Linux default backend; uses `PR_SET_NO_NEW_PRIVS`, seccomp network filtering, and Landlock filesystem rules; prefers a sibling `caelis-sandbox-helper` binary when present, otherwise falls back to the current CLI executable as helper; avoids Linux user-namespace dependencies but cannot enforce read-only subpaths such as `.git` inside a writable workspace)
  - built-in: `bwrap` (Linux optional backend; uses bubblewrap for stronger namespace-based filesystem and network isolation; requires both the `bwrap` binary and a working Linux user-namespace setup, or a setuid-root `bwrap`; install via `apt install bubblewrap` or equivalent)
- `-mcp-config`: MCP server config JSON path, default `~/.agents/mcp_servers.json` (missing file means MCP disabled)
- `-credential-store`: credential persistence mode (`auto|file|ephemeral`), default `auto`

Fallback behavior:
- In `default` mode on macOS, runtime only supports `seatbelt`; when unavailable it falls back to `host+approval`.
- In `default` mode on Linux, runtime defaults to `landlock`; you can switch to `bwrap` explicitly only on machines where bubblewrap startup prerequisites are satisfied.
- If sandbox backend is unavailable at startup (for example `sandbox-exec` missing on macOS, Landlock unsupported by the host kernel, `bwrap` missing on Linux, or Linux user namespaces are blocked so `bwrap` cannot create its sandbox), CLI falls back to `host+approval` and prints a warning.
- In non-interactive runs without approver context, escalated commands return `ApprovalRequiredError` with a hint to use interactive approval or `-permission-mode full_control`.
- If a command is routed to sandbox but fails with "command not found" (`exit code 127`), BASH asks for approval and retries on host.

Approval UX in interactive CLI:
- Host escalation requires explicit approval by default.
- Approval prompt options:
  - `y`: allow once
  - `a`: allow this exact command for current session (no more prompts for same command text)
  - `n` / empty: cancel
- On cancel, current agent run stops immediately and control returns to the user prompt.

BASH timeout behavior:
- BASH tool has a default timeout of `30m` for direct foreground execution.
- BASH tool has no default idle timeout; long-running commands are expected to use async task flow rather than be killed for silence alone.
- `yield_time_ms` now controls foreground vs async behavior:
  - omitted: wait briefly (`5s`) and return a `task_id` if still running
  - `0`: return a `task_id` immediately
  - `-1`: force synchronous foreground execution until completion
- When `tty=true` and `yield_time_ms` is omitted, BASH waits briefly (`1200ms`) and then returns a `task_id` so `TASK write` can continue the interactive session.
- Per-call `timeout_ms` and `idle_timeout_ms` overrides were removed from the BASH tool schema.
- Host/sandbox execution enforces non-interactive environment defaults (`CI=1`, `TERM=dumb`, `GIT_TERMINAL_PROMPT=0`, `PAGER=cat`, `NO_COLOR=1`).

System prompt pipeline order (high -> low):
1. `~/.{app}/prompts/IDENTITY.md`
2. `~/.{app}/prompts/AGENTS.md`
3. `{workspace}/AGENTS.md` (optional)
4. CLI-provided runtime fragments (for example runtime context, experimental LSP routing when enabled)
5. `~/.{app}/prompts/USER.md` + `-system-prompt` runtime override
6. skills metadata section (auto-discovered)

If template files are missing, CLI auto-creates defaults in `~/.{app}/prompts`.

MCP config example (`~/.agents/mcp_servers.json`):
```json
{
  "cache_ttl_seconds": 60,
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."],
      "include_tools": ["read_file", "list_directory"]
    },
    "browser": {
      "transport": "streamable",
      "url": "http://127.0.0.1:8787/mcp"
    }
  }
}
```

MCP Web tools guidance:
- First phase uses MCP, not an in-kernel `web_tools` provider.
- Prefer read-only `search` / `fetch` tool exposure.
- Example config and UX notes: [docs/mcp_web_tools.md](docs/mcp_web_tools.md)

Interactive slash commands:
- `/help`: show command help
- `/version`: show version info
- `/status`: show current model/thinking/stream/execution status
- `/new`: start a fresh conversation session
- `/fork`: fork current conversation into a new named session
- `/permission [default|full_control]`: show or switch permission mode
- `/sandbox [<type>]`: show or switch sandbox backend type
- `/models`: list available model aliases
- `/model <alias> [reasoning]`: switch model; optionally set reasoning level (`off|on|low|medium|high|very_high`)
- `/thinking <auto|on|off> [budget]`: switch thinking mode
- `/effort <low|medium|high|off>`: set reasoning effort
- `/stream <on|off>`: switch stream mode
- `/reasoning <on|off>`: toggle reasoning content rendering
- `/tools`: show current assembled tool list
- `/compact [note]`: trigger one manual compaction
- `/exit` / `/quit`: quit

Session behavior:
- Interactive CLI starts in a new session by default.
- Pass `-session <id>` to resume or continue an existing session.
- Sessions with no conversation events are not persisted.

CLI runtime preferences (`stream`, `thinking-mode`, `thinking-budget`, `reasoning-effort`, `reasoning display`, `permission-mode`, `sandbox-type`) are persisted in app config and reused on next start.

Config env placeholder behavior:
- Provider config (`~/.{app}/{app}_config.json`) supports `${ENV_NAME}` placeholders in string fields (for example `"token": "${DEEPSEEK_API_KEY}"`).
- On startup, CLI optionally loads `.env` from:
  - current working directory
  - config file directory (`~/.{app}/`)
- `.env` is optional; missing `.env` is allowed.
- If config contains unresolved placeholders, startup fails with an explicit `invalid config` error and the unresolved env var name.

LSP behavior:
- LSP tools live in a CLI-only experimental plugin provider: `lsp_tools`.
- They are disabled by default; enable them with `-experimental-lsp` or by explicitly adding `lsp_tools` to `-tool-providers`.
- When enabled, CLI auto-detects workspace language and injects one language server toolset when a supported server exists locally.
- Supported workspace language families: Go, Python, TypeScript, JavaScript, Rust, C/C++.

Manual compaction command in interactive mode:
```text
/compact optional note
```

Run lightweight eval:
```bash
go run ./eval/cmd -suite light
```

Eval model selection behavior:
- Eval reads model credentials from environment (`DEEPSEEK_API_KEY`, `GEMINI_API_KEY`).
- It runs only model aliases whose credentials are configured.
- If no eval credentials are configured, eval exits with a clear error.
- `eval/cmd` optionally loads nearest `.env` (for convenience only); `.env` is not required.

Run real-model eval matrix (stream/non-stream + thinking/non-thinking):
```bash
go run ./eval/cmd \
  -suite light \
  -models "deepseek-chat,gemini-2.5-flash" \
  -stream-modes both \
  -thinking-modes both \
  -thinking-budget 1024
```

## Security Notes
- `/connect` always prompts for `api_key` and updates provider config immediately.
- CLI runtime uses provider config (`~/.{app}/{app}_config.json`) as the single source for model auth at request time.
- Credential store (`~/.{app}/{app}_credentials.json`) is auxiliary only (`-credential-store=auto` by default, `0700` dir + `0600` file + atomic writes). Existing credential entries are merged into provider config on startup when config token is missing.
- `token_env` is no longer used as a runtime auth source; direct env override behavior is removed.

## Release
- Current release: `v0.0.15` (see `VERSION` and `CHANGELOG.md`).
- Local dry-run package:
```bash
make release-dry-run
```
- CI release is triggered by git tag push like `v0.0.1`.

### npm Distribution
- npm package source lives in `npm/` and publishes package `@onslaughtsnail/caelis`.
- End users install with:
```bash
npm i -g @onslaughtsnail/caelis
```
- npm publish runs in `.github/workflows/release.yml` after GoReleaser uploads release assets.
- npm package downloads platform binary from GitHub Releases during `postinstall`.

#### One-time GitHub/npm setup
1. Login to npm and ensure package name `@onslaughtsnail/caelis` is available (or change name in `npm/package.json`).
2. In npm package settings, configure **Trusted Publisher**:
   - Provider: GitHub Actions
   - Repository: `OnslaughtSnail/caelis`
   - Workflow file: `release.yml`
3. In GitHub repo settings, ensure Actions are allowed and workflow permissions are not blocking OIDC.
4. Push a tag like `v0.0.3` to trigger release + npm publish.
5. Detailed checklist: `docs/npm-release.md`.
