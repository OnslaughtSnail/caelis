# Unified Gateway Foundation Spec

## Status

This document is the authority for the next refactor layer after the SDK
foundation cleanup.

It defines the target shape of a `sdk`-backed Unified Gateway.

## Product Goal

Caelis is not trying to clone OpenClaw.

The target product is:

- an ACP-naive multi-agent coordination framework
- a single entry protocol shared by multiple product surfaces
- a system that can support `TUI`, `GUI`, and future remote channels without
  rebuilding orchestration logic per surface

The Unified Gateway exists to make that possible.

## Why This Layer Exists

The SDK gives us reusable runtime, session, controller, subagent, tool, and
assembly capabilities.

That is necessary, but not sufficient, for a real product boundary.

The product still needs a single application-facing control plane that can:

- accept inbound requests from multiple surfaces
- own session and turn orchestration
- expose streaming output through one contract
- carry future channel, daemon, and remote-control concerns without leaking
  them into the SDK

That layer is the Unified Gateway.

## Scope Of This Phase

This phase should build the extensibility skeleton only.

In scope:

- define the Unified Gateway contract
- define `sdk -> gateway -> adapters` layering
- reserve extension points for daemon mode and remote channels
- keep old code available only as behavior reference

Out of scope:

- switching the production CLI to the new gateway
- deleting legacy code immediately
- implementing Telegram / Discord / webhook adapters
- implementing daemon registration or remote transport
- copying OpenClaw feature-for-feature

## Architectural Position

The target layering is:

1. `sdk`
   - runtime, sessions, controllers, subagents, tools, sandbox, assembly
2. `gateway core`
   - product-facing orchestration boundary built only on `sdk`
3. `gateway adapters`
   - `tui`, `gui`, and future remote channel adapters
4. `gateway host`
   - foreground process lifecycle, health, logging, optional daemon wiring
5. `gateway transport`
   - in-process first; remote transports may be added later without changing
     gateway core semantics

The key rule is:

`sdk` knows nothing about product surfaces, channels, or daemon hosting.

## Contract Goals

The Unified Gateway contract must be stable enough that all product surfaces can
share one entry protocol.

That protocol should support at least:

### 1. Identity And Routing

- surface identity
- actor identity
- workspace identity
- session binding identity
- future channel/account/thread targeting

### 2. Session Lifecycle

- start
- load
- resume
- fork
- list / resolve recent session

### 3. Turn Lifecycle

- begin turn
- submit content
- stream events
- close handle
- interrupt
- reconnect / resume stream

### 4. Control-Plane State

- controller kind and controller id
- epoch and handoff state
- projection state required for ACP continuity
- future per-surface or per-channel metadata

### 5. Output Surface

- session events
- task stream events
- tool output chunks
- usage snapshots
- artifact references
- structured errors

### 6. Execution Policy Hooks

- approver / authorization hooks
- surface-level capability filtering
- future remote-actor policy checks

## Required Boundary Rules

1. Unified Gateway must depend only on the new `sdk` boundary.
2. Unified Gateway must not import `kernel/sessionsvc`, `kernel/runservice`, or
   equivalent legacy execution boundaries.
3. Product adapters must not orchestrate turns directly against `sdk` internals.
   They should talk to the gateway contract.
4. Daemon and remote concerns must stay outside `sdk`.
5. Channel-specific concerns must stay in adapters or host integrations, not in
   gateway core orchestration.

## Legacy Code Status

The existing implementations under:

- `internal/app/gateway`
- `internal/app/bootstrap`
- `cmd/cli` main-turn orchestration
- old ACP main-session coordination paths

remain useful in this phase only as:

- behavior reference
- regression oracle
- capability inventory

They are legacy reference code and should be treated as pending deletion after
the new gateway adoption is complete.

They are not the target architecture.

## Extensibility Requirements

The Unified Gateway must be designed so the following future product forms can
reuse the same entry protocol:

### Local Surfaces

- terminal UI
- graphical UI

### Remote Surfaces

- Telegram bot
- Discord bot
- future webhook or operator client surfaces

### Host Modes

- foreground local process
- optional daemon / service process with persistent lifecycle

The important rule is that these are adapter or host concerns.

They must not force the SDK to absorb product-facing transport logic.

## Daemon Direction

Daemon support is a future host capability, not a current implementation task.

The contract should still reserve room for:

- long-running gateway ownership
- health/status inspection
- graceful shutdown
- interrupt propagation to active turns
- reconnect-friendly turn and stream handles
- future remote operator attachment

No daemon registration, `launchd`, `systemd`, or platform-specific service
implementation is required in this phase.

## Remote Channel Direction

Remote channels are future gateway adapters.

They will eventually need:

- sender identity
- channel/account/thread addressing
- per-channel authz and allowlist hooks
- message-to-session binding
- outbound response delivery
- streaming-friendly response surfaces when supported

This phase should only ensure the gateway contract can carry that metadata.

It should not implement real Telegram or Discord integration yet.

## Recommended Delivery Order

### Stage 1: Gateway Core Contract

Deliver:

- gateway package shape
- request / response / event contracts
- in-process implementation skeleton backed only by `sdk`
- tests that prove the contract is independent from current CLI flow

### Stage 2: TUI-First Adoption

Deliver:

- TUI main path migrated onto the new gateway contract
- local main turn and ACP main turn both routed through the same gateway entry

### Stage 3: Additional Product Adapters

Deliver later:

- GUI adapter
- remote channel adapters
- optional daemon host
- optional remote transport

## Acceptance Criteria For The Next Build Stage

The next implementation stage is successful when:

1. a new Unified Gateway core exists and imports only `sdk`
2. its contract explicitly models session, turn, stream, interrupt, and
   control-plane state
3. the design leaves clear extension points for remote channels and daemon host
   mode
4. legacy gateway and CLI orchestration are documented as reference-only, not
   target architecture
5. docs remain small and current

## Non-Goals And Failure Modes

Do not treat success as:

- a renamed wrapper around legacy `sessionsvc`
- a second orchestration path that coexists indefinitely with CLI-owned flow
- channel-specific logic embedded into core gateway orchestration
- daemon-specific process management embedded into `sdk`

If any of those appear, the boundary has failed.
