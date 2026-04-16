# Session / Controller / Participant / Event Target Model

## Goal

This document defines the target interaction model for the refactored runtime.

It focuses on four things only:

- session identity and persistence
- controller ownership
- participant attachment and delegation
- one canonical event model

This document does not define implementation steps.

## Product Model

Caelis should expose one unified multi-agent session model.

Each session has:

- exactly one active controller
- zero or more attached participants
- one canonical event stream

The current three product forms remain valid, but they are expressed through
the same runtime model:

1. main kernel agent
2. main ACP controller
3. sidecar ACP participant

`SPAWN` is not a separate interaction category. It is a participant attachment
path whose trigger is programmatic rather than manual.

## Core Principles

1. One session, one active controller
2. Participants never become implicit controllers
3. All runtime paths emit the same canonical event shape
4. Canonical events should be ACP-compatible by default
5. Kernel-specific semantics should extend protocol-shaped events through
   metadata rather than inventing a parallel event language
6. UI, Gateway, replay, persistence, and ACP adapters should all consume the
   same event contract

## Session Model

A session is the durable unit of work and transcript continuity.

The session owns:

- session identity
- canonical event log
- durable state
- controller binding state
- participant attachment state

The session does not own:

- transport details
- ACP wire payloads
- UI rendering state
- runtime-local retry buffers or transient execution state

### Session Shape

```go
type Session struct {
    ID           string
    WorkspaceID  string
    CreatedAt    time.Time
    UpdatedAt    time.Time
    ActiveBind   ControllerBinding
    Participants []ParticipantBinding
    State        map[string]any
}
```

The concrete Go type may differ, but the durable semantics should match this
shape.

## Controller Model

The controller is the actor that currently owns the main prompt-turn loop for a
session.

There is exactly one active controller at a time.

### Controller Kinds

- `kernel`
- `acp`

### Controller Binding

```go
type ControllerBinding struct {
    Kind         string
    ControllerID string
    Label        string
    EpochID      string
    AttachedAt   time.Time
    Source       string
}
```

Required semantics:

- `Kind` distinguishes `kernel` vs `acp`
- `ControllerID` is empty for the local kernel controller and stable for ACP
  controllers
- `EpochID` changes whenever main control is handed off
- `Source` records why the binding exists, for example `user_select`,
  `resume_restore`, or `handoff_accept`

### Controller Epoch

Controller ownership changes must be represented by epoch changes rather than
by inventing a new session or transcript.

An epoch exists to answer:

- who controlled this event
- when handoff happened
- whether remote sync state is still valid

All canonical events emitted during a controller epoch must carry controller
metadata.

## Participant Model

Participants are attached collaborators within the same session.

Participants do not own the main loop unless an explicit handoff is approved.

### Participant Kinds

- `acp`
- `subagent`
- future local or remote specialized participants

### Participant Roles

- `sidecar`
- `delegated`
- `observer`

`SPAWN` and manual sidecar ACP should share the same participant lifecycle:

- attach
- start or prompt
- stream updates
- inspect or resume
- cancel or detach

They differ only in trigger metadata:

- `source=user_attach`
- `source=agent_spawn`

### Participant Binding

```go
type ParticipantBinding struct {
    ID            string
    Kind          string
    Role          string
    Label         string
    Source        string
    ParentTurnID  string
    DelegationID  string
    AttachedAt    time.Time
    ControllerRef string
}
```

`ControllerRef` links the participant to the controller epoch that created or
attached it.

## Handoff Model

Handoff is the only way control moves from one controller to another.

### Handoff Phases

1. proposal
2. decision
3. activation

### Proposal Sources

- user initiated
- runtime initiated
- agent suggested

Agent-initiated switching is allowed only as a proposal. Agents should not
silently replace the controller.

The runtime or policy layer decides whether the proposal is:

- accepted
- rejected
- deferred for user confirmation

### Handoff Rules

- only idle or safely interruptible sessions may hand off
- handoff creates a new controller epoch
- handoff must emit canonical lifecycle events
- handoff must update durable controller binding state
- ACP handoff may require remote sync and handoff packet generation, but those
  remain adapter concerns rather than session concerns

## Canonical Event Model

The runtime should define one canonical event envelope for all internal and ACP
paths.

This should be ACP-compatible by default rather than kernel-specific by
default.

### Design Rule

If ACP already has a stable event concept, reuse it.

If the kernel needs extra semantics, extend the canonical event through `meta`
instead of creating a separate event class.

### Canonical Event Envelope

```go
type Event struct {
    ID        string
    SessionID string
    Type      string
    Time      time.Time
    Actor     ActorRef
    Payload   any
    Meta      map[string]any
}
```

