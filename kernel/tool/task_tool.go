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
	return "Control async tasks from BASH or SPAWN. state is the task lifecycle status. Use wait to check progress, write to send bash stdin or continue a completed spawn session, cancel to stop a task, and list to inspect recent tasks."
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
					"enum":        []string{"wait", "write", "cancel", "list"},
					"description": "Task control action.",
				},
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task handle from an earlier async tool call.",
				},
				"input": map[string]any{
					"type":        "string",
					"description": "For action=write: send stdin to a running BASH task, or send a follow-up prompt to a completed SPAWN task.",
				},
				"yield_time_ms": map[string]any{
					"type":        "integer",
					"description": "Optional wait before returning. Defaults to 5000 for wait/write.",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Pagination offset for action=list.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Pagination limit for action=list. Defaults to 10 and is capped at 50.",
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
	offset := asIntArg(args, "offset")
	limit := asIntArg(args, "limit")
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
		snapshot, err := manager.Wait(ctx, req)
		if err != nil {
			return nil, err
		}
		return taskSnapshotResult(snapshot, true), nil
	case "write":
		if req.TaskID == "" {
			return nil, fmt.Errorf("tool: arg %q is required", "task_id")
		}
		snapshot, err := manager.Write(ctx, req)
		if err != nil {
			return nil, err
		}
		return taskSnapshotResult(snapshot, true), nil
	case "cancel":
		if req.TaskID == "" {
			return nil, fmt.Errorf("tool: arg %q is required", "task_id")
		}
		snapshot, err := manager.Cancel(ctx, req)
		if err != nil {
			return nil, err
		}
		return taskSnapshotResult(snapshot, false), nil
	case "list":
		items, err := manager.List(ctx)
		if err != nil {
			return nil, err
		}
		total := len(items)
		if offset < 0 {
			offset = 0
		}
		if limit <= 0 {
			limit = 10
		}
		if limit > 50 {
			limit = 50
		}
		if offset > len(items) {
			offset = len(items)
		}
		items = items[offset:]
		if len(items) > limit {
			items = items[:limit]
		}
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			out = append(out, CompactTaskListItem(item))
		}
		return map[string]any{
			"tasks":  out,
			"total":  total,
			"offset": offset,
			"limit":  limit,
		}, nil
	default:
		return nil, fmt.Errorf("tool: invalid action %q", action)
	}
}

func taskSnapshotResult(snapshot task.Snapshot, includeEvents bool) map[string]any {
	result := SnapshotResultMap(snapshot)
	if includeEvents && snapshot.Running && shouldExposeTaskEvents(snapshot) {
		if events := PublicTaskActionEvents(snapshot); len(events) > 0 {
			result["events"] = events
		}
	}
	return AppendTaskSnapshotEvents(result, snapshot)
}

func shouldExposeTaskEvents(snapshot task.Snapshot) bool {
	if snapshot.Kind != task.KindBash {
		return false
	}
	switch snapshot.State {
	case task.StateWaitingInput, task.StateWaitingApproval:
		return true
	default:
		return false
	}
}
