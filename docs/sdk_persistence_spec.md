# SDK Persistence Spec

## Status

The SDK already has a runnable minimum kernel:

- `sdk/model`
- `sdk/session`
- `sdk/runtime/local`
- `sdk/tool`
- `sdk/sandbox`
- `sdk/policy`
- `sdk/acp`

It already supports:

- real provider e2e
- real `acpx` ACP e2e
- tool loop
- plan loop
- approval -> `request_permission` -> continue

The highest-priority remaining foundation gap is durable session persistence.

## Goals

The first durable session implementation should optimize for:

- auditability: persisted data should be readable and explainable
- minimality: only necessary durable data should be stored
- cohesion: one session should live in one file
- portability: the format should remain easy to project into ACP and future app layers
- compatibility: the durable store should implement the current `sdk/session` contracts without pulling runtime concerns down into persistence

## Non-Goals

This phase does not try to solve:

- async task persistence
- checkpoint/compaction
- full replay of subagent transcripts into parent sessions
- app-owned config or mode persistence
- rich indexing/search

## Durable Unit

The durable unit is one session file.

Recommended layout:

```text
<root>/
  <session-id>.json
```

There should be no companion `meta.json`, `events.jsonl`, `state.json`, or sidecar files for the same session.

Listing can scan the directory, but each session remains high-cohesion in a single durable document.

## Durable Document

Each session file should contain one compact JSON document:

```json
{
  "kind": "caelis.sdk.session",
  "version": 1,
  "session": { "...": "..." },
  "events": [ "... durable events only ..." ],
  "state": { "...": "..." }
}
```

### Persisted `session`

Persist:

- `SessionRef`
- `cwd`
- `title`
- `metadata`
- `controller`
- `participants`
- `created_at`
- `updated_at`

Do not split session metadata into another file.

### Persisted `events`

Persist only durable events:

- canonical history events
- mirror events if the caller emits them as durable transcript anchors

Do not persist:

- UI-only events
- overlay-only events
- transient notices

This keeps the file auditable without turning it into a dump of runtime-only noise.

### Persisted `state`

Persist the current session state snapshot as one object.

The state is the durable source of truth for:

- mode-like runtime state
- plan snapshot
- continuation metadata
- future approval/task resumability

## Subagent / ACP Session Anchors

Subagents that run through ACP are separate sessions.

The parent session should persist only an anchor in its participant bindings. The parent file must not inline the child session transcript.

The participant anchor should contain at least:

- participant id
- participant kind
- participant role
- source
- delegation id
- controller ref
- child session id

This is enough for:

- audit
- rendering an attached/delegated participant in the parent session
- resuming incremental work against the child session later

It is intentionally not enough to reconstruct the entire child transcript from the parent file.

## Resume Expectations

Resume does not require recalling all subagent events into the parent session.

Resume only requires:

- the parent session durable state
- the parent session durable event log
- participant anchors that reference child sessions
- the child session's own durable file

Future runtime/subagent code can continue incrementally by reopening the child session directly.

## Store Contract Expectations

The first file-backed store should stay API-compatible with the current in-memory store:

- `GetOrCreate`
- `Get`
- `List`
- `AppendEvent`
- `Events`
- `BindController`
- `PutParticipant`
- `RemoveParticipant`
- `SnapshotState`
- `ReplaceState`
- `UpdateState`

The store may persist fewer event classes than the in-memory store, but it should preserve the same durable semantics.

## Write Strategy

The file-backed store should use atomic rewrite semantics:

1. read current document
2. apply mutation
3. write a temp file in the same directory
4. rename into place

This keeps the single-file contract while remaining easy to inspect and debug.

## Priority After Persistence

After durable session persistence, the recommended order remains:

1. `session/load` + durable replay plumbing
2. task runtime / async execution
3. subagent / delegation
4. checkpoint / compact
5. app-owned mode/config providers