```go
type ActorRef struct {
    Kind string // user | controller | participant | tool | system
    ID   string
    Role string
    Name string
}
```

### Required Meta Fields

All runtime events should be able to project the following metadata:

- `controller_kind`
- `controller_id`
- `controller_epoch`
- `participant_id`
- `participant_role`
- `delegation_id`
- `source`
- `turn_id`
- `transient`
- `acp_session_id`
- `acp_event_type`

Not every event must populate every field, but the envelope must support them.

### Canonical Event Families

At minimum, the canonical model should cover:

- user input
- agent message chunk
- agent thought chunk
- plan replace
- tool call start
- tool call update
- tool call result
- session state change
- participant attach
- participant detach
- handoff proposed
- handoff accepted
- handoff rejected
- run completed
- run failed
- run interrupted
- approval required

These families are intentionally protocol-shaped. The kernel should emit events
that can map naturally onto ACP-style transcripts.

### Payload Discipline

- canonical fields go into typed payloads
- protocol-shaped ACP extensions should live under one nested protocol payload
  rather than expanding the Event top level
- origin-specific raw payloads may be stored in `meta`
- UI consumers should prefer canonical payloads
- adapters may preserve raw ACP payloads for diagnostics, but raw payloads are
  not the primary product contract

### Standard ACP Compatibility

The canonical event model should remain legible to standard ACP clients even
when they do not understand Caelis-specific collaboration semantics.

That means:

- `message`, `tool_call`, `tool_call_update`, `plan`, and terminal lifecycle
  should stay close to ACP-native concepts
- `approval required` should align to ACP `session/request_permission`
  semantics rather than inventing a separate approval protocol
- controller or participant-specific semantics should remain optional metadata
  or nested protocol payloads, not required wire-level concepts for baseline
  ACP interoperability

In practice, a standard ACP client such as Zed should still function correctly
even if it ignores:

- controller epoch details
- participant attachment metadata
- delegation lineage
- handoff-specific annotations

## ACP Compatibility Strategy

ACP should be treated as the protocol grammar closest to the product contract.

That means:

- ACP controller updates should map into canonical events with minimal loss
- kernel-native turns should emit the same event families
- sidecar ACP and spawned subagents should use the same projection grammar

The system should avoid these anti-patterns:

- one event model for kernel and another for ACP
- UI renderers branching on event origin instead of event family
- session persistence storing ACP raw messages separately from main runtime
  semantics

## Persistence Model

The durable session log should store canonical events.

Additional projection or transport-specific artifacts may exist, but they are
secondary.

Persistence priorities:

1. canonical session event log is the source of truth
2. controller and participant bindings are durable session metadata
3. ACP transport state is adapter-owned sync state
4. UI-only projection caches are disposable

This keeps replay, resume, Gateway, and future GUI surfaces aligned to the same
source of truth.

## Runtime Boundary Implications

### Session Layer

The session layer should own:

- event append and load
- controller binding persistence
- participant binding persistence
- durable state persistence

The session layer should not decide:

- whether a handoff is allowed
- how ACP transport reconnect works
- how a tool call is rendered

### Runtime Layer

The runtime layer should own:

- turn execution
- policy evaluation
- handoff proposal and decision flow
- participant orchestration
- translation from agent execution to canonical events

The runtime layer should not own:

- ACP wire encoding
- UI-specific event shaping

### ACP Adapter Layer

The ACP adapter layer should own:

- ACP client and server transport
- handoff packet construction for remote ACP controllers
- mapping ACP wire updates into canonical events
- remote session sync state

The ACP adapter layer should not define a separate product event model.

## Mapping of Existing Product Modes

### Main Kernel Agent

- active controller: `kernel`
- participants: optional
- events: canonical events emitted directly by runtime

### Main ACP Controller

- active controller: `acp`
- participants: optional
- events: ACP updates normalized into canonical events

### Sidecar ACP

- active controller: unchanged
- participant: attached ACP participant
- events: canonical participant events with `participant_role=sidecar`

### Spawned Subagent

- active controller: unchanged unless later handoff is accepted
- participant: attached delegated participant
- events: canonical participant events with `participant_role=delegated`

## Simplification Rules

To keep the model small:

1. keep only one controller concept
2. keep only one participant concept
3. treat `SPAWN` and manual sidecar attach as the same lifecycle with different
   trigger sources
4. treat controller switch as handoff, not as a separate runtime mode
5. make canonical events the only upstream contract for UI and Gateway

## Decision Summary

The target runtime model should be:

- one durable session
- one active controller
- many optional participants
- one canonical ACP-compatible event envelope
- one explicit handoff mechanism for controller changes

This is the smallest model that still preserves:

- kernel main control
- ACP main control
- sidecar ACP collaboration
- spawned delegation
- future multi-agent coordination inside a single session
