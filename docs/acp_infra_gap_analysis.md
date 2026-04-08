# ACP Infrastructure Gap Analysis

## Scope

This document compares Caelis's current ACP implementation with the official
Rust SDK structure, with the goal of turning ACP support into reusable Go
infrastructure first, then iterating on TUI/UX on top of that foundation.

Reference SDK inspected:

- Rust SDK repository: `https://github.com/agentclientprotocol/rust-sdk`
- Local comparison checkout used for analysis: `/tmp/acp-rust-sdk`

## Current Caelis ACP Surface

Current ACP-related code is primarily split across:

- `internal/acp`
- `internal/acpclient`
- `internal/app/acpext`

### What already exists

- Protocol schema types for ACP methods and updates
- JSON-RPC stdio connection primitives
- ACP client process spawning and notification handling
- ACP server implementation for Caelis sessions
- Permission bridge
- File system and terminal capability bridging
- Session load/new/mode/config support
- ACP child-session update projection into runtime/TUI

This is enough to support real ACP product behavior today.

### Main problem

The implementation is product-capable, but not yet packaged as reusable ACP
infrastructure. Product concerns and protocol concerns are still mixed.

Examples:

- `internal/acp/runtime.go` mixes protocol-facing capability bridging with
  Caelis execution-runtime policy
- `internal/app/acpext/session_update_bridge.go` mixes ACP event normalization
  with Caelis runtime/sessionstream semantics
- `internal/acp/server*.go` couples generic server flow with Caelis adapter and
  runtime assumptions
- `internal/acpclient/client.go` couples generic client transport with local
  process spawning and Caelis defaults

## Official Rust SDK Structure

The Rust SDK is notably more layered.

### High-level package layout

- `agent-client-protocol-core`
  Protocol schema, roles, JSON-RPC connection, session model, cookbook,
  concepts, handler abstractions
- `agent-client-protocol-tokio`
  Runtime-specific stdio/process helpers
- `agent-client-protocol-test`
  Reusable testing fixtures
- `agent-client-protocol-conductor`
  Proxy/orchestration binary layer
- `agent-client-protocol-trace-viewer`
  Trace visualization

### Key architectural traits

- Core schema is clearly separate from transport/runtime integration
- JSON-RPC dispatch and handler model are isolated as reusable infrastructure
- Runtime-specific spawning is in a separate crate
- Test support is a first-class module, not just package-local tests
- Cookbook/concepts documentation captures ordering and session semantics

## Comparison Summary

### Where Caelis is already strong

- Practical ACP feature coverage is broad
- Real-world client/server interop is already exercised
- Permission and terminal bridging are deeper than a toy SDK
- Session resume/load and external-agent integration are already built

### Where Caelis is behind SDK-quality packaging

- No clean separation between schema/core/runtime/product adapters
- No reusable ACP test harness package
- No explicit “core concepts” docs for ordering, session lifecycle, retries,
  and update projection
- No stable public-facing Go package boundary for ACP components
- UI/event projection still lives too close to product-specific runtime logic

## Recommended Go Infrastructure Split

Do not port the Rust SDK directly. Instead, reshape the existing Go code into
similarly clean layers.

### Layer 1: ACP Schema/Core

Candidate destination:

- `pkg/acp/schema` or `internal/acp/core/schema`

Move/keep responsibilities:

- protocol message structs
- method/update constants
- request/response/update enums or typed wrappers
- raw content/tool-call modeling

Likely source files:

- `internal/acp/protocol.go`
- `internal/acpclient/protocol.go`

Immediate gap:

- eliminate duplicated protocol definitions between server/client packages

### Layer 2: JSON-RPC Connection/Transport Core

Candidate destination:

- `pkg/acp/jsonrpc`
- `pkg/acp/transport/stdio`

Move/keep responsibilities:

- connection abstraction
- request/response correlation
- notification dispatch
- stdio framing and low-level serving/calling

Likely source files:

- `internal/acp/conn.go`
- `internal/acpclient/conn.go`

Immediate gap:

- unify shared connection logic instead of maintaining mirrored client/server
  implementations

### Layer 3: ACP Client Runtime

Candidate destination:

- `pkg/acp/client`

Move/keep responsibilities:

- initialize/new/load/prompt/cancel/mode/config methods
- update decoding
- permission callback hook

Keep runtime-specific spawning separately:

- `pkg/acp/client/stdioexec` or similar

Likely source files:

- `internal/acpclient/client.go`
- `internal/acpclient/loopback.go`
- `internal/acpclient/permission_policy.go`

Immediate gap:

- split protocol client from local process lifecycle concerns

### Layer 4: ACP Server Runtime

Candidate destination:

