# caelis

`caelis` is a terminal-first agent runtime with a Bubble Tea console, an ACP server mode, persistent sessions, sandbox-aware command execution, MCP integration, and resumable task/delegation flows.

## What It Does

- Runs an interactive TUI agent in your terminal.
- Supports headless single-shot execution for scripted use.
- Exposes the same runtime over ACP for external clients.
- Persists sessions, plans, tasks, and lifecycle state so runs can be resumed safely.
- Supports ACP-backed external agents that can be invoked as slash commands inside the console.
- Routes shell execution through `default` approval mode or `full_control`.
- Supports built-in workspace tools, shell tools, MCP tools, and optional CLI LSP tools.
- Assembles prompts from built-in identity/runtime context, `AGENTS.md`, and discovered skills metadata.

## Current Layout

The codebase is organized around a small runtime kernel plus CLI-owned application wiring:

- `cmd/cli`: console mode, ACP mode, config/session wiring, prompt assembly inputs.
- `internal/app/assembly`: plugin/provider assembly for tools and policies.
- `internal/app/prompting`: prompt fragment assembly.
- `internal/app/skills`: skill metadata discovery and prompt rendering.
- `internal/acp`: ACP protocol server, session state handling, prompt parsing, streaming updates.
- `kernel/runtime`: run loop, replay, lifecycle, compaction, tasks, delegation, persistence.
- `kernel/session`: session/event types, visibility rules, projections, context windows.
- `kernel/tool/capability`: normalized tool capability metadata used by policies.

## Build

```bash
make build
make vet
make test
```

Show version:

```bash
go run ./cmd/cli -version
```

## Quick Start

Interactive console:

```bash
go run ./cmd/cli console \
  -ui=tui \
  -model openai-compatible/glm-5 \
  -tool-providers workspace_tools,shell_tools,mcp_tools \
  -policy-providers default_allow \
  -permission-mode default \
  -mcp-config ~/.agents/mcp_servers.json
```

Headless single-shot run:

```bash
go run ./cmd/cli console \
  -model openai-compatible/glm-5 \
  -input "Summarize the repository layout."
```

ACP server:

```bash
go run ./cmd/cli acp \
  -model openai-compatible/glm-5 \
  -tool-providers workspace_tools,shell_tools,mcp_tools \
  -policy-providers default_allow \
  -permission-mode default
```

If you have not configured a model yet, start the console and run `/connect`.

## Runtime Model

`caelis` has two execution modes:

- `-permission-mode default`: commands run in a sandbox when available; host escalation requires approval.
- `-permission-mode full_control`: commands run directly on the host with no approval gate.

Sandbox backend selection is controlled by `-sandbox-type` in `default` mode:

- macOS: `seatbelt`
- Linux: `bwrap`, then `landlock` fallback when available

If no supported sandbox backend is available, `caelis` falls back to host execution with approval and prints a warning.

## Sessions And Interaction

Interactive console sessions are persisted under `~/.caelis/sessions` by default. The console starts a new session unless you pass `-session`, and you can switch or recover work with slash commands.

Current interactive slash commands:

- `/help`
- `/agent list | add <builtin> | rm <name>`
- `/btw <question>`
- `/version`
- `/exit`
- `/quit`
- `/new`
- `/fork`
- `/compact [note]`
- `/status`
- `/sandbox [auto|<type>]`
- `/model use <alias> [reasoning]`
- `/model del [alias ...]`
- `/connect`
- `/resume [session-id]`

`/btw` runs an ephemeral side-question turn against the current context without persisting that exchange into conversation history.

ACP agent presets can be managed with `/agent`. Once configured, ACP agent IDs are exposed as dynamic slash commands, so adding `codex`, `gemini`, or `claude` enables `/codex ...`, `/gemini ...`, or `/claude ...` turns in the console. These run as external participant sessions rather than replacing the main conversation agent.

## Prompt And Skills

Prompt assembly combines:

1. Built-in identity/runtime instructions
2. Global `AGENTS.md`
3. Workspace `AGENTS.md`
4. Session/runtime prompt fragments
5. Skill metadata discovered from configured skill directories

Skills are discovered from local `SKILL.md` files and rendered as metadata into the final system prompt. The current skill discovery and prompt assembly pipeline lives in `internal/app/skills` and `internal/app/prompting`.

## Tools

The default console/ACP configuration uses:

- `workspace_tools`
- `shell_tools`
- `mcp_tools`

Optional:

- `lsp_tools` via `-experimental-lsp`

Built-in tool families include file reads/writes/search, shell execution, task control, delegation, and planning. MCP servers are configured through `~/.agents/mcp_servers.json` by default.

Example MCP config:

```json
{
  "cache_ttl_seconds": 60,
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]
    },
    "browser": {
      "transport": "streamable",
      "url": "http://127.0.0.1:8787/mcp"
    }
  }
}
```

## Release

- Current release: `v0.0.27`
- Version source: git tag at release time, with `VERSION` used as the local fallback
- Changelog: `CHANGELOG.md`

Local dry run:

```bash
make release-dry-run
```

CI release is triggered by pushing a version tag such as `v0.0.27`.

## npm Package

The npm package lives under `npm/` and publishes as `@onslaughtsnail/caelis`.

```bash
npm i -g @onslaughtsnail/caelis
```
