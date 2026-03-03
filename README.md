# caelis

`caelis` is a clean-slate Agent framework kernel prototype.
It is designed to be extracted as a standalone repository.

## Scope
- Kernel-first architecture.
- Minimal M1: single agent + synchronous tool loop.
- Compile-time plugin registration + provider-based assembly.
- Built-in mandatory `READ` tool with line/token caps.
- Modular system prompt pipeline with auto-seeded templates (`IDENTITY.md`, `AGENTS.md`, `USER.md`).
- Skills metadata auto-discovery (`~/.agents/skills`, `.agents/skills`) and prompt injection.
- Policy hooks (egress/audit/output) with default allow behavior.
- Unified model provider layer with API types: `openai`, `openai_compatible`, `gemini`, `anthropic`, `deepseek`.
- CLI shell and real-model eval runner.
- Tool execution runtime abstraction (`no_sandbox` / `sandbox` with extensible backend type).
- Token-budget based auto compaction (append-only event strategy).
- MCP ToolSet integration (`stdio` / `sse` / `streamable`), assembled via `mcp_tools` provider.

## Quick Start
```bash
make build
make vet
make test
```

Run CLI:
```bash
go run ./cmd/cli \
  -tool-providers workspace_tools,shell_tools,lsp_tools,mcp_tools \
  -policy-providers default_allow \
  -model deepseek/deepseek-chat \
  -permission-mode default \
  -mcp-config ~/.agents/mcp_servers.json \
  -stream=true \
  -thinking-mode=off \
  -compact-watermark=0.7 \
  -context-window=65536 \
  -thinking-budget=1024 \
  -prompt-config-dir "" \
  -skills-dirs "~/.agents/skills,.agents/skills" \
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
  - default: macOS uses `seatbelt`; other platforms use `docker`
  - on macOS, only `seatbelt` is supported in `default` mode
  - built-in: `seatbelt` (macOS `sandbox-exec`)
  - built-in: `docker` (non-macOS default backend; requires local Docker daemon, image defaults to `alpine:3.20`, override via `CAELIS_SANDBOX_DOCKER_IMAGE`)
  - docker network defaults to `bridge`, override via `CAELIS_SANDBOX_DOCKER_NETWORK` (for stricter isolation use `none`)
  - sandbox does not fully mirror host toolchain; for language-specific workflows (go/node/python) use a richer image via `CAELIS_SANDBOX_DOCKER_IMAGE`
- `-mcp-config`: MCP server config JSON path, default `~/.agents/mcp_servers.json` (missing file means MCP disabled)
- `-prompt-config-dir`: override prompt config directory; empty means `~/.{app}/prompts`
- `-credential-store`: credential persistence mode (`auto|file|ephemeral`), default `auto`

Fallback behavior:
- In `default` mode on macOS, runtime only supports `seatbelt`; when unavailable it falls back to `host+approval`.
- In `default` mode on other platforms, runtime tries: `docker -> host+approval`.
- If sandbox backend is unavailable at startup (for example `sandbox-exec` missing on macOS or Docker daemon unavailable on Linux), CLI falls back to `host+approval` and prints a warning.
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
- BASH tool has a default timeout of `90s` for host and sandbox execution.
- BASH tool has a default no-output timeout of `45s` (idle timeout) to stop interactive/long-running commands that stop producing output.
- Optional tool args `timeout_ms` and `idle_timeout_ms` can override per call.
- For commands that may start interactive loops (`go run ...`, `npm start`, etc), prefer one-shot/non-interactive flags and set explicit `timeout_ms`.
- Host/sandbox execution enforces non-interactive environment defaults (`CI=1`, `TERM=dumb`, `GIT_TERMINAL_PROMPT=0`, `PAGER=cat`, `NO_COLOR=1`).

System prompt pipeline order (high -> low):
1. `~/.{app}/prompts/IDENTITY.md`
2. `~/.{app}/prompts/AGENTS.md`
3. `{workspace}/AGENTS.md` (optional)
4. LSP routing policy (conditional, auto-enabled when `LSP_*` tools are available)
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

Interactive slash commands:
- `/help`: show command help
- `/version`: show version info
- `/status`: show current model/thinking/stream/execution status
- `/new`: start a fresh conversation session
- `/permission [default|full_control]`: show or switch permission mode
- `/sandbox [<type>]`: show or switch sandbox backend type
- `/models`: list available model aliases
- `/model <alias>`: switch model
- `/thinking <auto|on|off> [budget]`: switch thinking mode
- `/effort <low|medium|high|off>`: set reasoning effort
- `/stream <on|off>`: switch stream mode
- `/reasoning <on|off>`: toggle reasoning content rendering
- `/tools`: show current assembled tool list
- `/compact [note]`: trigger one manual compaction
- `/exit`: quit

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
- LSP tools are injected by CLI plugin provider `lsp_tools` (no manual activation step).
- CLI auto-detects workspace language and injects one language server toolset by default when a supported server exists locally.
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
- Current target release: `v0.0.1` (see `VERSION` and `CHANGELOG.md`).
- Local dry-run package:
```bash
make release-dry-run
```
- CI release is triggered by git tag push like `v0.0.1`.