- `pkg/acp/server`

Move/keep responsibilities:

- request handling
- session registry
- update emission
- adapter interface
- capability negotiation

Likely source files:

- `internal/acp/server.go`
- `internal/acp/server_session.go`
- `internal/acp/server_stream.go`
- `internal/acp/server_tool_helpers.go`
- `internal/acp/server_prompt.go`
- `internal/acp/adapter.go`

Immediate gap:

- extract a generic `Adapter` contract that does not depend on Caelis runtime
  details beyond the interface boundary

### Layer 5: ACP Capability Bridges

Candidate destination:

- `pkg/acp/extensions/fs`
- `pkg/acp/extensions/terminal`
- `pkg/acp/extensions/permission`

Likely source files:

- `internal/acp/runtime.go`
- `internal/acp/permission.go`

Immediate gap:

- separate reusable capability bridge logic from Caelis execution policy and
  workspace policy

### Layer 6: Product Adapters

Keep in product code:

- `internal/app/acpext/self_runner.go`
- `internal/app/acpext/session_update_bridge.go`
- TUI projection and transcript rendering

These should depend on ACP infrastructure, not define it.

## Gap Checklist

### P0

- Unify duplicated schema definitions
- Unify duplicated connection code
- Split process spawning from generic ACP client
- Define one reusable server adapter boundary

### P1

- Extract generic permission bridge package
- Extract generic fs/terminal bridge helpers
- Build ACP conformance-style fixtures from current tests
- Add docs for ordering, replay, update lifecycle, and cancellation

### P2

- Add a reusable ACP event projector for UI/runtime consumers
- Add trace capture/replay utilities
- Add external examples for standalone client and standalone agent

## Progress Update

The following refactors have already been completed in-repo.

### 1. Schema consolidation

- `internal/acpclient` now reuses `internal/acp` protocol types where possible
- client-local raw decoding types are kept only where delayed decoding is
  necessary

Impact:

- reduced schema drift risk between client and server paths
- made ACP method/update modeling closer to one canonical source of truth

### 2. Shared JSON-RPC connection layer

- shared connection code was extracted into `internal/acpconn`
- `internal/acp` and `internal/acpclient` now wrap the shared connection layer
  instead of maintaining mirrored implementations

Impact:

- request/response dispatch semantics are centralized
- transport-level bug fixes now land once instead of in two codepaths

### 3. ACP client split into core/runtime/process layers

- `internal/acpclient/core_client.go` now owns generic ACP client operations:
  initialize, session lifecycle, prompt, cancel, session-update decoding, and
  permission callbacks
- `internal/acpclient/local_client.go` now owns local capability handling:
  filesystem, terminal, stderr capture, and runtime-backed request handling
- `internal/acpclient/client.go` now acts as a thin product-facing wrapper for
  process startup, lifecycle, and explicit delegation to the core client
- `internal/acpclient/loopback.go` now wires the shared layers together instead
  of constructing a monolithic client

Impact:

- generic ACP protocol behavior is now separated from Caelis local process glue
- capability bridging is easier to extract further into reusable infrastructure
- all call paths now enter through explicit client assembly instead of lazy
  fallback initialization inside `Client`

### Next recommended step

- perform the same split on the server side by separating generic ACP server
  flow from Caelis runtime/session adapter glue

### 4. Server state core extraction

- `internal/acp/server_state.go` now owns shared server state primitives:
  authentication state, client capabilities, loaded session registry, and live
  stream session registry
- `internal/acp/server.go` now uses that state core instead of storing those
  concerns directly on `Server`
- `internal/acp/server_session.go` and `internal/acp/server_stream.go` now read
  and mutate shared state through one path instead of touching ad hoc `Server`
  fields

Impact:

- generic ACP server state is starting to separate from request-dispatch and
  adapter-specific logic
- the next extraction can focus on RPC dispatch and prompt/session orchestration
  without re-solving state ownership

### 5. Server RPC core extraction

- `internal/acp/server_core.go` now owns RPC dispatch, session/prompt request
  routing, and outbound session-update notification helpers
- `internal/acp/server.go` now acts as the server assembly entrypoint and thin
  wrapper around the core

Impact:

- request handling logic is now isolated from server construction
- prompt execution and session update emission have one explicit core path
- the next step can focus on separating generic server flow from Caelis adapter
  semantics instead of disentangling transport dispatch again

### 6. Server service bridge extraction

- `internal/acp/server_services.go` now wraps auth methods, auth validation,
  adapter calls, cancel handling, and session filesystem access behind one
  bridge
- `server_core.go` and `server_session.go` no longer reach directly into
  `ServerConfig.Adapter` or `ServerConfig.Authenticate`

Impact:

