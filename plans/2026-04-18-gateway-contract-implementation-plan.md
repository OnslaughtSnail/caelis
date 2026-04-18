# Gateway Contract Implementation Plan

Goal:
build the Unified Gateway bottom-up from a stable public contract, then
implement the remaining gateway behavior before returning to TUI work.

Development order:

1. SDK runtime semantics
   - keep `stream` optional per turn
   - preserve ACP-compatible live-update semantics
   - cover real provider e2e for live turn behavior

2. Gateway public contract
   - define stable root-package service interfaces
   - keep root package as the only adapter-facing import
   - keep `core` and `host` internal implementation boundaries explicit

3. Gateway implementation
   - session / turn / replay / binding / control-plane services
   - surface-aware request policy
   - host lifecycle and remote session routing
   - focused tests for core, host, app composition root, and adapter consumers

4. Adapter and surface adoption
   - headless
   - TUI runtime bridge
   - later daemon / channel adapters

Current slice:

- upgrade `docs/unified_gateway_foundation_spec.md` into the authoritative
  interface contract
- define root interfaces:
  - `SessionService`
  - `TurnService`
  - `ControlPlaneService`
  - `CoreService`
  - `HostService`
- add compile-time and focused tests that `*gateway.Gateway` and
  `*gateway.Host` satisfy the intended service slices
- then continue gateway implementation in service-slice order:
  1. request policy
  2. session and replay
  3. turn and handle
  4. control-plane
  5. host

Acceptance for the current slice:

- the spec explicitly defines the public gateway contract already promised by
  product docs
- adapters can depend only on the root `gateway` package contract
- the root package exposes stable interface slices rather than one monolithic
  concrete type only
- tests prove current concrete `Gateway` and `Host` satisfy those interface
  slices
- no new legacy imports appear
