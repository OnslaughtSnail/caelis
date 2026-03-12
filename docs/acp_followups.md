# ACP Follow-ups

This document tracks ACP work that is still worth doing after the current
stdio-first baseline.

## Implemented in the current baseline

- `initialize`
- `authenticate`
  - Optional, lightweight local handshake only
  - Best suited for local `stdio` flows driven by `acpx`
  - Can validate a client-injected credential against a separate env var when
    `caelis acp` is started with `-auth-method-id` and `-auth-token-env`
- `session/new`
- `session/list`
- `session/load`
- `session/set_mode`
- `session/set_config_option`
- `session/prompt`
- `session/cancel`
- `session/update`
- `session/request_permission`
- ACP client file bridge for `READ` / `WRITE` / `PATCH`
- ACP client terminal bridge for sync and async `BASH` / `TASK wait`

## Current boundaries

- Transport is `stdio` only.
- A single ACP connection is tied to one workspace and one process.
- `authenticate` is intentionally thin. It is a protocol gate for local clients,
  not a strong remote security boundary.
- Session mode/config changes are persisted in session state and applied to
  later prompts, but they are not intended to reconfigure an already-running
  model turn.

## Worth doing next

### 1. Interactive terminal input

`terminal/create`, output polling, wait, and release are implemented, but
interactive stdin writes are still missing. This limits ACP-driven REPLs,
prompts that expect follow-up input, and long-running shell workflows that need
incremental interaction.

Recommended scope:

- Add ACP terminal input plumbing in `internal/acp/runtime.go`
- Map it onto `AsyncCommandRunner.WriteInput`
- Add end-to-end `acpx` coverage with an interactive command

### 2. Richer terminal session state

The current async terminal bridge is good enough for `TASK wait`, but terminal
session metadata is still minimal.

Recommended scope:

- Preserve stderr independently from stdout when ACP clients expose it
- Track more accurate last-activity timestamps
- Record clearer termination reasons for cancelled vs exited sessions
- Surface better task/session diagnostics in ACP updates

### 3. Non-`file://` resource links

Only `ResourceLink(file://...)` is supported today. This is enough for coding
workflows, but richer client-provided resources still fall back to unsupported.

Recommended scope:

- Define an allowlist of URI schemes worth supporting
- Keep `file://` semantics unchanged
- Be explicit about sandbox and trust boundaries for any remote/resource-backed
  scheme

### 4. Multimodal prompt blocks

Image/audio prompt blocks are still unsupported. This is not a coding-path
priority, but it is the main protocol-surface gap once text/resource-link
handling is stable.

Recommended scope:

- Add capability advertisement only when the model/runtime can actually consume
  the modality
- Avoid ACPX-specific behavior; stay inside ACP prompt block semantics

### 5. Stronger authentication if remote transports ever appear

The current auth flow is deliberately local-first. If ACP grows beyond local
`stdio`, this needs a stronger model.

Recommended scope:

- Keep the current lightweight env-backed method for local use
- Add a distinct remote-capable auth design instead of stretching the local
  handshake
- Do not couple provider credentials to ACP client authentication

### 6. Multi-workspace / multi-cwd session hosting

The current one-process-one-workspace model keeps behavior predictable and is a
good fit for `acpx` today. If a long-lived ACP daemon becomes desirable, this
will become the next structural limitation.

Recommended scope:

- Only revisit after there is a concrete long-lived daemon use case
- Preserve the current single-workspace behavior as the safe default

## Intentionally deferred

- ACPX-specific proprietary integration
  - ACPX + CLI + Skills is already a good fit while ACP and clients are still
    moving quickly
  - Prefer keeping `caelis acp` standards-based unless ACPX-only features show
    clear, durable value
- ACP unstable `session/set_model`
  - Leave this out until the protocol surface settles and there is a real need
    to hot-swap models per ACP session
