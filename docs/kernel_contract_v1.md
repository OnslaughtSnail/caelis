# Kernel Contract v1

This document defines the runtime event and error contract consumed by upper layers (CLI/Web/API).

## Event Envelope

All runtime events follow `session.Event`:

| Field | Description |
|-------|-------------|
| `id` | Event ID |
| `session_id` | Session ID |
| `time` | Event timestamp |
| `message` | Model message payload |
| `meta` | Extensible metadata map |

## Contract Metadata Keys

- `meta.kind`: event kind
- `meta.contract_version`: contract version string (`v1`)

## Lifecycle Events

| Field | Value |
|-------|-------|
| `meta.kind` | `"lifecycle"` |
| `meta.contract_version` | `"v1"` |
| `meta.lifecycle.status` | lifecycle status (see below) |
| `meta.lifecycle.phase` | lifecycle phase |
| `meta.lifecycle.error` | error message _(optional)_ |
| `meta.lifecycle.error_code` | machine-readable error code _(optional)_ |

**Status values:** `running` · `waiting_approval` · `interrupted` · `failed` · `completed`

## Delegated Child-Run Lineage

Child-run session events may carry lineage metadata:

| Field | Description |
|-------|-------------|
| `meta.parent_session_id` | Parent session ID |
| `meta.child_session_id` | Child session ID |
| `meta.parent_tool_call_id` | Tool call ID that triggered delegation |
| `meta.delegation_id` | Delegation identifier |

These fields are for orchestration and observability only — they are not forwarded into model-visible message content. CLI may display compact session/delegation prefixes, but stored lineage values remain full IDs.

## Model Visibility

Runtime and tool metadata is for UI/orchestration use only and must not be forwarded to the model context. `llmagent` strips `metadata` and `_ui_*` fields before sending tool responses back to the model.

## Error Code Contract

Machine-readable error codes from `kernel/execenv` — use these for programmatic branching instead of string matching:

| Code | Meaning |
|------|---------|
| `ERR_APPROVAL_REQUIRED` | Human approval required |
| `ERR_APPROVAL_ABORTED` | Approval was aborted |
| `ERR_SESSION_BUSY` | Session is busy with another run |
| `ERR_SANDBOX_UNSUPPORTED` | Sandbox not supported on this platform |
| `ERR_SANDBOX_UNAVAILABLE` | Sandbox is configured but unavailable |
| `ERR_SANDBOX_COMMAND_TIMEOUT` | Sandbox command exceeded timeout |
| `ERR_SANDBOX_IDLE_TIMEOUT` | Sandbox exceeded idle timeout |
| `ERR_HOST_COMMAND_TIMEOUT` | Host command exceeded timeout |
| `ERR_HOST_IDLE_TIMEOUT` | Host exceeded idle timeout |

Use `execenv.ErrorCodeOf(err)` / `execenv.IsErrorCode(err, code)` for conditional handling.

## Runtime State Query

`runtime.RunState(ctx, RunStateRequest)` returns the latest lifecycle snapshot for a session:

| Field | Description |
|-------|-------------|
| `has_lifecycle` | Whether lifecycle events are present |
| `status` | Latest lifecycle status |
| `phase` | Lifecycle phase |
| `error` | Latest error message (if any) |
| `error_code` | Machine-readable error code (if any) |
| `event_id` | Source lifecycle event ID |
| `updated_at` | Lifecycle event timestamp |
