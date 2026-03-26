# Changelog

## Unreleased

## v0.0.29 - 2026-03-26

### ACP And Headless Compatibility Fixes
- Fixed headless single-shot `-p` runs to use streaming model responses again, avoiding provider failures in non-interactive mode for prompts that require streaming.
- Fixed ACP `session/new` startup sequencing so the server returns the JSON-RPC response before pushing initial session snapshot notifications, improving compatibility with strict headless ACP clients.
- Fixed empty ACP plan updates to serialize as `[]` instead of `null`, which restores compatibility with `acpx` text rendering for sessions that have no active plan entries.

### Session Catalog Robustness
- Treated missing `acp_remote` catalog roots as an empty backfill case instead of surfacing a startup warning before any ACP-backed sessions exist.
- Added regression coverage around post-response ACP notifications, empty-plan encoding, and missing-scope catalog backfill behavior.

## v0.0.28 - 2026-03-26

### TUI Transcript And Panel Refinements
- Reworked the Bubble Tea welcome card and panel/header rendering around shared transcript primitives, with richer participant labels, tighter panel shells, and more consistent prompt/help styling.
- Improved subagent and participant transcript rendering so superseded reasoning/completion noise is hidden without dropping unsuperseded diagnostic reasoning on failed or approval-paused turns.
- Added Markdown table-aware narrative rendering while preserving ordinary pipe-delimited prose and shell pipelines as plain text.

### Delegation, Task Idle Detection, And Safe Follow-up Writes
- Projected idle-timed-out child task results to the spawn preview UI as explicit `timed_out` terminal states and prevented repeated `TASK status` polling from extending the subagent idle timeout window.
- Applied a default idle-timeout fallback to ACP-backed child sessions, including `self` delegates, so stalled child runs cannot hang indefinitely without surfaced failure state.
- Extended read-before-write policy state with safe-write evidence so follow-up `WRITE` / `PATCH` operations against just-created or previously empty files can proceed without a redundant `READ`.

### Execenv Polish And Release Metadata
- Fixed minor `kernel/execenv` cleanup and test issues surfaced by editor diagnostics, including dead-condition cleanup, Linux-only helper constant scoping, and a clearer platform switch in runtime tests.
- Bumped the repository release metadata to `v0.0.28` in `CHANGELOG.md`, `README.md`, and `VERSION` for the tagged release.

## v0.0.27 - 2026-03-25

### Release Prep
- Bumped the repository release metadata to `v0.0.27` in `CHANGELOG.md`, `README.md`, and `VERSION` to prepare the next tagged release.

## v0.0.26 - 2026-03-24

### External ACP Agents, Resume, And Delegation
- Added configurable ACP-backed agent presets and dynamic slash routing so configured agents such as `/codex`, `/gemini`, and `/claude` can run as external participants directly from the console.
- Added persisted external participant metadata plus resume-time ACP stream reattachment, keeping mirrored participant turns, tool activity, and approval state visible after reopening a session.
- Tightened ACP subagent recovery so `SPAWN` children keep durable lifecycle/session references for task tracking, including persisted failure state and fallback inspection from stored runtime/session data after restart.

### Runtime, ACP, And Task Tracking
- Reworked ACP/self subagent execution around reusable child sessions, child working-directory tracking, richer permission-option handling, and preserved delegation lineage when reusing existing child sessions.
- Improved task reconciliation, subagent inspection, and resumed-session replay so live child sessions remain controllable across process restarts instead of falling back to stale interrupted state.
- Added new ACP client/server coverage around connection lifecycle, session load/new semantics, permission negotiation, and resumed child-session projections.

### TUI Streaming, Panels, And Model Plumbing
- Expanded Bubble Tea streaming/rendering with smoother raw-delta playback, inline panel collapse animation, richer participant/subagent status blocks, and better replay behavior for resumed child sessions and tool streams.
- Improved spawn/subagent previews so only live child sessions trigger automatic ACP reload on resume, avoiding duplicate replay for completed historical delegates while keeping canonical transcript rendering intact.
- Refreshed model/provider request plumbing and multimodal message-part handling across provider adapters, runtime traces, and prompt assembly to support the updated ACP/session flow safely.

