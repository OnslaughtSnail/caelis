# Plugin / Agent Assembly Boundary Plan

## Goal

Define the stable boundary between:

- app-owned plugin and agent discovery
- pure resolved assembly data
- runtime construction of ACP behavior objects

The SDK should consume resolved data, not host plugin lifecycle.

## Stable Rules

1. `sdk/plugin` is a data-contract package, not a plugin host
2. `ResolvedAssembly` is pure data only
3. app layers discover plugins, parse manifests, and resolve precedence
4. runtime layers build behavior objects from resolved assembly data
5. subagent and controller wiring must come from the same ACP agent source
6. external tools are not plugin contributions in this phase

## Current Runtime Shape

`sdk/runtime/local.Config` uses `Assembly` as the primary source of ACP
control-plane wiring.

When `Assembly.Agents` is populated, runtime construction must derive:

- ACP registry
- ACP subagent runner
- ACP controller manager

from that exact data source.

Explicit `Controllers` and `Subagents` remain only as compatibility escape
hatches for assembly-free embedding or tests.

Mixing:

- `Assembly.Agents`
- explicit `Controllers`
- explicit `Subagents`

is not allowed, because it creates two competing control-plane sources and
reintroduces the mismatch this boundary is meant to remove.

## Current Status

The following pieces are already in place:

- `sdk/plugin.AgentConfig`
- `sdk/plugin.ResolvedAssembly`
- `sdk/runtime/local.Config.Assembly`
- runtime-local assembly-driven ACP wiring

The remaining work in this area is not “more plugin system”.

The remaining work is:

- keep assembly consumption unambiguous
- share ACP wiring helpers across runtime and bridge code where useful
- avoid reintroducing compatibility-only config that weakens the boundary

## Non-Goals

This document does not authorize:

- dynamic Go plugin loading
- in-process third-party code plugins
- runtime-owned marketplace lifecycle
- arbitrary runtime interface bags inside `ResolvedAssembly`

## Active Direction

Use this boundary as the rule for current SDK cleanup:

- app resolves `ResolvedAssembly`
- runtime consumes `ResolvedAssembly`
- old production code may still exist, but should not define the target SDK shape
