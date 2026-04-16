package spawn

import (
	"context"
	"fmt"
	"strings"

	sdkdelegation "github.com/OnslaughtSnail/caelis/sdk/delegation"
	sdktool "github.com/OnslaughtSnail/caelis/sdk/tool"
)

const ToolName = "SPAWN"

type Tool struct {
	agents []sdkdelegation.Agent
}

func New(agents []sdkdelegation.Agent) Tool {
	out := make([]sdkdelegation.Agent, 0, len(agents))
	for _, one := range agents {
		normalized := sdkdelegation.NormalizeAgent(one)
		if normalized.Name == "" {
			continue
		}
		out = append(out, normalized)
	}
	return Tool{agents: out}
}

func (t Tool) Definition() sdktool.Definition {
	props := map[string]any{
		"agent": map[string]any{
			"type":        "string",
			"description": agentDescription(t.agents),
		},
		"prompt": map[string]any{
			"type":        "string",
			"description": "The sub-task for the selected agent. Keep it specific and self-contained.",
		},
		"yield_time_ms": map[string]any{
			"type":        "integer",
			"description": "Optional wait window before control returns while the spawned task continues in the background.",
		},
	}
	if enum := agentNames(t.agents); len(enum) > 0 {
		props["agent"].(map[string]any)["enum"] = enum
	}
	return sdktool.Definition{
		Name:        ToolName,
		Description: "Delegate a sub-task to one available ACP agent. Use TASK wait or cancel with the returned task_id after it yields.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           props,
			"required":             []string{"prompt"},
			"additionalProperties": false,
		},
	}
}

func (Tool) Call(context.Context, sdktool.Call) (sdktool.Result, error) {
	return sdktool.Result{}, fmt.Errorf("tool: SPAWN must be executed by the runtime wrapper")
}

func agentNames(agents []sdkdelegation.Agent) []string {
	out := make([]string, 0, len(agents))
	for _, one := range agents {
		if name := strings.TrimSpace(one.Name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func agentDescription(agents []sdkdelegation.Agent) string {
	if len(agents) == 0 {
		return "Optional ACP agent name. Omit to use the default self agent."
	}
	parts := make([]string, 0, len(agents))
	for _, one := range agents {
		name := strings.TrimSpace(one.Name)
		if name == "" {
			continue
		}
		if desc := strings.TrimSpace(one.Description); desc != "" {
			parts = append(parts, name+": "+desc)
			continue
		}
		parts = append(parts, name)
	}
	if len(parts) == 0 {
		return "Optional ACP agent name. Omit to use the default self agent."
	}
	return "Optional ACP agent name. Available agents: " + strings.Join(parts, "; ") + ". Omit to use the default self agent."
}

var _ sdktool.Tool = Tool{}
