package tool

import (
	"fmt"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
)

const outputMetaKey = "output_meta"

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
	if snapshotIsDelegated(snapshot) {
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
	isTTY := snapshotOutputMetaTTY(snapshot.Result)
	if !isTTY && snapshot.Output.Stdout != "" {
		result = taskstream.AppendResultEvent(result, taskstream.Event{
			Label:  label,
			TaskID: snapshot.TaskID,
			CallID: snapshot.TaskID,
			Stream: "stdout",
			Chunk:  snapshot.Output.Stdout,
			State:  string(snapshot.State),
		})
	}
	if !isTTY && snapshot.Output.Stderr != "" {
		result = taskstream.AppendResultEvent(result, taskstream.Event{
			Label:  label,
			TaskID: snapshot.TaskID,
			CallID: snapshot.TaskID,
			Stream: "stderr",
			Chunk:  snapshot.Output.Stderr,
			State:  string(snapshot.State),
		})
	}
	if !isTTY && snapshot.Output.Log != "" {
		stream := "stdout"
		if snapshot.Kind == task.KindDelegate || snapshot.Kind == task.KindSpawn {
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
	result := snapshotUIFields(snapshot)
	appendSnapshotStateFields(result, snapshot)
	appendSnapshotOutputMetadata(result, snapshot)
	if strings.TrimSpace(snapshot.TaskID) != "" && snapshotIsActive(snapshot) {
		result["task_id"] = strings.TrimSpace(snapshot.TaskID)
	}
	switch {
	case snapshotIsActive(snapshot):
		if value := snapshotResultValue(snapshot); value != "" {
			result["result"] = value
		}
	case snapshotIsDelegated(snapshot):
		if value := snapshotResultValue(snapshot); value != "" {
			result["result"] = value
		}
	default:
		if snapshotOutputMetaTTY(snapshot.Result) {
			if value := snapshotPreviewValue(snapshot); value != "" {
				result["result"] = value
			}
		} else {
			appendSnapshotTerminalFields(result, snapshot)
		}
	}
	if msg := snapshotMessage(snapshot); msg != "" {
		result["msg"] = msg
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

func snapshotIsDelegated(snapshot task.Snapshot) bool {
	switch snapshot.Kind {
	case task.KindDelegate, task.KindSpawn:
		return true
	default:
		return false
	}
}

func CompactTaskListItem(snapshot task.Snapshot) map[string]any {
	item := map[string]any{}
	if id := strings.TrimSpace(snapshot.TaskID); id != "" {
		item["task_id"] = id
	}
	if snapshotIsActive(snapshot) {
		appendSnapshotStateFields(item, snapshot)
		if summary := snapshotListSummary(snapshot); summary != "" {
			item["summary"] = summary
		}
		return item
	}
	appendSnapshotStateFields(item, snapshot)
	if summary := snapshotListSummary(snapshot); summary != "" {
		item["summary"] = summary
	}
	return item
}

func snapshotUIFields(snapshot task.Snapshot) map[string]any {
	result := map[string]any{}
	for _, key := range []string{
		"_ui_child_session_id",
		"_ui_delegation_id",
		"_ui_agent",
		"_ui_approval_pending",
	} {
		if value, ok := snapshot.Result[key]; ok && value != nil && strings.TrimSpace(fmt.Sprint(value)) != "" {
			result[key] = value
		}
	}
	return result
}

func appendSnapshotStateFields(result map[string]any, snapshot task.Snapshot) {
	if result == nil {
		return
	}
	state := strings.TrimSpace(string(snapshot.State))
	if state != "" {
		result["state"] = state
	}
}

func snapshotProgressSnippet(snapshot task.Snapshot) string {
	if snapshotIsDelegated(snapshot) {
		if approvalPending(snapshot.Result) {
			return "waiting for approval"
		}
		if state := strings.TrimSpace(fmt.Sprint(snapshot.Result["progress_state"])); state != "" && state != "running" {
			return strings.ReplaceAll(state, "_", " ")
		}
		return "subagent is running"
	}
	return snapshotPreviewValue(snapshot)
}

func snapshotTerminalOutput(snapshot task.Snapshot) string {
	if snapshotIsDelegated(snapshot) {
		return compactSnippet(snapshotFinalResult(snapshot))
	}
	return compactSnippet(firstNonEmptyText(
		snapshot.Output.Stdout,
		snapshot.Output.Stderr,
		snapshot.Output.Log,
	))
}

func snapshotYieldMessage(snapshot task.Snapshot) string {
	id := strings.TrimSpace(snapshot.TaskID)
	snippet := snapshotProgressSnippet(snapshot)
	base := "task yielded before completion"
	if id != "" {
		base += "; use TASK with task_id " + id
	}
	if snippet == "" {
		return base
	}
	return base + "\n" + snippet
}

func snapshotMessage(snapshot task.Snapshot) string {
	id := strings.TrimSpace(snapshot.TaskID)
	switch snapshot.State {
	case task.StateCompleted:
		return "task success"
	case task.StateFailed:
		return "task failed"
	case task.StateCancelled:
		return "cancelled"
	case task.StateInterrupted:
		return "interrupted"
	case task.StateTerminated:
		return "terminated"
	case task.StateWaitingInput:
		if id != "" {
			return "waiting for input; use TASK write with task_id " + id
		}
		return "waiting for input"
	case task.StateWaitingApproval:
		if id != "" {
			return "waiting for approval; use TASK with task_id " + id
		}
		return "waiting for approval"
	default:
		if id != "" {
			return "task yielded before completion; use TASK with task_id " + id
		}
		return "task yielded before completion"
	}
}

func snapshotResultValue(snapshot task.Snapshot) string {
	if snapshotIsActive(snapshot) {
		return snapshotProgressSnippet(snapshot)
	}
	if snapshotIsDelegated(snapshot) {
		return snapshotFinalResult(snapshot)
	}
	return firstNonEmptyText(
		snapshot.Output.Stdout,
		snapshot.Output.Stderr,
		snapshot.Output.Log,
	)
}

func snapshotFinalResult(snapshot task.Snapshot) string {
	return firstNonEmptyText(
		fmt.Sprint(snapshot.Result["final_result"]),
		fmt.Sprint(snapshot.Result["final_summary"]),
	)
}

func snapshotListSummary(snapshot task.Snapshot) string {
	if snapshotIsActive(snapshot) {
		return snapshotYieldMessage(snapshot)
	}
	return snapshotTerminalOutput(snapshot)
}

func appendSnapshotTerminalFields(result map[string]any, snapshot task.Snapshot) {
	if result == nil {
		return
	}
	if value, ok := snapshot.Result["exit_code"]; ok && value != nil {
		result["exit_code"] = value
	}
	if stdout := snapshot.Output.Stdout; strings.TrimSpace(stdout) != "" {
		result["stdout"] = stdout
	}
	if stderr := snapshot.Output.Stderr; strings.TrimSpace(stderr) != "" {
		result["stderr"] = stderr
	}
	if output := snapshot.Output.Log; strings.TrimSpace(output) != "" {
		result["output"] = output
	}
}

func appendSnapshotOutputMetadata(result map[string]any, snapshot task.Snapshot) {
	if result == nil || len(snapshot.Result) == 0 {
		return
	}
	if value, ok := snapshot.Result["exit_code"]; ok && value != nil {
		result["exit_code"] = value
	}
	if raw, ok := snapshot.Result[outputMetaKey]; ok {
		if meta := cloneAnyMap(raw); len(meta) > 0 {
			result[outputMetaKey] = meta
		}
	}
}

func snapshotPreviewValue(snapshot task.Snapshot) string {
	if preview := strings.TrimSpace(fmt.Sprint(snapshot.Result["latest_output"])); preview != "" && preview != "<nil>" {
		return preview
	}
	return compactSnippet(firstNonEmptyText(
		snapshot.Output.Log,
		snapshot.Output.Stdout,
		snapshot.Output.Stderr,
	))
}

func snapshotOutputMetaTTY(values map[string]any) bool {
	if len(values) == 0 {
		return false
	}
	raw, ok := values[outputMetaKey]
	if !ok {
		return false
	}
	meta, ok := raw.(map[string]any)
	if !ok || len(meta) == 0 {
		return false
	}
	switch typed := meta["tty"].(type) {
	case bool:
		return typed
	case string:
		value := strings.TrimSpace(strings.ToLower(typed))
		return value == "1" || value == "true" || value == "yes" || value == "on"
	default:
		return false
	}
}

func cloneAnyMap(raw any) map[string]any {
	in, ok := raw.(map[string]any)
	if !ok || len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func compactSnippet(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	text = strings.Join(lines, "\n")
	rs := []rune(text)
	if len(rs) > 240 {
		return string(rs[:237]) + "..."
	}
	return text
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func approvalPending(values map[string]any) bool {
	if len(values) == 0 {
		return false
	}
	raw, ok := values["approval_pending"].(bool)
	return ok && raw
}
