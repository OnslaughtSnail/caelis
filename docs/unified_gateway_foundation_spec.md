# Unified Gateway Foundation Spec

## Status

This document is the authority for the current `sdk`-backed Unified Gateway
layer.

The foundational local gateway now exists in code and is the active reference
boundary for:

- session lifecycle through `gateway`
- turn orchestration through `gateway`
- local headless and minimal local interactive entry through the new `cmd/cli`
- minimal control-plane state and ACP main controller handoff/adoption through
  the new `gateway`
- session-backed replay/reconnect, continuity checkpoints, and richer binding
  state through the new `gateway`

This document now defines:

- the accepted Stage 1 boundary
- the current acceptance target
- the remaining deferred work after local acceptance

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

This phase should build and validate the local extensibility skeleton.

In scope:

- define the Unified Gateway contract
- define `sdk -> gateway -> adapters` layering
- land one app-owned composition root on top of the new `sdk`
- land local headless and minimal local interactive adoption on the new gateway
- expose canonical gateway events for local adapters
- land minimal control-plane state and ACP main controller handoff on the new
  gateway
- land session-backed replay/reconnect plus richer binding metadata for local
  surfaces
- reserve extension points for daemon mode and remote channels

Out of scope:

- implementing Telegram / Discord / webhook adapters
- implementing daemon registration or remote transport
- implementing durable reconnect across process restart
- implementing channel-scoped auth, pairing, or remote actor policy
- copying OpenClaw feature-for-feature

## Stage 1 Acceptance Target

The Unified Gateway should be considered acceptable for Stage 1 when all of the
following are true:

- it depends only on the new `sdk`
- one app-owned composition root assembles `sdk -> gateway -> adapter` without
  any legacy bootstrap path
- local headless and minimal local interactive turns both run through the same
  gateway turn contract
- adapters consume canonical gateway events rather than parsing raw SDK runtime
  behavior directly
- session-backed replay and continuity checkpoints are available without
  depending on live in-memory handles
- binding state exposes session identity, ownership metadata, and expiry
  behavior through gateway-owned contract
- session lifecycle, turn lifecycle, approval bridging, interrupt, and current
  local resume/binding flows are covered by tests
- remaining ACP / remote / daemon work is explicitly deferred rather than
  implied complete

## Deferred After Stage 1

The following work remains intentionally deferred after local acceptance:

- full ACP adapter and richer ACP surface adoption beyond minimal controller
  handoff and local acceptance coverage
- daemon host lifecycle
- remote transport and remote channel adapters
- channel-scoped auth, pairing, and remote actor policy

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

## Stage 1 Packaging Rule

The 5 layers above are a conceptual architecture, not a mandatory Stage 1
package split.

Stage 1 should stay physically small:

- `sdk/`
- `gateway/`
- `gateway/adapter/`

In Stage 1, `gateway/` may temporarily contain both gateway core logic and the
minimal local host lifecycle needed for in-process use.

Dedicated `host` and `transport` packages should only be extracted when daemon
mode or remote transport becomes real implementation work.

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
- projection and continuity checkpoints required for ACP recovery
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

## Current Accepted Local Contract

The accepted local gateway contract now includes:

- session lifecycle: `start`, `load`, `resume`, `fork`, `list`
- explicit binding lifecycle: bind one session to one surface-owned key and
  inspect binding state through the gateway
- turn lifecycle: `begin`, `submit`, `interrupt`, `close`
- live handle replay through `EventsAfter(cursor)`
- durable session-backed replay through gateway replay APIs built from
  persisted session events
- minimal control-plane state with controller binding, participant bindings,
  run state, and continuity checkpoints

Continuity checkpoints currently mean:

- latest persisted session cursor
- latest cursor in the active controller epoch
- latest cursor per participant
- latest ACP-origin cursor and event type when ACP-origin events exist

This is sufficient for local recovery and adapter continuity without leaking
raw SDK internals. It is not yet a full remote transport protocol.

## Turn Handle Contract

Turn Handle is the core gateway contract.

The gateway must expose its own handle abstraction rather than leaking the SDK
runtime runner directly into adapters.

The handle contract must include:

### 1. Stable Handle Identity

- `handle_id`
- `session_ref`
- `run_id`
- `turn_id`
- creation timestamp

The handle identity must remain stable for the life of one active turn.

### 2. Event Consumption

Adapters must consume one canonical gateway event stream from the handle.

The handle contract must define:

- whether the primary stream is single-consumer or multi-subscriber
- event ordering guarantees
- terminal event / terminal error behavior
- replay and reconnect behavior

Stage 1 should default to one canonical event stream per handle plus explicit
replay / reconnect APIs, rather than implicit multi-consumer streaming.

### 3. Cursor And Reconnect

Reconnect requires durable cursor semantics.

The handle contract must expose at least one stable resume token based on
gateway-owned event ids or cursors so an adapter can:

- reconnect after transient disconnect
- ask for events after a known cursor
- detect stream resync boundaries

### 4. Continuation Submission

The handle must support mid-run continuation through explicit submission.

This includes at least:

- additional conversational input
- overlay or side-question style submission
- approval responses

Adapters must not talk to the SDK runner directly for continuation.

### 5. Approval Bridge

Interactive approval is part of the handle contract, not an adapter-side
special case.

The gateway must bridge SDK approval requests into canonical gateway approval
events and accept canonical gateway approval submissions back through the same
handle.

### 6. Cancel vs Close

The handle contract must distinguish:

- `cancel`: request interruption of the active turn
- `close`: release adapter ownership of the handle and its stream resources

