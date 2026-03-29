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
	return "Start a new ACP child session for bounded delegated work. agent accepts self or any configured ACP agent id such as codex, copilot, or gemini. If the child is still running when yield_time_ms elapses, the result includes task_id; continue with TASK wait, and once that child session reaches completed you can use TASK write to start another turn in the same child session."
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
					"description": "Optional target agent id. Use self or any configured ACP agent id such as codex, copilot, or gemini. Defaults to the configured default agent, then self.",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "Task prompt for the child session.",
				},
				"yield_time_ms": map[string]any{
					"type":        "integer",
					"description": "Optional per-call wait for this SPAWN before returning in milliseconds. Defaults to 30000. If the child is still running when this wait expires, the result includes task_id and you continue with TASK wait.",
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Optional total timeout for the child task in milliseconds. This is independent from yield_time_ms. If that total timeout is reached, the returned payload is still a task snapshot; inspect state and msg/output to see the terminal timeout result.",
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
	for _, legacy := range []string{"session", "session_id", "new_session", "yield_seconds", "timeout_seconds"} {
		if _, ok := args[legacy]; ok {
			return nil, fmt.Errorf("tool: arg %q is no longer supported", legacy)
		}
	}
	rawYield, yieldSpecified := args["yield_time_ms"]
	yieldSpecified = yieldSpecified && rawYield != nil
	yieldMS := asIntArg(args, "yield_time_ms")
	if !yieldSpecified || yieldMS <= 0 {
		yieldMS = int(defaultSpawnYield / time.Millisecond)
	}
	timeoutMS := asIntArg(args, "timeout_ms")
	var timeout time.Duration
	if timeoutMS > 0 {
		timeout = time.Duration(timeoutMS) * time.Millisecond
	}
	snapshot, err := manager.StartSpawn(ctx, task.SpawnStartRequest{
		Agent:   agentName,
		Prompt:  promptText,
		Yield:   time.Duration(yieldMS) * time.Millisecond,
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
