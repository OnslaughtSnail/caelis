# ACP Ordering, Replay, Update Lifecycle, and Cancellation

## Scope

This document defines the stable behavioral contract for the SDK ACP layer.

It applies to:

- `/Users/xueyongzhi/WorkDir/xueyongzhi/caelis/sdk/acp`
- runtime-backed agents exposed through `sdk/acp.RuntimeAgent`
- ACP-compatible replay and terminal adapters

## Ordering

The SDK ACP layer preserves canonical session event order.

Rules:

- `session/load` replays durable events in stored order.
- `session/prompt` emits updates in the same order the underlying runtime handle yields events.
- one canonical event may project to multiple ACP updates, but those updates remain adjacent and ordered.

Examples:

- assistant reasoning chunk before assistant final message
- tool call before tool call update
- handoff event before the first message produced by the new controller epoch

## Replay

Replay is durable-history driven.

Rules:

- `session/load` reads canonical events from `sdk/session`
- replay does not reconstruct hidden product-only state
- replay order is identical to durable event order
- replay is allowed to skip non-projectable events, but not reorder projectable ones

The intended source of truth is the canonical session event stream.

## Update Lifecycle

`session/prompt` uses this lifecycle:

1. append durable user event
2. execute runtime turn
3. normalize runtime events into canonical events
4. project canonical events into ACP notifications
5. emit notifications in-order

Permission requests are not durable transcript facts.

Rules:

- approval is treated as runtime orchestration
- approval may emit `session/request_permission`
- approval does not become replay history

## Cancellation

Cancellation is best-effort and session-scoped.

Rules:

- `session/cancel` targets the active prompt for that session
- a cancelled prompt should return `stopReason=cancelled`
- no durable "pending approval" or "pending cancel continuation" is required
- if a runtime is waiting on approval and the session is cancelled, the turn ends in terminal interrupted/cancelled state

## Test Coverage

The baseline SDK ACP conformance checks live in:

- `/Users/xueyongzhi/WorkDir/xueyongzhi/caelis/sdk/acp/conformance_test.go`
- `/Users/xueyongzhi/WorkDir/xueyongzhi/caelis/sdk/acp/runtime_agent_test.go`
- `/Users/xueyongzhi/WorkDir/xueyongzhi/caelis/sdk/acp/projector_test.go`
- `/Users/xueyongzhi/WorkDir/xueyongzhi/caelis/sdk/acp/terminal_test.go`

These tests cover:

- replay ordering
- prompt update ordering
- cancellation stop reason
- projector protocol shape
- terminal adapter lifecycle
