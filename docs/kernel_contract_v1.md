# Kernel Contract v1

This document defines the runtime event and error contract consumed by upper layers (CLI/Web/API).

## Event Envelope

All runtime events follow `session.Event`:

- `id`: event id
- `session_id`: session id
- `time`: event timestamp
- `message`: model message payload
- `meta`: extensible metadata map

## Contract Metadata Keys

- `meta.kind`: event kind
- `meta.contract_version`: contract version string (`v1`)

### Lifecycle Events

Lifecycle events use:

- `meta.kind = "lifecycle"`
- `meta.contract_version = "v1"`
- `meta.lifecycle.status`
- `meta.lifecycle.phase`
- optional `meta.lifecycle.error`
- optional `meta.lifecycle.error_code`

Lifecycle statuses:

- `running`
- `waiting_approval`
- `interrupted`
- `failed`
- `completed`

### Model Visibility

Runtime/tool metadata is for UI/orchestration and should not be forwarded to model context.
`llmagent` strips `metadata` and `_ui_*` fields before sending tool responses back to model.

## Error Code Contract

Machine-readable error codes (from `kernel/execenv`) are stable identifiers for programmatic handling:

- `ERR_APPROVAL_REQUIRED`
- `ERR_APPROVAL_ABORTED`
- `ERR_SESSION_BUSY`
- `ERR_SANDBOX_UNSUPPORTED`
- `ERR_SANDBOX_UNAVAILABLE`
- `ERR_SANDBOX_COMMAND_TIMEOUT`
- `ERR_SANDBOX_IDLE_TIMEOUT`
- `ERR_HOST_COMMAND_TIMEOUT`
- `ERR_HOST_IDLE_TIMEOUT`

Use `execenv.ErrorCodeOf(err)` / `execenv.IsErrorCode(err, code)` for branching instead of string matching.

## Runtime State Query

`runtime.RunState(ctx, RunStateRequest)` returns latest lifecycle snapshot for a session:

- `has_lifecycle`: whether lifecycle events are present
- `status`: latest lifecycle status
- `phase`: lifecycle phase
- `error`: latest lifecycle error message (if any)
- `error_code`: machine-readable error code (if any)
- `event_id`: source lifecycle event id
- `updated_at`: lifecycle event timestamp
