# ACP Product Plan

## Goal

Productize ACP, multi-agent, and remote execution on top of the existing local `sdk -> gateway -> app/gatewayapp -> adapters -> tui/headless` path without regressing the local headless or TUI default experience.

The key rule for this phase is architectural containment:

- ACP participants, controller handoff, subagent activity, approvals, and interrupts continue to flow through gateway canonical events.
- The TUI transcript continues to consume `gateway.EventEnvelope -> TranscriptEvent -> blocks/document`.
- Remote or daemon entry must continue to go through `gateway/host`.

## Current Reality

### Main controller

Current implementation:

- `sdk/controller/acp/manager.go` can activate one ACP main controller for a session.
- The manager starts an ACP client, creates a remote ACP session, forwards prompt turns, and bridges ACP updates back into normalized `sdk/session.Event` records.
- Approval requests are bridged through the runtime approval requester.
- `sdk/runtime/local/controlplane.go` can hand off the current session controller between local kernel and ACP.
- `gateway/core/gateway.go` exposes `HandoffController` and `ControlPlaneState`.

What is productized in this phase:

- TUI `/agent use <name|local>` on top of the gateway control plane.
- `/agent add <builtin>` and `/agent remove <agent>` update registered ACP agent configuration, not session participants.
- Dynamic slash commands such as `/codex <prompt>` create ACP child sessions and return `@handle` continuation targets.

### Participant

Current implementation:

- `sdk/runtime/local/controlplane.go` already supports `AttachACPParticipant` and `DetachACPParticipant`.
- `sdk/controller/acp/manager.go` can start ACP participants as sidecars and records stable participant bindings.
- Participant attach/detach lifecycle is appended to durable session history.
- Canonical gateway events already model participant lifecycle and participant-scoped narrative/tool/lifecycle traffic.

What is productized in this phase:

- Session participants are created by SPAWN or dynamic slash child sessions, not by `/agent add`.
- `@handle <prompt>` continues an existing delegated child session.

### Subagent

Current implementation:

- `sdk/subagent/acp/registry.go` provides ACP agent registry over resolved app assembly.
- `sdk/subagent/acp/runner.go` can spawn, wait on, and cancel ACP-backed subagents.
- Runtime permission requests are bridged back to the parent approval flow.
- Subagent activity already lands in canonical event and transcript paths.

What stays unchanged in this phase:

- No new subagent product surface.
- No new agent protocol beyond the existing ACP-backed registry and runner.

### Terminal and e2eagent

Current implementation:

- `acpbridge/cmd/e2eagent` is the stable local ACP fixture used by runtime, gateway, and adapter integration tests.
- `acpx` and `acpbridge` already have env-gated E2E coverage for sessions, tool loops, plans, approvals, async bash, and spawned ACP children.

What stays unchanged in this phase:

- Eval-style ACP bridge tests remain opt-in via env gating and do not block default `go test ./...`.

## User Concepts

These are the user-facing terms the product should use consistently.

### Main agent

The primary controller for the current session. By default this is the local kernel/runtime. After handoff it may be an ACP agent.

### Participant

An attached sidecar agent that shares the session context but does not replace the main controller. Participants are good for background collaboration, delegated observation, or companion workflows.

### Subagent

A delegated child run or child session created by the runtime for a bounded task. Subagents are runtime-owned execution units, not long-lived user-managed sidecars.

### Handoff

A control-plane switch that changes who owns the next main turn for the session. Example: local kernel -> ACP main controller, or ACP main controller -> local kernel.

### Background task

Work that continues outside the main foreground reply path, typically represented as participant or subagent activity and surfaced through the same transcript event flow.

### Approval

An explicit user decision required before sensitive execution continues. ACP permission requests and local runtime approvals must map to the same stable approval concept.

### Interrupt

Cancellation of the currently active turn. Interrupt acts on the gateway turn handle, not by directly reaching into runtime or TUI internals.

## Product Surface

### Headless and local default

Keep the current local headless and TUI main path unchanged:

- local session start/resume still goes through `app/gatewayapp.Stack` and `gateway/core.Gateway`
- transcript rendering still goes through canonical events and `TranscriptEvent`
- headless `RunOnce` remains the simplest single-turn path

### TUI `/agent`

Primary commands:

- `/agent list`
- `/agent add <builtin>`
- `/agent use <agent|local>`
- `/agent remove <agent>`

Behavior rules:

- All `/agent` operations go through `gateway/adapter/tui/runtime.Driver`.
- The driver talks to `app/gatewayapp.Stack` and `gateway/core.Gateway`.
- The TUI does not read runtime controller maps or participant registries directly.

Error recovery rules:

- unknown agent -> tell the user to run `/agent list`
- unknown handle -> tell the user that `@handle` only targets SPAWN or dynamic slash children
- ambiguous short id/name -> tell the user to type more or use the full id
- missing control plane -> report that ACP control plane is unavailable for this stack

## Event and Rendering Rules

ACP activity must not introduce a parallel TUI rendering system.

Required flow:

- runtime/session events
- gateway canonical projection
- `gateway.EventEnvelope`
- `ProjectGatewayEventToTranscriptEvents`
- `TranscriptEvent`
- transcript block/document model

Legacy ACP projection messages may remain as compatibility adapters, but ACP product work must continue converging on the same transcript event flow.

## Remote and Daemon Boundary

Remote or daemon execution must enter through `gateway/host`.

Allowed:

- daemon or bridge process creates a `gateway/host.Host`
- remote surfaces bind by remote address and call `EnsureRemoteSession`
- remote input calls `BeginRemoteTurn`

Not allowed:

- TUI or remote UI talking directly to runtime controller/subagent plumbing
- remote UI bypassing gateway session binding, replay, interrupt, or control-plane state

## Tests and Coverage

This phase relies on a combination of unit, integration, and env-gated eval coverage.

Default test coverage:

- headless single turn: `gateway/adapter/headless/e2e_test.go`
- TUI fake-turn and slash command dispatch: `tui/tuiapp/driver_bridge_test.go`
- ACP participant message projection and transcript lanes: `tui/tuiapp/transcript_event_test.go`
- subagent tool and interrupted-turn transcript behavior: `tui/tuiapp/transcript_event_test.go`
- ACP participant attach/detach and controller handoff integration: `sdk/runtime/local/e2e_test.go`
- gateway control-plane projection and wrappers: `gateway/core/gateway_test.go`
- TUI runtime driver ACP integration: `gateway/adapter/tui/runtime/gateway_driver_test.go`

Eval-style coverage:

- `acpbridge/acpx_e2e_test.go`
- gated behind `SDK_RUN_ACPX_E2E=1`
- must stay non-blocking for baseline `go test ./...`

## Known Limits

- `/agent ask`, `/agent status`, and `/agent handoff` are not part of the command surface.
- Mention completion lists delegated child handles only.
- `/agent remove` unregisters ACP agent config; it does not remove existing child sessions.
- Multi-remote session hosting is still host-centric and not yet a full daemon product with discovery, auth, or tenancy policy.
- ACP agent catalog is assembly-backed static config today; there is no dynamic runtime registration surface yet.
