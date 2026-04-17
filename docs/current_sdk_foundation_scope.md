# Current SDK Foundation Scope

## Status

This document is the authority for the current refactor phase.

The goal of the current phase is:

- make `sdk/` the stable foundation for the next Caelis rebuild
- keep old `kernel` / `internal/app` / `cmd/cli` code as behavior reference only
- avoid starting production-path migration until the SDK foundation is stable

The goal of the current phase is not:

- switching the production main path to `sdk`
- incrementally de-kernelizing the current CLI
- preserving old roadmap branches and intermediate analysis docs

## What Must Be True In This Phase

1. `sdk/` is a real standalone boundary with no dependency on `kernel`, `internal/app`, or `cmd/cli`
2. current SDK contracts are stable enough for upper-layer rewrite work
3. internal SDK assembly and ACP wiring do not keep ambiguous or misleading compatibility paths
4. docs stay small and current; outdated roadmap or investigation notes should be deleted instead of preserved

## Current Engineering Rules

### 1. Production-path migration is deferred on purpose

The current repo still runs through the old production path.

That is expected in this phase.

Architecture review should therefore separate:

- whether the SDK foundation is internally sound
- whether the old production path has already switched

Only the first question is in scope right now.

### 2. Old code is reference material, not target architecture

`kernel`, `internal/app`, and `cmd/cli` may still contain the only complete
product behavior.

They remain useful as:

- behavior reference
- compatibility reference
- regression oracle

They are not the target architecture for the next-stage rebuild.

### 3. SDK cleanup should prioritize misleading boundaries

Current-stage SDK work should prefer:

- removing dead or no-op config surface
- rejecting ambiguous wiring
- collapsing duplicated ACP plumbing
- keeping runtime assembly driven by pure resolved data

Current-stage SDK work should avoid:

- speculative abstraction
- optional compatibility knobs without active call sites
- carrying multiple “equivalent” control-plane sources forever

## Active Documents

Only the following documents should remain active in `docs/` for this phase:

1. `docs/current_sdk_foundation_scope.md`
2. `docs/plugin_agent_assembly_boundary_plan.md`
3. `docs/session_controller_event_model.md`
4. `docs/sdk_compaction_chain_audit_and_design.md`

If a new document is added, another outdated document should usually be removed
so the set stays small and non-conflicting.

## Immediate Next Step

The next useful work in this phase is:

- continue tightening SDK-internal boundaries
- keep docs aligned with the current phase
- avoid spending effort on rewriting upper layers before the SDK foundation is settled