## v0.0.25 - 2026-03-17

### Internal Architecture Refactor
- Moved CLI-owned assembly, prompting, and skills metadata helpers out of legacy kernel packages into `internal/app/assembly`, `internal/app/prompting`, and `internal/app/skills`.
- Moved session projection helpers into `kernel/session` and normalized tool capability metadata under `kernel/tool/capability`, reducing redundant top-level kernel packages.
- Split large runtime, ACP server, and TUI rendering files into focused modules to make replay, lifecycle, task control, prompt parsing, and rendering logic easier to maintain without changing the exposed behavior.

### Runtime, ACP, And UI Hardening
- Preserved runtime/ACP behavior while refactoring by adding and updating regression coverage around session projections, persisted task rules, ACP sanitization, runtime replay, and permission/session-mode switching.
- Improved TUI activity/result pairing so batched tool updates produce fewer orphaned entries and clearer merged activity state.
- Added explicit unknown-tool failures in the agent loop so unsupported tool calls surface a direct error instead of falling through policy and authorization paths.

### Documentation
- Refreshed the repository documentation to reflect the current application structure, runtime model, supported entry points, and release version.

## v0.0.24 - 2026-03-16

### TUI Rendering, Streaming & Mutation Summaries
- Improved Bubble Tea streaming rendering so Markdown/plain-text output stays stable across incremental deltas, preserving short tokens and avoiding unwanted indentation drift.
- Corrected `PATCH` / `WRITE` mutation added/removed line counts, including better replay-time handling and richer preview accounting for insert-only edits and legacy write responses.
- Suppressed misleading rich diffs and summaries for no-op file writes so unchanged mutations no longer render phantom removals or noisy replay output.

### Ephemeral `/btw` Side Questions
- Added `/btw <question>` as an ephemeral side-question flow that reuses current session context without appending the exchange to persisted conversation history.
- Added dedicated overlay event typing/visibility handling across runtime, session projection, ACP, and TUI layers so overlay turns stay non-persistent and machine-classifiable.
- Blocked tool use during `/btw` runs and added dedicated TUI overlay rendering/interaction coverage for side-question answers, errors, and dismissal behavior.

### Planning & Runtime Event Semantics
- Normalized runtime event categorization around stable `meta.event_type` values, with backward-compatible inference for legacy metadata.
- Tightened `PLAN` tool guidance/response semantics so plan updates can stay lightweight in the transcript while still updating structured session state.

## v0.0.23 - 2026-03-15

### Runtime, Prompt Flow & Session Streaming
- Reworked runtime execution around a durable `Runner` abstraction so CLI, TUI, ACP, and subagents can keep a live run open, replay persisted events, and submit follow-up prompts into the active session without starting a second run.
- Added inline follow-up submission support for the Bubble Tea console and ACP prompt handling, including queued-message rendering/ack flows in the TUI and runner reuse on the server side.
- Added loop-detection coverage plus replay/cursor plumbing for long event streams so resumed consumers can recover dropped durable pages more reliably.

### Policy, Retry & Delegation Hardening
- Persisted `READ` evidence into session state so read-before-write policy decisions survive stale in-memory state and can be backfilled from stored events.
- Improved model retry handling by surfacing richer upstream provider error details and skipping automatic retries for non-retryable HTTP 4xx responses.
- Hardened detached subagent failure handling so panic/startup failures are recorded in lifecycle state instead of silently disappearing.

### Prompt Assembly & Environment Context
- Added a structured `<environment_context>` prompt fragment that injects `cwd`, `shell`, `current_date`, and `timezone` into the assembled system prompt for both CLI/TUI and ACP sessions.
- Kept workspace-aware prompt assembly centralized in the CLI-owned prompt pipeline, with ACP continuing to feed session-specific `cwd` from the protocol layer.
- Refreshed prompt-assembly regression coverage to lock in fragment ordering and the new environment-context fields.

