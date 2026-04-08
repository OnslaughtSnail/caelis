# ACP TUI Rendering Framework

## Goal

TUI rendering should follow ACP protocol events rather than the origin of the
agent session. Internal delegated agents, self ACP sessions, and external ACP
agents should all project into one rendering timeline.

## Canonical ACP Event Classes

Render the transcript from protocol-shaped events:

- `agent_message_chunk`
- `agent_thought_chunk`
- `tool_call`
- `tool_call_update`
- `plan`
- permission / waiting-approval state
- terminal turn state (`completed`, `failed`, `interrupted`, `timed_out`)

## Rendering Rules

### 1. Session header is session metadata, not source-specific chrome

Header should only encode:

- actor / agent label
- session state
- elapsed time

It must not imply a separate product surface for external ACP agents.

### 2. Timeline body preserves ACP event order

The body should reflect the prompt-turn lifecycle:

- plan updates
- reasoning chunks
- assistant chunks
- tool call creation / updates
- terminal state

Do not collapse the tool trace into a synthetic summary by default. The ACP
tool timeline is part of the product contract.

When a turn contains multiple ACP content classes, the body should use subtle
section dividers such as `plan`, `response`, and `activity` to reduce visual
noise without hiding tool chronology. Single-section turns should stay quiet
and avoid redundant labels.

### 3. Plan updates replace plan state semantically

ACP plans are full replacements, not append-only diffs. The renderer should
show the latest plan state while preserving chronological visibility when that
is useful for the local session view.

### 4. Tool rendering is protocol-driven

Tool UI should derive from ACP fields:

- `kind` determines icon / visual category
- `title` is the primary label
- `status` determines tone
- `content`, `rawInput`, `rawOutput`, and `locations` feed detail rows

For readability, one tool call may render as a grouped lifecycle paragraph:

- keep the start row visible as the canonical anchor
- show streaming preview or final result as indented detail rows beneath it
- avoid repeating the tool name in every result row when the result belongs to
  the same call
- preserve chronological ordering across different tool calls
- apply a default preview budget for long args and long results, with a
  truncation hint such as `… N more lines`, instead of always expanding the
  full payload inline

### 5. Source adapters feed one transcript renderer

All ACP-producing paths should normalize into one UI event model before
rendering. The TUI should not maintain separate presentation logic for:

- external ACP participants
- resumed ACP sessions
- self ACP delegated sessions

## Current Repository Direction

The first unification step is complete:

- `ParticipantTurnBlock` and `SubagentPanelBlock` now use the same ACP
  transcript renderer.
- Shared rendering lives in `internal/cli/tuiapp/acp_transcript.go`.
- Mixed ACP transcripts now render with sectioned body grouping driven by
  projected event classes instead of source-specific formatting.

## Recommended Next Steps

1. Replace remaining source-specific bridge code with one canonical ACP turn
   projection layer.
2. Preserve richer tool-call detail from `content` and `locations`, not just
   summarized `Args` and `Output`.
3. Add session-info and mode/config updates to the same transcript grammar.
4. Add snapshot / replay tests that feed raw ACP `session/update` payloads
   directly into the renderer contract.
