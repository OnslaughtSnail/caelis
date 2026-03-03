# Changelog

## v0.0.2 - 2026-03-03

### TUI Interface
- Full Bubble Tea TUI rewrite: streaming output, tool call display, approval UX, reasoning blocks.
- Inline diff/patch viewer (`tuidiff`) for file patch display within TUI.
- TUI theming system (`tuikit/theme`), line-style renderer (`tuikit/linestyle`), and ANSI sanitizer.
- TUI diagnostics view (`tui_diag.go`).
- Headless execution mode (`headless.go`) for non-interactive single-shot runs.

### Model & Reasoning
- Model catalog: static capabilities snapshot + on-demand remote refresh (`model_catalog.go`, `model_catalog_remote.go`).
- Model reasoning display mode: per-model reasoning option set (`model_reasoning.go`) supporting `off/on/low/medium/high/very_high`.
- `normalizeReasoningSelection` helper for cleaner reasoning flag parsing.
- `/reasoning <on|off>` slash command for toggling reasoning content rendering.
- `/model <alias> [reasoning]` extended to accept inline reasoning level.

### Input & Attachments
- Input refs (`input_refs.go`): `@file` and `@image` reference parsing in user prompts.
- Image utilities (`imageutil`): clipboard image capture (Darwin / Linux), resize, LRU content cache.

### CLI & Sessions
- UI mode abstraction (`ui.go`, `ui_mode.go`): `auto|tui|line` selection logic unified.
- `/fork` slash command: fork current conversation into a new named session.
- `/quit` alias for `/exit`.
- `markdown_render.go`: standalone Markdown-to-ANSI renderer for line-editor mode.
- Session index tests and coverage expansion.
- Stream ordering guarantee tests (`console_stream_order_test.go`).
- Model switch tests (`console_model_test.go`).

### Kernel & Policy
- Tool-level authorization baseline: per-tool allow/deny annotations, policy evaluation pre-execution.
- Workspace boundary policy (`workspace_boundary.go`): restrict filesystem tools to within project root.
- `tool_args.go` / `tool_args_test.go`: typed tool argument schema and validation.
- `session_events.go`: typed session event helpers separate from runtime core.
- `context_window.go`: session-level context window accounting.
- `run_state.go`: richer run state tracking (cancel, interrupt, finish signals).

### LSP
- LSP adapter, broker, and client packages moved to `internal/cli/` (decoupled from kernel).
- LSP tools provider refactored as standalone CLI plugin (`lsp_tools_provider.go`).

### Misc
- `envload` package extracted to `internal/envload/`.
- `version` package tests added.
- Eval provider factory and runner improvements.
- Various test coverage additions across `tuiapp`, `tuikit`, `runtime`, `session`, `execenv`, `policy`.

---

## v0.0.1 - 2026-02-09

- Initial `caelis` kernel + CLI release candidate.
- Unified model provider layer (`openai`, `openai_compatible`, `gemini`, `anthropic`, `deepseek`).
- Built-in core tools (`READ`) and workspace/shell tools with execution runtime abstraction.
- MCP ToolSet integration via `~/.agents/mcp_servers.json` (`mcpServers` schema).
- Session persistence and workspace-isolated session index (SQLite).
- Context compaction with watermark strategy and manual `/compact`.
- Skills metadata discovery and prompt injection.
- Real-model eval runner with CI light gate and nightly suite.
- CLI interactive commands: model switching, connect, sessions, compaction, status, tool display.
