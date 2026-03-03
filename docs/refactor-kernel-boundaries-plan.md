# Kernel Boundary Refactor Plan

## Goals
- Align security policy with sandbox-first execution model.
- Keep local sandbox tools smooth, but restore explicit trust boundary for external/unknown tools.
- Remove compaction config fields that no longer affect runtime behavior.

## Risks
- Breaking API change in `runtime.CompactionConfig`.
- Default approval prompts may increase for previously bypassed external tools.

## Checklist
- [x] Update security baseline to require authorization for `MCP__*` tools.
- [x] Update security baseline to require authorization for unknown tool names.
- [x] Keep local core tools (`READ/LIST/GLOB/STAT/SEARCH/WRITE/PATCH/BASH/...`) auto-allowed.
- [x] Remove dead `PreserveRecentTurns` from compaction config.
- [x] Update kernel tests to match the new trust boundary and compaction contract.
- [x] Run full repository tests and verify no non-kernel regressions.
