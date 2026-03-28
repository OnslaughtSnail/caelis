package tool

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
	visiblemeta "github.com/OnslaughtSnail/caelis/kernel/tool/internal/outputmeta"
)

const outputMetaKey = "output_meta"

const (
	maxPublicTaskEvents     = 4
	maxPublicTaskEventBytes = 4096
	maxActionTaskEvents     = 1
	maxActionTaskEventBytes = 512
)

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
	if snapshotIsSubagent(snapshot) {
		if snapshot.Output.Log != "" {
			result = taskstream.AppendResultEvent(result, taskstream.Event{
				Label:  label,
				TaskID: snapshot.TaskID,
				CallID: snapshot.TaskID,
				Stream: "assistant",
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
		if snapshot.Kind == task.KindSpawn {
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
	result := map[string]any{}
	appendHiddenSnapshotFields(result, snapshot)
	appendSnapshotStateFields(result, snapshot)
	if taskID := strings.TrimSpace(snapshot.TaskID); taskID != "" && snapshotIsActive(snapshot) {
		result["task_id"] = taskID
	}
	switch {
	case snapshotIsActive(snapshot):
		if msg := snapshotActiveMessage(snapshot); msg != "" {
			result["msg"] = msg
		}
	case snapshotIsSubagent(snapshot):
		if snapshot.State == task.StateCompleted {
			if value := snapshotFinalResult(snapshot); value != "" {
				result["output"] = value
			}
		}
		if text := firstNonEmptyText(fmt.Sprint(snapshot.Result["error"])); text != "" {
			result["error"] = text
		}
	default:
		appendSnapshotTerminalFields(result, snapshot)
		if value, ok := snapshot.Result["exit_code"]; ok && value != nil {
			result["exit_code"] = value
		}
		appendSnapshotTruncationMessage(result, snapshot)
		if text := firstNonEmptyText(fmt.Sprint(snapshot.Result["error"])); text != "" {
			result["error"] = text
		}
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

func snapshotIsSubagent(snapshot task.Snapshot) bool {
	return snapshot.Kind == task.KindSpawn
}

func CompactTaskListItem(snapshot task.Snapshot) map[string]any {
	item := map[string]any{
		"state": string(snapshot.State),
		"kind":  string(snapshot.Kind),
	}
	if id := strings.TrimSpace(snapshot.TaskID); id != "" {
		item["task_id"] = id
	}
	if summary := snapshotListSummary(snapshot); summary != "" {
		item["summary"] = summary
	}
	return item
}

func snapshotListSummary(snapshot task.Snapshot) string {
	if !snapshotIsActive(snapshot) {
		return ""
	}
	if preview := snapshotProgressPreview(snapshot); preview != "" {
		return preview
	}
	return snapshotActiveMessage(snapshot)
}

func snapshotProgressPreview(snapshot task.Snapshot) string {
	if preview := strings.TrimSpace(fmt.Sprint(snapshot.Result["latest_output"])); preview != "" && preview != "<nil>" {
		return compactSnippet(preview)
	}
	return compactSnippet(firstNonEmptyText(
		snapshot.Output.Log,
		snapshot.Output.Stdout,
		snapshot.Output.Stderr,
	))
}

func snapshotActiveMessage(snapshot task.Snapshot) string {
	switch snapshot.State {
	case task.StateWaitingInput:
		if snapshot.Kind == task.KindSpawn {
			return "subagent is waiting for input"
		}
		return "bash is waiting for input; use TASK write"
	case task.StateWaitingApproval:
		if snapshot.Kind == task.KindSpawn {
			return "subagent is waiting for user approval"
		}
		return "bash is waiting for user approval"
	case task.StateRunning:
		if snapshot.Kind == task.KindSpawn {
			return "subagent is still running"
		}
		return "bash is still running"
	default:
		return ""
	}
}

func PublicTaskEvents(snapshot task.Snapshot) []map[string]any {
	events := collectPublicTaskEvents(snapshot)
	if len(events) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		item := map[string]any{
			"seq":    ev.Seq,
			"stream": ev.Stream,
			"text":   ev.Text,
		}
		out = append(out, item)
	}
	return out
}

func PublicTaskActionEvents(snapshot task.Snapshot) []map[string]any {
	events := collectPublicTaskEvents(snapshot)
	if len(events) == 0 {
		return nil
	}
	events = trimPublicTaskEvents(events, maxActionTaskEvents, maxActionTaskEventBytes)
	if len(events) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		item := map[string]any{
			"seq":    ev.Seq,
			"stream": ev.Stream,
			"text":   ev.Text,
		}
		out = append(out, item)
	}
	return out
}

type publicTaskEvent struct {
	Seq    int
	Stream string
	Text   string
}

func collectPublicTaskEvents(snapshot task.Snapshot) []publicTaskEvent {
	events := make([]publicTaskEvent, 0, 2)
	switch snapshot.Kind {
	case task.KindSpawn:
		text := strings.TrimSpace(snapshot.Output.Log)
		if text != "" {
			seq := intValue(snapshot.Result["progress_seq"])
			if seq <= 0 {
				seq = utf8.RuneCountInString(text)
			}
			events = append(events, publicTaskEvent{Seq: seq, Stream: "assistant", Text: text})
		}
	default:
		if text := strings.TrimSpace(snapshot.Output.Stdout); text != "" {
			seq := intValue(snapshot.Result["stdout_bytes"])
			if seq <= 0 {
				seq = utf8.RuneCountInString(text)
			}
			events = append(events, publicTaskEvent{Seq: seq, Stream: "stdout", Text: text})
		}
		if text := strings.TrimSpace(snapshot.Output.Stderr); text != "" {
			seq := intValue(snapshot.Result["stdout_bytes"]) + intValue(snapshot.Result["stderr_bytes"])
			if seq <= 0 {
				seq = utf8.RuneCountInString(text)
			}
			events = append(events, publicTaskEvent{Seq: seq, Stream: "stderr", Text: text})
		}
	}
	if len(events) == 0 {
		return nil
	}
	return trimPublicTaskEvents(events, maxPublicTaskEvents, maxPublicTaskEventBytes)
}

func trimPublicTaskEvents(events []publicTaskEvent, maxEvents int, maxBytes int) []publicTaskEvent {
	if len(events) == 0 {
		return nil
	}
	if maxEvents > 0 && len(events) > maxEvents {
		events = append([]publicTaskEvent(nil), events[len(events)-maxEvents:]...)
	} else {
		events = append([]publicTaskEvent(nil), events...)
	}
	if maxBytes <= 0 {
		return events
	}
	total := 0
	start := len(events)
	for i := len(events) - 1; i >= 0; i-- {
		size := len(events[i].Text)
		if total+size <= maxBytes {
			total += size
			start = i
			continue
		}
		remaining := maxBytes - total
		if remaining > 0 {
			events[i].Text = tailBytes(events[i].Text, remaining)
			start = i
		}
		break
	}
	if start > 0 {
		events = events[start:]
	}
	filtered := events[:0]
	for _, ev := range events {
		ev.Text = strings.TrimSpace(ev.Text)
		if ev.Text == "" {
			continue
		}
		filtered = append(filtered, ev)
	}
	return filtered
}

func tailBytes(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	text = text[len(text)-limit:]
	for !utf8.ValidString(text) && len(text) > 0 {
		text = text[1:]
	}
	return text
}

func appendHiddenSnapshotFields(result map[string]any, snapshot task.Snapshot) {
	if result == nil || len(snapshot.Result) == 0 {
		return
	}
	for key, value := range snapshot.Result {
		if !strings.HasPrefix(strings.TrimSpace(key), "_ui_") {
			continue
		}
		if value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		result[key] = value
	}
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

func snapshotFinalResult(snapshot task.Snapshot) string {
	return firstNonEmptyText(
		fmt.Sprint(snapshot.Result["final_result"]),
		fmt.Sprint(snapshot.Result["final_summary"]),
	)
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

func appendSnapshotTruncationMessage(result map[string]any, snapshot task.Snapshot) {
	if result == nil || len(snapshot.Result) == 0 {
		return
	}
	raw, ok := snapshot.Result[outputMetaKey]
	if !ok {
		return
	}
	meta := cloneAnyMap(raw)
	if len(meta) == 0 {
		return
	}
	if compacted := visiblemeta.CompactVisible(meta); len(compacted) > 0 {
		result["msg"] = "output truncated"
	}
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
	return task.FormatLatestOutput(text)
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

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}
