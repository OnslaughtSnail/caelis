package agents

import (
	"fmt"
	"sort"
	"strings"
)

var registryPresets = map[string]Descriptor{
	"claude-acp": {
		ID:          "claude-acp",
		Name:        "Claude Agent",
		Description: "Claude Agent ACP adapter maintained by Zed.",
		Type:        TypeRegistry,
		Transport:   TransportACP,
		Command:     "npx",
		Args:        []string{"-y", "@zed-industries/claude-agent-acp"},
	},
	"codex-acp": {
		ID:          "codex-acp",
		Name:        "Codex CLI",
		Description: "Codex ACP adapter maintained by Zed.",
		Type:        TypeRegistry,
		Transport:   TransportACP,
		Command:     "npx",
		Args:        []string{"-y", "@zed-industries/codex-acp"},
	},
	"gemini": {
		ID:          "gemini",
		Name:        "Gemini CLI",
		Description: "Gemini CLI in ACP mode.",
		Type:        TypeRegistry,
		Transport:   TransportACP,
		Command:     "gemini",
		Args:        []string{"--experimental-acp"},
	},
	"github-copilot-cli": {
		ID:          "github-copilot-cli",
		Name:        "GitHub Copilot CLI",
		Description: "GitHub Copilot CLI ACP server mode.",
		Type:        TypeRegistry,
		Transport:   TransportACP,
		Command:     "copilot",
		Args:        []string{"--acp"},
	},
}

func KnownRegistryPresets() []Descriptor {
	out := make([]Descriptor, 0, len(registryPresets))
	for _, preset := range registryPresets {
		out = append(out, cloneDescriptor(preset))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func LookupRegistryPreset(id string) (Descriptor, bool) {
	preset, ok := registryPresets[strings.TrimSpace(strings.ToLower(id))]
	if !ok {
		return Descriptor{}, false
	}
	return cloneDescriptor(preset), true
}

func ResolveDescriptor(d Descriptor) (Descriptor, error) {
	d.ID = strings.TrimSpace(d.ID)
	d.Name = strings.TrimSpace(d.Name)
	d.Description = strings.TrimSpace(d.Description)
	d.Type = strings.TrimSpace(strings.ToLower(d.Type))
	d.Endpoint = strings.TrimSpace(d.Endpoint)
	d.Command = strings.TrimSpace(d.Command)
	d.WorkDir = strings.TrimSpace(d.WorkDir)
	d.Args = append([]string(nil), d.Args...)
	d.Env = cloneStringMap(d.Env)
	if d.Transport == "" {
		d.Transport = TransportACP
	}
	if d.Type != TypeRegistry {
		return d, nil
	}
	preset, ok := LookupRegistryPreset(d.ID)
	if !ok {
		return Descriptor{}, fmt.Errorf("agents: unknown registry preset %q", d.ID)
	}
	if d.Name == "" {
		d.Name = preset.Name
	}
	if d.Description == "" {
		d.Description = preset.Description
	}
	if d.Transport == "" {
		d.Transport = preset.Transport
	}
	if d.Endpoint == "" {
		d.Endpoint = preset.Endpoint
	}
	if d.Command == "" {
		d.Command = preset.Command
	}
	if len(d.Args) == 0 {
		d.Args = append([]string(nil), preset.Args...)
	}
	if len(d.Env) == 0 && len(preset.Env) > 0 {
		d.Env = cloneStringMap(preset.Env)
	}
	if d.WorkDir == "" {
		d.WorkDir = preset.WorkDir
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
