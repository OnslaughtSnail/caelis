package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/tool/capability"
)

const (
	TaskToolName    = "TASK"
	defaultTaskWait = 5 * time.Second
)

type taskTool struct{}

func NewTaskTool() (Tool, error) {
	return &taskTool{}, nil
}

func (t *taskTool) Name() string {
	return TaskToolName
}

func (t *taskTool) Description() string {
	return "Control a long-running task created by BASH, DELEGATE, or future async tools. Use wait/status/write/cancel/list with task_id."
}

func (t *taskTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"wait", "status", "write", "cancel", "list"},
					"description": "wait for a fresh task snapshot, inspect status, send input, cancel a task, or list tracked tasks",
				},
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task handle returned by an earlier async-yielded tool call.",
				},
				"input": map[string]any{
					"type":        "string",
					"description": "Optional input text for action=write.",
				},
				"yield_time_ms": map[string]any{
					"type":        "integer",
					"description": "For action=wait or action=write, how long to wait before returning an updated task snapshot. Values greater than 0 wait that many milliseconds. If omitted or set to 0 or a negative value, TASK waits 5 seconds.",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func (t *taskTool) Capability() capability.Capability {
	return capability.Capability{Risk: capability.RiskLow}
}

func (t *taskTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	manager, ok := task.ManagerFromContext(ctx)
	if !ok || manager == nil {
		return nil, fmt.Errorf("tool: task manager is unavailable")
	}
	action := strings.TrimSpace(asStringArg(args, "action"))
	if action == "" {
		return nil, fmt.Errorf("tool: arg %q is required", "action")
	}
	req := task.ControlRequest{
		TaskID: strings.TrimSpace(asStringArg(args, "task_id")),
		Input:  asStringArg(args, "input"),
	}
	rawYield, yieldSpecified := args["yield_time_ms"]
	yieldSpecified = yieldSpecified && rawYield != nil
	yieldMS := asIntArg(args, "yield_time_ms")
	if (action == "wait" || action == "write") && (!yieldSpecified || yieldMS <= 0) {
		yieldMS = int(defaultTaskWait / time.Millisecond)
	}
	if yieldMS < 0 {
		yieldMS = 0
	}
	req.Yield = time.Duration(yieldMS) * time.Millisecond

	switch action {
	case "wait":
		if req.TaskID == "" {
			return nil, fmt.Errorf("tool: arg %q is required", "task_id")
		}
		startedAt := time.Now()
		snapshot, err := manager.Wait(ctx, req)
		if err != nil {
			return nil, err
		}
		result := AppendTaskSnapshotEvents(SnapshotResultMap(snapshot), snapshot)
		result["waited_ms"] = int(time.Since(startedAt).Milliseconds())
		return result, nil
	case "status":
		if req.TaskID == "" {
			return nil, fmt.Errorf("tool: arg %q is required", "task_id")
		}
		snapshot, err := manager.Status(ctx, req)
		if err != nil {
			return nil, err
		}
		return SnapshotResultMap(snapshot), nil
	case "write":
		if req.TaskID == "" {
			return nil, fmt.Errorf("tool: arg %q is required", "task_id")
		}
		startedAt := time.Now()
		snapshot, err := manager.Write(ctx, req)
		if err != nil {
			return nil, err
		}
		result := AppendTaskSnapshotEvents(SnapshotResultMap(snapshot), snapshot)
		result["waited_ms"] = int(time.Since(startedAt).Milliseconds())
		return result, nil
	case "cancel":
		if req.TaskID == "" {
			return nil, fmt.Errorf("tool: arg %q is required", "task_id")
		}
		snapshot, err := manager.Cancel(ctx, req)
		if err != nil {
			return nil, err
		}
		return AppendTaskSnapshotEvents(SnapshotResultMap(snapshot), snapshot), nil
	case "list":
		items, err := manager.List(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			out = append(out, SnapshotResultMap(item))
		}
		return map[string]any{
			"tasks": out,
			"count": len(out),
		}, nil
	default:
		return nil, fmt.Errorf("tool: invalid action %q", action)
	}
}
