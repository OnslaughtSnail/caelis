# caelis

`caelis` is a terminal-first agent runtime with a Bubble Tea console, an ACP server mode, persistent sessions, sandbox-aware command execution, and resumable task and delegation flows.

## What It Does

- Runs an interactive TUI coding agent in your terminal.
- Supports headless single-shot execution for scripts and automation.
- Exposes the same runtime over ACP for external clients.
- Persists sessions, plans, tasks, and lifecycle state so interrupted work can be resumed safely.
- Supports ACP-backed external agents that can be invoked as slash commands inside the console.
- Routes shell execution through approval-aware sandboxed `default` mode or host `full_control`.
- Ships built-in workspace tools, shell tools, and optional CLI LSP tools.
- Assembles prompts from built-in runtime context, `AGENTS.md`, workspace metadata, and discovered local skills.

## Current Layout

The codebase is organized around a small runtime kernel plus CLI-owned application wiring:

- `cmd/cli`: console mode, ACP mode, config/session wiring, prompt assembly inputs.
- `internal/app/assembly`: provider registration and tool/policy assembly.
- `internal/app/prompting`: prompt fragment assembly.
- `internal/app/skills`: skill metadata discovery and prompt rendering.
- `internal/acp`: ACP protocol server, session state handling, prompt parsing, and streaming updates.
- `kernel/runtime`: run loop, replay, lifecycle, compaction, tasks, delegation, and persistence.
- `kernel/session`: session/event types, visibility rules, projections, and context windows.
- `kernel/tool`: built-in tool implementations and tool capability metadata.

## Build

Requires Go `1.25.1`.

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
  -tool-providers workspace_tools,shell_tools \
  -policy-providers default_allow \
  -permission-mode default
```

Headless single-shot run:

```bash
go run ./cmd/cli console \
  -model openai-compatible/glm-5 \
  -p "Summarize the repository layout."
```

ACP server:

```bash
go run ./cmd/cli acp \
  -model openai-compatible/glm-5 \
  -tool-providers workspace_tools,shell_tools \
  -policy-providers default_allow \
  -permission-mode default
```

If no model is configured yet, start the console and run `/connect`.

## Runtime And Permissions

`caelis` has two execution modes:

- `-permission-mode default`: commands run in a sandbox when available; host escalation requires approval.
- `-permission-mode full_control`: commands run directly on the host with no approval gate.

Sandbox backend selection is controlled by `-sandbox-type` in `default` mode:

- macOS: `seatbelt`
- Linux: `bwrap`, then `landlock` fallback when available

If no supported sandbox backend is available, `caelis` falls back to host execution with approval and prints a warning.

The console also exposes session modes:

- `default`: normal coding mode with execution enabled.
- `plan`: planning-first mode that focuses on analysis before edits.
- `full_access`: maps to `full_control` execution while keeping the session/UI state explicit.

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

## Prompt Assembly And Skills

Prompt assembly combines:

1. Built-in identity and runtime instructions
2. Global `AGENTS.md`
3. Workspace `AGENTS.md`
4. Session and runtime prompt fragments
5. Discovered local skill metadata

Skills are discovered from local `SKILL.md` files and rendered as metadata into the final system prompt. The CLI flag `-skills-dirs` is retained for compatibility, but current skill loading uses the standard local skills location under `~/.agents/skills`.

## Tools

The default console and ACP configuration uses:

- `workspace_tools`
- `shell_tools`

Optional:

- `lsp_tools` via `-experimental-lsp`

Built-in tool families include file reads, writes, search, shell execution, planning, task control, and delegation.

User-facing MCP tool loading is no longer supported in the CLI runtime. Older ACP protocol fields still exist for compatibility with session and client payloads, but the shipped console and ACP entry points no longer expose `mcp_tools` or `-mcp-config`.

## Release

- Current release: `v0.0.34`
- Version source: git tag at release time, with `VERSION` used as the local fallback
- Changelog: `CHANGELOG.md`

Local dry run:

```bash
make release-dry-run
```

CI release is triggered by pushing a version tag such as `v0.0.34`.

## npm Package

The npm package lives under `npm/` and publishes as `@onslaughtsnail/caelis`.

Platform-specific binaries publish as internal npm packages and are pulled in through `optionalDependencies`, so installs stay on the npm registry path instead of downloading GitHub Release assets during `postinstall`.

```bash
npm i -g @onslaughtsnail/caelis
```