- generic server flow now depends on an explicit services boundary rather than
  raw product wiring
- the next adapter-facing cleanup can narrow request/response semantics without
  reopening server core or state ownership

### 7. Adapter request model narrowing

- `internal/acp/adapter.go` now defines adapter-facing request types for
  session creation/loading and mode/config mutation
- `server_services.go` now converts ACP RPC request structs into those narrower
  adapter request models
- `internal/app/acpadapter/service.go` and the ACP harness adapter tests now
  implement the new adapter request boundary directly

Impact:

- adapter implementations no longer need to depend on ACP RPC request structs
- generic ACP server flow is closer to a reusable infrastructure layer, while
  Caelis adapter logic now sits behind a cleaner semantic boundary

### 8. Live ACP event projector extraction

- `internal/acpprojector/live.go` now owns a shared live-update projector for
  `ContentChunk`, `ToolCall`, `ToolCallUpdate`, and `PlanUpdate`
- `internal/app/acpext/session_update_bridge.go` now consumes that projector
  instead of maintaining its own ACP update decoding state
- `cmd/cli/external_agent_slash.go` and `cmd/cli/external_participants_resume.go`
  now use the same live projector for external ACP participant forwarding

Impact:

- live ACP updates now have one canonical projection path into session events
  and UI-facing streams
- self ACP and external ACP are materially closer to sharing the same transcript
  grammar end to end

### 9. Replay ACP event projector extraction

- `internal/acpprojector/replay.go` now owns resumed/replay ACP projection,
  including the `loading -> live` phase transition needed for session-load
  history
- `cmd/cli/subagent_resume_acp.go` now uses the replay projector instead of a
  custom ACP replay state machine

Impact:

- live ACP and resumed ACP now share one projector family instead of parallel
  hand-written update decoders
- TUI-facing subagent projection is now much closer to consuming a single
  normalized ACP event model regardless of source or lifecycle phase

### 10. TUI ACP projection message path

- `internal/cli/tuievents/messages.go` now defines a shared `ACPProjectionMsg`
  for protocol-native participant/subagent updates
- `internal/cli/tuiapp/stream_acp_projection.go` now applies those projection
  messages directly onto participant turns and subagent panels
- external ACP participants and resumed ACP subagents now send projector output
  to TUI via `ACPProjectionMsg` instead of first decomposing into bespoke
  `RawDeltaMsg`, `ParticipantToolMsg`, or replay-only synthetic paths

Impact:

- projector output now reaches the TUI update layer directly
- participant and subagent rendering are materially closer to consuming one
  normalized ACP transcript model

### 11. Spawn preview projection convergence

- `cmd/cli/spawn_preview.go` now sends subagent preview stream/tool/plan updates
  through `ACPProjectionMsg` instead of bespoke preview-only TUI message types
- `internal/acpprojector/format.go` now provides lightweight formatting helpers
  for preview-side tool argument and result summaries
- `internal/cli/tuiapp/stream_acp_projection.go` now handles in-progress tool
  previews on the same projection path used by external ACP and resumed ACP

Impact:

- spawned subagent previews, resumed ACP subagents, and external ACP
  participants now share one projection-first transcript update path into TUI
- subagent-specific legacy messages are now mostly limited to panel lifecycle
  concerns such as bootstrap, status, approval wait state, and terminal state
- the remaining UI work can focus on transcript rendering quality rather than
  source-specific event decoding

## UI Implication

Once ACP infrastructure is cleanly layered, the TUI should consume one
normalized ACP event timeline regardless of source:

- self ACP delegate
- external ACP participant
- resumed ACP session
- spawned subagent preview

This lets the UI become protocol-native instead of source-native.

## Suggested Execution Order

1. Merge protocol structs into one schema layer.
2. Merge connection logic into one JSON-RPC/transport layer.
3. Split generic client/server from Caelis process/runtime glue.
4. Extract capability bridges.
5. Introduce a canonical ACP event projector.
6. Rebuild TUI presentation entirely around that projector.

## Progress Update

### Completed

1. Schema consolidation has started.
   `internal/acpclient/protocol.go` now reuses most ACP schema types from
   `internal/acp`, while keeping only a few client-local raw-decoding shapes.

2. Connection layer consolidation is complete.
   Shared JSON-RPC connection code now lives in:

   - `internal/acpconn/conn.go`

   Both:

   - `internal/acp/conn.go`
   - `internal/acpclient/conn.go`

   are now thin wrappers over that shared implementation.

### Next recommended step

Split generic client/server logic from product-specific process/runtime glue:

- keep protocol client/server in reusable ACP infrastructure
- move Caelis-specific spawning, runtime binding, and delegation projection to
  product adapters