## v0.0.22 - 2026-03-14

### Planning, Slash Commands & Session Recovery
- Added a new core `PLAN` tool that persists structured execution plans in session state, surfaced in ACP updates and rendered in the TUI plan drawer during active turns.
- Added ACP `available_commands_update` + `plan` session updates, plus built-in `/help`, `/status`, and `/compact` slash command handling for ACP sessions.
- Fixed resumed-session task reconciliation so runtime-level reattachment for async `BASH` tasks works reliably and preserves running task continuity.
- Aligned advertised ACP slash-command behavior with runtime handling to avoid command-list/protocol mismatches.
- Expanded regression coverage across ACP protocol/session updates, plan rendering, authorization metadata propagation, and resumed-session reconciliation paths.

## v0.0.21 - 2026-03-13

### Release Follow-up
- Fixed CI `go vet` failures in TUI theme tests by switching Bubble Tea background-color test messages to keyed struct literals.
- Hardened async host-runner coverage to avoid timing-sensitive output assertions that flaked in GoReleaser's `go test ./...` hook.
- Refreshed `README.md` so the documented CLI flags, slash commands, runtime behavior, and current release version match the shipped code.

## v0.0.20 - 2026-03-13

### Console, Theme & Runtime UX
- Added terminal-background aware light/dark theme resolution for the Bubble Tea UI, including full re-theming of existing transcript, markdown, diff, and tool-output blocks when auto theme is enabled.
- Simplified the interactive command surface by removing legacy `/permission`, `/tools`, and `/skills` slash commands while surfacing platform-aware sandbox choices with clearer experimental labels.
- Updated runtime and status messaging so sandbox labels, fallback hints, and README guidance reflect the current default/experimental backend behavior more accurately.

### Gitignore-Aware Workspace Discovery
- Added a shared gitignore matcher and applied it across filesystem tools, workspace file mention completion, and LSP language detection so ignored/generated content is excluded consistently.
- Added regression coverage for root and nested `.gitignore` handling in filesystem search, input reference completion, and language detection.

### ACP, Sandbox & Policy Handling
- Kept ACP default-mode `BASH` sandbox execution on the real sandbox runner instead of the ACP terminal bridge, so reported sandbox routes match actual enforcement and sandbox policy continues to apply.
- Removed `.codex` from the default workspace-write read-only subpath list, leaving `.git` as the built-in protected path in the derived sandbox policy.
- Expanded ACP/runtime coverage around terminal capability handling and async sandbox preservation under session-scoped runtimes.

## v0.0.19 - 2026-03-13

### ACP, Session Config & Model Catalog
- Reworked ACP session config flows with richer capability reporting, image-aware prompt support, and improved session/runtime handling across model selection and prompt submission.
- Refreshed the bundled model catalog snapshot and provider capability overlays, plus updated provider discovery/factory wiring for newer catalog metadata and model capability handling.
- Expanded ACP and provider test coverage around protocol fields, permission/runtime plumbing, prompt ordering, and multimodal request construction.

### TUI, Console & Multimodal Input
- Migrated the Bubble Tea console to the v2 stack with a split TUI app structure, richer tool-output panels, improved composer rendering, and updated key/mouse handling.
- Added inline attachment tokens in the composer, history/queue preservation for attachments, attachment-only sends, and safer multimodal prompt assembly so text, images, and session-mode injection stay aligned.
- Added live bash task watches, improved resumed-session rendering for interleaved image/text turns, and broadened console/TUI regression coverage for stream ordering and input flows.

### Image, Clipboard & Runtime Handling
- Expanded clipboard image extraction on macOS and Linux/WSL, including broader MIME handling and more consistent image-loading behavior across headless and interactive entry points.
- Normalized TIFF handling through the shared image pipeline so resize/encode behavior is consistent regardless of whether images come from files, clipboard paste, or cached content parts.
- Tightened runtime task/delegate reporting with better wait metadata and lifecycle fallback handling.

