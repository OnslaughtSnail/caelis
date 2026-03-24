package runservice

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	"github.com/OnslaughtSnail/caelis/kernel/tool/capability"
)

const defaultSpawnYield = 30 * time.Second

type selfSpawnTool struct {
	defaultAgent string
}

func NewSelfSpawnTool(defaultAgent string) (tool.Tool, error) {
	return &selfSpawnTool{defaultAgent: strings.TrimSpace(defaultAgent)}, nil
}

func (t *selfSpawnTool) Name() string { return tool.SpawnToolName }

func (t *selfSpawnTool) Description() string {
	return "Create a new ACP child session with a prompt."
}

func (t *selfSpawnTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent": map[string]any{
					"type":        "string",
					"description": "Target agent identifier. Defaults to defaultAgent, then falls back to \"self\".",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "Prompt to send to the child session.",
				},
				"yield_seconds": map[string]any{
					"type":        "integer",
					"description": "Optional wait time in seconds before yielding control back. Defaults to 30 if omitted or non-positive.",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Optional timeout for the delegated prompt in seconds.",
				},
			},
			"required":             []string{"prompt"},
			"additionalProperties": false,
		},
	}
}

func (t *selfSpawnTool) Capability() capability.Capability {
	return capability.Capability{Risk: capability.RiskLow}
}

func (t *selfSpawnTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	manager, ok := task.ManagerFromContext(ctx)
	if !ok || manager == nil {
		return nil, fmt.Errorf("tool: task manager is unavailable")
	}
	promptText := strings.TrimSpace(asStringArg(args, "prompt"))
	if promptText == "" {
		return nil, fmt.Errorf("tool: arg %q is required", "prompt")
	}
	agentName := strings.TrimSpace(asStringArg(args, "agent"))
	if agentName == "" {
		agentName = strings.TrimSpace(t.defaultAgent)
	}
	if agentName == "" {
		agentName = "self"
	}
	for _, legacy := range []string{"session", "session_id", "new_session"} {
		if _, ok := args[legacy]; ok {
			return nil, fmt.Errorf("tool: arg %q is no longer supported; SPAWN only creates new child sessions, use TASK write with the SPAWN task_id to continue an existing child session", legacy)
		}
	}
	rawYield, yieldSpecified := args["yield_seconds"]
	yieldSpecified = yieldSpecified && rawYield != nil
	yieldSeconds := asIntArg(args, "yield_seconds")
	if !yieldSpecified || yieldSeconds <= 0 {
		yieldSeconds = int(defaultSpawnYield / time.Second)
	}
	timeoutSeconds := asIntArg(args, "timeout_seconds")
	var timeout time.Duration
	if timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}
	snapshot, err := manager.StartSpawn(ctx, task.SpawnStartRequest{
		Agent:   agentName,
		Prompt:  promptText,
		Yield:   time.Duration(yieldSeconds) * time.Second,
		Timeout: timeout,
		Kind:    task.KindSpawn,
	})
	if err != nil {
		return nil, err
	}
	return tool.AppendTaskSnapshotEvents(tool.SnapshotResultMap(snapshot), snapshot), nil
}

func asStringArg(args map[string]any, key string) string {
	if len(args) == 0 {
		return ""
	}
	raw, ok := args[key]
	if !ok || raw == nil {
		return ""
	}
	if text, ok := raw.(string); ok {
		return text
	}
	return fmt.Sprint(raw)
}

func asIntArg(args map[string]any, key string) int {
	if len(args) == 0 {
		return 0
	}
	raw, ok := args[key]
	if !ok || raw == nil {
		return 0
	}
	switch value := raw.(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}
