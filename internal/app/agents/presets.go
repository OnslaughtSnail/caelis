package agents

import (
	"fmt"
	"sort"
	"strings"
)

var builtinCatalog = map[string]Descriptor{
	"pi": {
		ID:          "pi",
		Name:        "pi",
		Description: "Pi ACP agent.",
		Stability:   StabilityExperimental,
		Transport:   TransportACP,
		Command:     "npx",
		Args:        []string{"pi-acp"},
		Builtin:     true,
	},
	"openclaw": {
		ID:          "openclaw",
		Name:        "openclaw",
		Description: "OpenClaw ACP bridge.",
		Stability:   StabilityExperimental,
		Transport:   TransportACP,
		Command:     "openclaw",
		Args:        []string{"acp"},
		Builtin:     true,
	},
	"codex": {
		ID:          "codex",
		Name:        "codex",
		Description: "Codex CLI ACP adapter maintained by Zed.",
		Stability:   StabilityStable,
		Transport:   TransportACP,
		Command:     "npx",
		Args:        []string{"@zed-industries/codex-acp"},
		Builtin:     true,
	},
	"claude": {
		ID:          "claude",
		Name:        "claude",
		Description: "Claude Agent ACP adapter maintained by Zed.",
		Stability:   StabilityExperimental,
		Transport:   TransportACP,
		Command:     "npx",
		Args:        []string{"-y", "@zed-industries/claude-agent-acp"},
		Builtin:     true,
	},
	"gemini": {
		ID:          "gemini",
		Name:        "gemini",
		Description: "Gemini CLI ACP server.",
		Stability:   StabilityExperimental,
		Transport:   TransportACP,
		Command:     "gemini",
		Args:        []string{"--acp"},
		Builtin:     true,
	},
	"cursor": {
		ID:          "cursor",
		Name:        "cursor",
		Description: "Cursor CLI ACP server.",
		Stability:   StabilityExperimental,
		Transport:   TransportACP,
		Command:     "cursor-agent",
		Args:        []string{"acp"},
		Builtin:     true,
	},
	"copilot": {
		ID:          "copilot",
		Name:        "copilot",
		Description: "GitHub Copilot CLI ACP server.",
		Stability:   StabilityStable,
		Transport:   TransportACP,
		Command:     "copilot",
		Args:        []string{"--acp", "--stdio"},
		Builtin:     true,
	},
	"droid": {
		ID:          "droid",
		Name:        "droid",
		Description: "Factory Droid ACP server.",
		Stability:   StabilityExperimental,
		Transport:   TransportACP,
		Command:     "droid",
		Args:        []string{"exec", "--output-format", "acp"},
		Builtin:     true,
	},
	"kimi": {
		ID:          "kimi",
		Name:        "kimi",
		Description: "Kimi CLI ACP server.",
		Stability:   StabilityExperimental,
		Transport:   TransportACP,
		Command:     "kimi",
		Args:        []string{"acp"},
		Builtin:     true,
	},
	"opencode": {
		ID:          "opencode",
		Name:        "opencode",
		Description: "OpenCode ACP server.",
		Stability:   StabilityExperimental,
		Transport:   TransportACP,
		Command:     "npx",
		Args:        []string{"-y", "opencode-ai", "acp"},
		Builtin:     true,
	},
	"kiro": {
		ID:          "kiro",
		Name:        "kiro",
		Description: "Kiro CLI ACP server.",
		Stability:   StabilityExperimental,
		Transport:   TransportACP,
		Command:     "kiro-cli",
		Args:        []string{"acp"},
		Builtin:     true,
	},
	"kilocode": {
		ID:          "kilocode",
		Name:        "kilocode",
		Description: "Kilocode CLI ACP server.",
		Stability:   StabilityExperimental,
		Transport:   TransportACP,
		Command:     "npx",
		Args:        []string{"-y", "@kilocode/cli", "acp"},
		Builtin:     true,
	},
	"qwen": {
		ID:          "qwen",
		Name:        "qwen",
		Description: "Qwen Code ACP server.",
		Stability:   StabilityExperimental,
		Transport:   TransportACP,
		Command:     "qwen",
		Args:        []string{"--acp"},
		Builtin:     true,
	},
}

func KnownBuiltins() []Descriptor {
	out := make([]Descriptor, 0, len(builtinCatalog))
	for _, preset := range builtinCatalog {
		out = append(out, cloneDescriptor(preset))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func LookupBuiltin(id string) (Descriptor, bool) {
	preset, ok := builtinCatalog[strings.TrimSpace(strings.ToLower(id))]
	if !ok {
		return Descriptor{}, false
	}
	return cloneDescriptor(preset), true
}

func ResolveDescriptor(d Descriptor) (Descriptor, error) {
	d = normalizeDescriptor(d)
	if d.Transport == TransportSelf {
		return d, nil
	}
	if strings.TrimSpace(d.Command) == "" {
		return Descriptor{}, fmt.Errorf("agents: acp agent %q requires a command", d.ID)
	}
	return d, nil
}

func cloneDescriptor(d Descriptor) Descriptor {
	d.Args = append([]string(nil), d.Args...)
	d.Env = cloneStringMap(d.Env)
	return d
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
