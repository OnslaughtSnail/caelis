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

type selfSpawnTool struct{}

func NewSelfSpawnTool() (tool.Tool, error) {
	return &selfSpawnTool{}, nil
}

func (t *selfSpawnTool) Name() string { return tool.SpawnToolName }

func (t *selfSpawnTool) Description() string {
	return "Spawn a self child session to execute a focused task."
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
					"description": "Target agent identifier from the configured agent registry. Defaults to \"self\".",
				},
				"task": map[string]any{
					"type":        "string",
					"description": "The task prompt to send to the spawned self child session.",
				},
				"yield_seconds": map[string]any{
					"type":        "integer",
					"description": "Optional wait time in seconds before yielding control back. Defaults to 30 if omitted or non-positive.",
				},
			},
			"required":             []string{"task"},
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
	taskText := strings.TrimSpace(asStringArg(args, "task"))
	if taskText == "" {
		return nil, fmt.Errorf("tool: arg %q is required", "task")
	}
	agentName := strings.TrimSpace(asStringArg(args, "agent"))
	if agentName == "" {
		agentName = "self"
	}
	rawYield, yieldSpecified := args["yield_seconds"]
	yieldSpecified = yieldSpecified && rawYield != nil
	yieldSeconds := asIntArg(args, "yield_seconds")
	if !yieldSpecified || yieldSeconds <= 0 {
		yieldSeconds = int(defaultSpawnYield / time.Second)
	}
	snapshot, err := manager.StartDelegate(ctx, task.DelegateStartRequest{
		Agent: agentName,
		Task:  taskText,
		Yield: time.Duration(yieldSeconds) * time.Second,
		Kind:  task.KindSpawn,
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
