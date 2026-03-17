package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool/capability"
)

const (
	DelegateTaskToolName = "DELEGATE"
	defaultDelegateYield = 30 * time.Second
)

type delegateTaskTool struct{}

func NewDelegateTask() (Tool, error) {
	return &delegateTaskTool{}, nil
}

func (t *delegateTaskTool) Name() string {
	return DelegateTaskToolName
}

func (t *delegateTaskTool) Description() string {
	return "Delegate a focused task into a child agent run. Use yield_time_ms to allow long tasks to continue in the background."
}

func (t *delegateTaskTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "The task prompt to send to the child agent.",
				},
				"yield_time_ms": map[string]any{
					"type":        "integer",
					"description": "Optional wait time before yielding control back. Values greater than 0 wait that many milliseconds. If omitted or set to 0 or a negative value, DELEGATE waits 30 seconds before returning a task_id if the child task is still running.",
				},
			},
			"required":             []string{"task"},
			"additionalProperties": false,
		},
	}
}

func (t *delegateTaskTool) Capability() capability.Capability {
	return capability.Capability{Risk: capability.RiskLow}
}

func (t *delegateTaskTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	manager, ok := task.ManagerFromContext(ctx)
	if !ok || manager == nil {
		return nil, fmt.Errorf("tool: task manager is unavailable")
	}
	taskText := strings.TrimSpace(asStringArg(args, "task"))
	if taskText == "" {
		return nil, fmt.Errorf("tool: arg %q is required", "task")
	}
	rawYield, yieldSpecified := args["yield_time_ms"]
	yieldSpecified = yieldSpecified && rawYield != nil
	yieldMS := asIntArg(args, "yield_time_ms")
	if !yieldSpecified || yieldMS <= 0 {
		yieldMS = int(defaultDelegateYield / time.Millisecond)
	}
	snapshot, err := manager.StartDelegate(ctx, task.DelegateStartRequest{
		Task:  taskText,
		Yield: time.Duration(yieldMS) * time.Millisecond,
	})
	if err != nil {
		return nil, err
	}
	result := SnapshotResultMap(snapshot)
	return AppendTaskSnapshotEvents(result, snapshot), nil
}

func AppendTaskSnapshotEvents(result map[string]any, snapshot task.Snapshot) map[string]any {
	if result == nil {
		result = map[string]any{}
	}
	if snapshot.TaskID == "" {
		return result
	}
	running := snapshotIsActive(snapshot)
	label := strings.ToUpper(strings.TrimSpace(string(snapshot.Kind)))
	if label == "" {
		label = "TASK"
	}
	if snapshot.Output.Stdout != "" {
		result = taskstream.AppendResultEvent(result, taskstream.Event{
			Label:  label,
			TaskID: snapshot.TaskID,
			CallID: snapshot.TaskID,
			Stream: "stdout",
			Chunk:  snapshot.Output.Stdout,
			State:  string(snapshot.State),
		})
	}
	if snapshot.Output.Stderr != "" {
		result = taskstream.AppendResultEvent(result, taskstream.Event{
			Label:  label,
			TaskID: snapshot.TaskID,
			CallID: snapshot.TaskID,
			Stream: "stderr",
			Chunk:  snapshot.Output.Stderr,
			State:  string(snapshot.State),
		})
	}
	if snapshot.Output.Log != "" {
		stream := "stdout"
		if snapshot.Kind == task.KindDelegate {
			stream = "assistant"
		}
		result = taskstream.AppendResultEvent(result, taskstream.Event{
			Label:  label,
			TaskID: snapshot.TaskID,
			CallID: snapshot.TaskID,
			Stream: stream,
			Chunk:  snapshot.Output.Log,
			State:  string(snapshot.State),
		})
	}
	if !running {
		result = taskstream.AppendResultEvent(result, taskstream.Event{
			Label:  label,
			TaskID: snapshot.TaskID,
			CallID: snapshot.TaskID,
			State:  string(snapshot.State),
			Final:  true,
		})
	}
	return result
}

func SnapshotResultMap(snapshot task.Snapshot) map[string]any {
	running := snapshotIsActive(snapshot)
	result := map[string]any{
		"task_id":         snapshot.TaskID,
		"kind":            string(snapshot.Kind),
		"title":           snapshot.Title,
		"state":           string(snapshot.State),
		"running":         running,
		"yielded":         snapshot.Yielded,
		"supports_input":  snapshot.SupportsInput,
		"supports_cancel": snapshot.SupportsCancel,
		"output":          snapshotOutputMap(snapshot.Output),
	}
	if strings.TrimSpace(snapshot.Output.Stdout) != "" {
		result["stdout"] = snapshot.Output.Stdout
	}
	if strings.TrimSpace(snapshot.Output.Stderr) != "" {
		result["stderr"] = snapshot.Output.Stderr
	}
	if strings.TrimSpace(snapshot.Output.Log) != "" {
		result["log"] = snapshot.Output.Log
	}
	if len(snapshot.Result) > 0 {
		result["result"] = snapshot.Result
		for key, value := range snapshot.Result {
			if _, exists := result[key]; !exists {
				result[key] = value
			}
		}
	}
	if snapshot.TaskID == "" {
		delete(result, "task_id")
	}
	return result
}

func snapshotIsActive(snapshot task.Snapshot) bool {
	if snapshot.Running {
		return true
	}
	switch snapshot.State {
	case task.StateRunning, task.StateWaitingApproval, task.StateWaitingInput:
		return true
	default:
		return false
	}
}

func snapshotOutputMap(output task.Output) map[string]any {
	result := map[string]any{}
	if strings.TrimSpace(output.Stdout) != "" {
		result["stdout"] = output.Stdout
	}
	if strings.TrimSpace(output.Stderr) != "" {
		result["stderr"] = output.Stderr
	}
	if strings.TrimSpace(output.Log) != "" {
		result["log"] = output.Log
	}
	if len(result) == 0 {
		return nil
	}
	return result
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