## v0.0.18 - 2026-03-11

### CLI, TUI & Interaction
- Refreshed the Bubble Tea console with updated theming, markdown/code styling, prompt overlays, palette animation, viewport scrollbars, and improved multi-line input rendering.
- Added delegated child-session previews in the TUI plus friendlier `BASH` / `DELEGATE` / `TASK` summaries, approval prompts, and task wait messaging.
- Expanded console and TUI coverage for stream ordering, approvals, task summaries, palette/input behavior, and line-editor interactions.

### Runtime, Delegation & Session Streaming
- Added raw `sessionstream` plumbing so delegated child runs can project live session events back into the parent UI with preserved lineage metadata.
- Reworked subagent and task-manager handling around attached/detached child contexts, delegate inspection, persisted task snapshots, and session-backed delegate previews.
- Added delegate metadata helpers plus improved runtime/test coverage for child lineage, session streaming, and task lifecycle reporting.

### Safety, Execution & Task Semantics
- Introduced centralized dangerous-command detection shared by session mode and command-policy preflight checks, including wrapper-aware handling for commands invoked through `env`, `sudo`, and `time`.
- Tightened shell/task execution semantics around bounded default waits for `BASH`, `DELEGATE`, and `TASK`, and aligned `TASK` wording around returning refreshed task snapshots.
- Added text sanitization and command-safety test coverage to lock in the new preflight checks and CLI rendering behavior.

## v0.0.17 - 2026-03-10

### CLI, TUI & Model UX
- Reworked `/model` slash UX with subcommand-aware completion for `list` / `use` / `rm` / `edit`, ghost hints, auto-open pickers, and duplicate-endpoint disambiguation.
- Added model removal cleanup for saved provider credentials and improved multi-select prompt flows with custom-choice passthrough and safer interruption handling.
- Improved console and TUI rendering for `TASK` / `BASH` results with friendlier summaries, clearer full-access status styling, and cleanup of partial assistant output after interrupted runs.

### Runtime, Sessions & ACP
- Synced session mode with runtime permission mode, added swappable runtime views for CLI tools/providers, and limited hidden prompt injection to plan mode so runtime defaults no longer leak into assembled prompts.
- Added atomic session-state update support across indexed, in-memory, and file-backed stores so concurrent runtime and ACP updates preserve unrelated state.
- Improved ACP runtime/session resources with mode-aware full-access bridging, client filesystem preservation under ACP full access, and buffered/coalesced assistant partial-content delivery.

### Shell, Sandbox & Task Execution
- Added async session support to `bwrap`, `landlock`, and `seatbelt` sandboxes, while making full-control runtimes consistently collapse sandbox execution back onto the host runner.
- Updated `BASH` and `DELEGATE` wait semantics so omitted `yield_time_ms` waits briefly before backgrounding, `0` returns immediately, and `-1` forces synchronous completion.
- Added turn-scoped cleanup for background tasks, persisted final task snapshots across turns, and relaxed duplicate-call suppression for repeated `TASK` polling.

### Model Catalog & Dependencies
- Refreshed the bundled models.dev capability snapshot and provider overlays with broader catalog coverage plus more conservative context-aware fallback max-output defaults.
- Promoted `golang.org/x/sys` to a direct dependency for the updated runtime and session plumbing.

## v0.0.15 - 2026-03-08

### Runtime, Tasks & Delegation
- Added core `DELEGATE` and `TASK` tools with unified async task control for delegated child runs and long-running shell work.
- Added persisted task-ledger recovery so interrupted async tasks can be reconciled without leaving sessions in a broken state.
- Added child-run lineage metadata on delegated session events (`parent_session_id`, `child_session_id`, `parent_tool_call_id`, `delegation_id`).
- Hardened task and subagent failure handling for detached delegate runs, nil-context callers, and interrupted task controllers.

