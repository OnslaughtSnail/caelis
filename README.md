# caelis

`caelis` is a terminal-first agent runtime built around one local stack: `sdk -> gateway -> app/gatewayapp -> adapters -> tui/headless`.

## What It Does

- Runs an interactive Bubble Tea TUI when started from a TTY with no prompt input.
- Runs a headless single-shot turn when given `-p` or piped stdin.
- Persists sessions and app config under `~/.caelis` by default.
- Routes command execution through approval-aware sandboxing in `default` mode or direct host execution in `full_control`.
- Assembles prompts from built-in instructions, workspace `AGENTS.md`, global `~/.agents/AGENTS.md`, and discovered local skills.

## Current Layout

- `cmd/cli`: flat-flag CLI entrypoint. Chooses TUI or headless mode; there are no `console` or `acp` subcommands.
- `sdk/`: reusable foundation for runtime, session, model/provider, tool, sandbox, delegation, plugin, and stream contracts. Root packages stay contract-first; concrete implementations live in subpackages such as `sdk/runtime/local`, `sdk/session/file`, and `sdk/tool/builtin`.
- `gateway/`: product-facing API surface. `gateway/core` owns session/turn/control-plane orchestration and `gateway/host` owns host and remote-session lifecycle.
- `app/gatewayapp`: local composition root that assembles the SDK-backed runtime, gateway resolver, prompt assembly, config store, and session store.
- `gateway/adapter/headless`: one-shot headless adapter over the root `gateway` contract.
- `gateway/adapter/tui/runtime`: gateway-to-TUI driver bridge.
- `tui/`: presentation layer, including `tui/tuiapp`, `tui/tuikit`, `tui/modelcatalog`, and `tui/tuidiff`.
- `acp/` and `acpbridge/`: ACP schema, transport, and bridge helpers that stay adjacent to the local stack rather than defining the CLI entrypoint.

Architecture overview: [docs/architecture.md](docs/architecture.md)
Deeper design documents: [docs/current_sdk_foundation_scope.md](docs/current_sdk_foundation_scope.md), [docs/unified_gateway_foundation_spec.md](docs/unified_gateway_foundation_spec.md)

## Development

Caelis currently requires Go `1.25.1` as declared in `go.mod`.

```bash
make quality
make test
make build
```

- `make quality`: runs formatting check, `golangci-lint`, tests, `go vet`, and `go build ./...`
- `make test`: runs `go test ./...`
- `make build`: runs `go build ./...`

## CLI Entry

`cmd/cli` uses one flat flag set. Run `go run ./cmd/cli -h` to inspect the current flags.

Important flags include:

- `-p`: single-shot prompt text
- `-format`: `text` or `json` for headless output
- `-session`, `-store-dir`, `-workspace-key`, `-workspace-cwd`
- `-permission-mode`: `default` or `full_control`
- `-provider`, `-api`, `-model`, `-base-url`, `-token`, `-token-env`
- `-model-alias`, `-context-window`, `-max-output-tokens`
- `-interactive`: force the TUI path even when stdin is piped

## Quick Start

Interactive TUI:

```bash
go run ./cmd/cli \
  -provider openai \
  -model gpt-5 \
  -permission-mode default
```

Headless single-shot:

```bash
go run ./cmd/cli \
  -provider openai \
  -model gpt-5 \
  -permission-mode default \
  -p "Summarize the repository layout."
```

Headless from stdin:

```bash
printf '%s\n' "Summarize the repository layout." | go run ./cmd/cli \
  -provider openai \
  -model gpt-5 \
  -format text
```

If no model is configured yet, start the TUI and use `/connect`.
The TUI opens a guided wizard for provider, base URL, API key or `env:YOUR_API_KEY`, and model selection.

## Runtime And Permissions

`caelis` currently exposes one CLI permission switch:

- `-permission-mode default`: use the local sandbox runtime when available and require approval for host escalation.
- `-permission-mode full_control`: execute directly on the host.

Sandbox backend selection is resolved by the local stack/runtime. The CLI does not currently expose a top-level `-sandbox-type` flag.

## Sessions And Interaction

Interactive sessions are stored under `~/.caelis/sessions` by default. The TUI starts a fresh session unless `-session` is provided.

Current built-in slash commands:

- `/help`
- `/agent list`, `/agent add <builtin>`, `/agent use <agent|local>`, `/agent remove <agent>`
- dynamic ACP child commands for registered agents, for example `/codex <prompt>` and follow-up `@handle <prompt>`
- `/connect`
- `/model use <alias>` or `/model del <alias>`
- `/sandbox [auto|seatbelt|bwrap|landlock]`
- `/status`
- `/new`
- `/resume`
- `/compact`
- `/exit`
- `/quit`

Notes:

- `/agent` manages ACP-backed participants and main-controller handoff without bypassing the gateway control plane. `/agent list` shows attached participants, while `/agent add` completion shows available ACP agents.
- `/connect` is the recommended entrypoint for configuring providers and models inside the TUI.
- `/status` shows the current provider, model alias, session, sandbox route, workspace, and store directory without printing API keys or auth headers.
- `/btw` is intentionally hidden in the default TUI until that overlay flow is fully supported.
- Input completion is available for:
  - `/agent`: available ACP agent names for `add`, attached agent names for `handoff`/`use`, and attached participant ids for `remove`.
  - `#path`: workspace-relative file and directory paths, with shallow recursive search, fuzzy/prefix matching, common noise directories skipped, and a hard result limit.
  - `$skill`: discovered skills from `~/.agents/skills`, `<workspace>/.agents/skills`, and `<workspace>/skills`, showing skill name plus summary/path metadata.
  - `/resume`: recent sessions, ordered by most recently updated first, showing title, model, workspace, and session id.
- `@mention` completion is intentionally a documented no-op for now. The TUI returns an empty list until a stable participant/agent registry exists.

## Release And Packaging

- Go release archives are produced from `./cmd/cli` by GoReleaser.
- npm publishes a thin launcher package from `npm/` plus platform-specific binary packages from `npm/packages/*`.
- The npm wrapper is intentionally file-whitelisted so published artifacts do not include workspace files such as `.env`, `.git`, `.superpowers`, local caches, or temporary build outputs.

Local dry run:

```bash
make release-dry-run
```

## npm Package

The npm package lives under `npm/` and publishes as `@onslaughtsnail/caelis`.

```bash
npm i -g @onslaughtsnail/caelis
```

Supported platforms: macOS/Linux (`x64`, `arm64`).
