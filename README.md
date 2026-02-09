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
  -exec-mode no_sandbox \
  -bash-strategy strict \
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
- `-exec-mode`: `no_sandbox|sandbox`
- `-sandbox-type`: sandbox backend type (when `-exec-mode=sandbox`, pluggable by runtime registry)
- `-bash-strategy`: `auto|full_access|agent_decided|strict`
- `-bash-allowlist`: override command allowlist (comma-separated)
- `-bash-deny-meta`: deny shell meta characters (`|;&><\`$\\`) in strict/agent-decided checks
- `-mcp-config`: MCP server config JSON path, default `~/.agents/mcp_servers.json` (missing file means MCP disabled)
- `-prompt-config-dir`: override prompt config directory; empty means `~/.{app}/prompts`
- `-credential-store`: credential persistence mode (`auto|file|ephemeral`), default `auto`

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
- `/models`: list available model aliases
- `/model <alias>`: switch model
- `/thinking <auto|on|off> [budget]`: switch thinking mode
- `/effort <low|medium|high|off>`: set reasoning effort
- `/stream <on|off>`: switch stream mode
- `/reasoning <on|off>`: toggle reasoning content rendering
- `/tools`: show current assembled tool list
- `/compact [note]`: trigger one manual compaction
- `/exit`: quit

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