### Shell & Interaction
- Reworked `BASH` around `yield_time_ms`, `task_id`, and explicit `tty=true` PTY sessions for interactive command flows.
- Removed legacy `sandbox_permissions` handling from `BASH` and aligned destructive-command routing with sandbox-first semantics unless explicitly escalated.

### CLI, TUI & Prompt Assembly
- Moved product prompt defaults out of `kernel/promptpipeline`, leaving kernel with a smaller prompt assembler and CLI-owned defaults.
- Made LSP tools opt-in as an experimental CLI feature instead of a default capability.
- Added anchored inline tool-output blocks in the TUI for `BASH` and `DELEGATE`, with filtered delegate previews that avoid leaking nested tool output into the main view.
- Improved approval rendering and MCP web-tool guidance for read-only `search` / `fetch` style integrations.

## v0.0.14 - 2026-03-08

### CLI & TUI
- Added live file-mutation diff previews for `WRITE` and `PATCH`, plus clearer fallback summaries when rich previews are skipped.
- Refined approval prompts with scoped session approvals, clearer command/edit framing, and improved TUI hint/status behavior.
- Improved resumed-session rendering, tool output panels, prompt guidance text, and folded diff presentation for large multi-hunk edits.

### Runtime & Model Handling
- Added conservative context-usage tracking in the console/TUI status bar using runtime-backed estimates and streamed usage metadata.
- Improved model request retry handling with rate-limit aware backoff, clearer retry warnings, and safer handling for interrupted partial streams.
- Added streamed usage support for OpenAI-compatible providers and surfaced model-catalog fallback hints during interactive connect.

### Policy & Filesystem Tools
- Unified `WRITE`/`PATCH` mutation planning and preview generation so workspace-boundary approvals can show scoped path context and mutation previews.
- Expanded workspace-boundary and filesystem mutation coverage for external writes, path scoping, and diff preview generation.

## v0.0.11 - 2026-03-06

### Release Follow-up
- Stabilized async execution tests in `kernel/execenv` by replacing fixed sleeps with bounded polling, avoiding empty-output flakes on slower CI runners.
- Reissued the release after the `v0.0.10` GitHub workflow failed during `go test ./...` in GoReleaser.

## v0.0.10 - 2026-03-06

### Shell & Execution Runtime
- Added async `BASH` sessions with session IDs, incremental output reads, input writes, status checks, termination, and session listing.
- Added streamed shell output plumbing so live `BASH` output can be rendered directly in the TUI.
- Improved host execution with smarter idle detection, process defaults, session management, ring buffers, and seatbelt profile support.

### Runtime & Session State
- Added `eventview` projections and readonly session views for model/runtime consumption.
- Moved run lifecycle state to persisted session snapshots and improved recovery/rebuild behavior for pending tool calls.
- Added readonly session state access in invocation context and aligned runtime/session stores with snapshot APIs.

### CLI & TUI
- Added inline shell output panels in the Bubble Tea UI with adaptive width, capped preview height, and improved approval prompts.
- Moved model catalog implementation to `internal/cli/modelcatalog` and added a CLI facade for catalog lookups.
- Refined status, connect, model reasoning, and console runtime wiring with broader test coverage.

## v0.0.2 - 2026-03-03

### TUI Interface
- Full Bubble Tea TUI rewrite: streaming output, tool call display, approval UX, reasoning blocks.
- Inline diff/patch viewer (`tuidiff`) for file patch display within TUI.
- TUI theming system (`tuikit/theme`), line-style renderer (`tuikit/linestyle`), and ANSI sanitizer.
- TUI diagnostics view (`tui_diag.go`).
- Headless execution mode (`headless.go`) for non-interactive single-shot runs.

### Model & Reasoning
- Model catalog: static capabilities snapshot + on-demand remote refresh (`internal/cli/modelcatalog`).
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
- Core `DELEGATE_TASK` tool: delegate a focused child run with isolated child session history.
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
