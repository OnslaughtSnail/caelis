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
cp .env.example .env
make build
make vet
make test
```

Run CLI:
```bash
go run ./cmd/cli \
  -tool-providers local_tools,workspace_tools,shell_tools,lsp_activation,mcp_tools \
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
- `-permission-mode`: `default|full_control`
  - `full_control`: run commands on host directly, no approval required
  - `default`: run commands in sandbox by default; escalated host command requires approval
- `-sandbox-type`: sandbox backend type (when `-permission-mode=default`, pluggable by runtime registry)
  - default: macOS uses `seatbelt`; other platforms use `docker`
  - built-in: `seatbelt` (macOS `sandbox-exec`)
  - built-in: `docker` (requires local Docker daemon; image defaults to `alpine:3.20`, override via `CAELIS_SANDBOX_DOCKER_IMAGE`)
  - docker network defaults to `bridge`, override via `CAELIS_SANDBOX_DOCKER_NETWORK` (for stricter isolation use `none`)
  - sandbox does not fully mirror host toolchain; for language-specific workflows (go/node/python) use a richer image via `CAELIS_SANDBOX_DOCKER_IMAGE`
- `-safe-commands`: override sandbox safe command set (comma-separated)
  - default: `pwd,ls,find,cat,head,tail,wc,echo,grep,sed,awk,rg`
  - note: this list is currently not used for approval routing; host approval in `default` is only triggered by fallback or explicit `sandbox_permissions=require_escalated`
- `-mcp-config`: MCP server config JSON path, default `~/.agents/mcp_servers.json` (missing file means MCP disabled)
- `-prompt-config-dir`: override prompt config directory; empty means `~/.{app}/prompts`
- `-credential-store`: credential persistence mode (`auto|file|ephemeral`), default `auto`

Fallback behavior:
- In `default` mode on macOS, runtime tries sandbox backends in order: `seatbelt -> docker -> host+approval`.
- In `default` mode on other platforms, runtime tries: `docker -> host+approval`.
- If all sandbox backends are unavailable at startup (for example `sandbox-exec` missing and Docker daemon unavailable), CLI falls back to `host+approval` and prints a warning.
- In non-interactive runs without approver context, escalated commands return `ApprovalRequiredError` with a hint to use interactive approval or `-permission-mode full_control`.
- If a command is routed to sandbox but fails with "command not found" (`exit code 127`), BASH asks for approval and retries on host.

Approval UX in interactive CLI:
- Safe read-style commands are default-approved for host escalation in current session (`cat`, `head`, `grep`, and other `safe-commands`, plus `git status`).
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
4. LSP routing policy (conditional, auto-enabled when `LSP_ACTIVATE` is available)
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

CLI runtime preferences (`stream`, `thinking-mode`, `thinking-budget`, `reasoning-effort`, `reasoning display`, `permission-mode`, `sandbox-type`) are persisted in app config and reused on next start.

LSP behavior:
- `LSP_ACTIVATE` is still available.
- For Go workspaces (`go.mod` exists or root has `*.go`), CLI auto-activates Go LSP tools at run start to reduce missed tool-loading.

Manual compaction command in interactive mode:
```text
/compact optional note
```

Run lightweight eval:
```bash
go run ./eval/cmd -suite light
```

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
- `/connect` defaults to `api_key_env` (e.g. `DEEPSEEK_API_KEY`, `GEMINI_API_KEY`).
- `/connect` stores API key/token in `~/.{app}/{app}_credentials.json` by default (`-credential-store=auto`), with owner-only permissions (`0700` dir, `0600` file) and atomic writes.
- Provider config (`~/.{app}/{app}_config.json`) persists only non-secret auth metadata (`token_env`, `credential_ref`), no inline token by default.
- Existing inline tokens in provider config are auto-migrated to credential store on startup.

## Release
- Current target release: `v0.0.1` (see `VERSION` and `CHANGELOG.md`).
- Local dry-run package:
```bash
make release-dry-run
```
- CI release is triggered by git tag push like `v0.0.1`.