`close` must be idempotent.

`cancel` must not be overloaded as stream cleanup.

## Concurrency Contract

The Unified Gateway must define its concurrency semantics explicitly.

Stage 1 should use:

- at most one active run per session
- cross-session parallelism is allowed
- continuation `submit` is valid only for the currently active handle of that
  session
- a second `begin turn` on the same session while a handle is active must fail
  with a structured conflict error unless the API explicitly requests takeover

This rule applies across all adapters, not per adapter.

The gateway, not the adapter, owns active-run arbitration.

## Intent Resolution And Assembly Ownership

Adapters should pass user intent, not fully assembled runtime objects.

Adapters should not construct or select concrete `Agent`, `Model`, or similar
execution objects before calling the gateway.

The contract should prefer gateway-facing requests shaped like:

- session reference
- input and content parts
- mode or profile hint
- model preference hint
- surface or channel metadata

The gateway must then resolve that intent against already-resolved app-owned
configuration such as `sdk/plugin.ResolvedAssembly`.

Important boundary rule:

- plugin discovery
- manifest parsing
- assembly precedence resolution

remain composition-root responsibilities, not gateway responsibilities

The gateway owns runtime selection from resolved data, not plugin lifecycle.

## Composition Root Contract

The spec must treat gateway construction as a first-class contract.

Stage 1 does not need a final bootstrap implementation, but it must define:

### Required Dependencies

The gateway must be constructed from stable higher-level interfaces, including
at least:

- session service or equivalent SDK session boundary
- runtime boundary
- control-plane boundary when present
- resolved assembly input
- policy and approval hooks
- optional terminal/task/artifact capabilities

### Construction Ownership

Composition root is responsible for:

- building the SDK dependencies
- loading and resolving app configuration
- injecting resolved assembly and policy surfaces into the gateway

Adapters must not become de facto composition roots.

### Lifecycle Ownership

The composition root must own startup and shutdown of long-lived dependencies.

The gateway may expose `Close`, but should not silently own external process
lifetimes it did not create.

## Structured Error Model

The gateway must define a transport-stable structured error model.

The error contract should include at least:

- stable `kind`
- stable `code`
- `retryable`
- `user_visible`
- optional safe display message
- optional internal detail

The error taxonomy must distinguish at least:

- session lookup / binding errors
- active-run conflict errors
- approval-required or approval-aborted errors
- context / compaction / token-window errors
- adapter misuse or invalid request errors
- internal gateway or runtime failures

Adapters must not rely on string matching to drive UX behavior.

## Headless And Non-Interactive Use

Non-interactive mode is a first-class adapter shape.

The same gateway turn contract must support:

- interactive streaming surfaces such as TUI and future GUI
- blocking headless execution
- future CI or automation entrypoints

Headless mode is not a separate orchestration path.

It is one adapter strategy over the same gateway contract, typically by opening
one handle, draining it to completion, and applying a non-interactive approval
policy.

## Required Boundary Rules

1. Unified Gateway must depend only on the new `sdk` boundary.
2. Unified Gateway must not import `kernel/sessionsvc`, `kernel/runservice`, or
   equivalent legacy execution boundaries.
3. Product adapters must not orchestrate turns directly against `sdk` internals.
   They should talk to the gateway contract.
4. Daemon and remote concerns must stay outside `sdk`.
5. Channel-specific concerns must stay in adapters or host integrations, not in
   gateway core orchestration.
6. Gateway turn handles, concurrency control, and runtime selection must be
   owned by the gateway contract, not leaked to adapters.

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

## Channel Binding Contract

Channel or surface binding is gateway-owned routing state.

The contract must define:

- who creates a binding
- who may replace a binding
- what happens when one channel rebinds to another session
- whether bindings are ephemeral or persistent
- how stale bindings expire or are cleaned up

Stage 1 should prefer simple ephemeral bindings scoped to the local process,
with explicit replacement semantics and deterministic cleanup on adapter close.

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

## ACP Projection Ownership

Gateway core should own:

- canonical event model
- canonical turn and session state
- projection checkpoints or sync markers needed for continuity

Gateway core should not own ACP-specific rendering or projection formatting.

ACP projector behavior should remain adapter-specific protocol logic that
consumes canonical gateway events and gateway-owned continuity state.

This keeps gateway core protocol-agnostic while still supporting ACP continuity.

## Observability Reservation

Stage 1 does not need a full metrics or tracing design, but the contract should
reserve:

- per-turn correlation id
- per-handle traceable identifiers
- a wrapper or interceptor point around gateway methods

The goal is to avoid forcing observability concerns into core orchestration
later.

## Recommended Delivery Order

### Stage 1: Gateway Core Contract

Deliver:

- gateway package shape
- request / response / event contracts
- explicit turn handle contract
- explicit concurrency contract
- intent-based runtime selection from resolved assembly
- structured error model
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
3. its turn handle contract defines identity, continuation, approval, cursor,
   cancel, and close semantics
4. its concurrency contract defines one active run per session and cross-session
   parallelism
5. the design leaves clear extension points for remote channels and daemon host
   mode
6. legacy gateway and CLI orchestration are documented as reference-only, not
   target architecture
7. docs remain small and current

## Non-Goals And Failure Modes

Do not treat success as:

- a renamed wrapper around legacy `sessionsvc`
- a second orchestration path that coexists indefinitely with CLI-owned flow
- channel-specific logic embedded into core gateway orchestration
- daemon-specific process management embedded into `sdk`

If any of those appear, the boundary has failed.
