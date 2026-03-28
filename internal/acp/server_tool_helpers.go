package acp

import (
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/idutil"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
)

type toolCallSnapshot struct {
	name string
	args map[string]any
}

func toolKindForName(name string) string {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "READ":
		return ToolKindRead
	case "WRITE", "PATCH":
		return ToolKindEdit
	case "SEARCH", "GLOB", "LIST":
		return ToolKindSearch
	case "PLAN":
		return ToolKindOther
	case "BASH", "TASK":
		return ToolKindExecute
	default:
		return ToolKindOther
	}
}

func summarizeToolCallTitle(name string, args map[string]any) string {
	name = strings.TrimSpace(name)
	switch strings.ToUpper(name) {
	case "READ", "WRITE", "PATCH", "SEARCH", "LIST", "GLOB":
		if path, _ := args["path"].(string); strings.TrimSpace(path) != "" {
			return fmt.Sprintf("%s %s", name, strings.TrimSpace(path))
		}
	case "BASH":
		if command, _ := args["command"].(string); strings.TrimSpace(command) != "" {
			return fmt.Sprintf("BASH %s", strings.TrimSpace(command))
		}
	case "TASK":
		action := strings.TrimSpace(stringValue(args["action"]))
		display := taskActionCallDisplayName(action)
		switch strings.ToLower(action) {
		case "wait":
			if waited := friendlyWaitLabelForACP(effectiveTaskWaitMSForACP(action, args)); waited != "" {
				return fmt.Sprintf("%s %s", display, waited)
			}
			return display
		case "status", "cancel":
			if taskID := strings.TrimSpace(stringValue(args["task_id"])); taskID != "" {
				return fmt.Sprintf("%s %s", display, idutil.ShortDisplay(taskID))
			}
			return display
		case "write":
			if preview := summarizeTaskWriteInputForACP(args); preview != "" {
				return fmt.Sprintf("%s %s", display, preview)
			}
			return display
		default:
			taskID := strings.TrimSpace(stringValue(args["task_id"]))
			if action != "" && taskID != "" {
				return fmt.Sprintf("%s {task=%s}", display, idutil.ShortDisplay(taskID))
			}
			if action != "" {
				return display
			}
		}
	}
	return name
}

func toolCallContentForResult(toolName string, result map[string]any) []ToolCallContent {
	if !strings.EqualFold(strings.TrimSpace(toolName), "BASH") {
		return nil
	}
	terminalID := strings.TrimSpace(stringValue(result["session_id"]))
	if terminalID == "" {
		terminalID = strings.TrimSpace(stringValue(result["_ui_exec_session_id"]))
	}
	if terminalID == "" {
		return nil
	}
	return []ToolCallContent{{
		Type:       "terminal",
		TerminalID: terminalID,
	}}
}

func toolStatusForResult(toolName string, result map[string]any) string {
	if hasToolError(result) {
		return ToolStatusFailed
	}
	state := strings.ToLower(strings.TrimSpace(stringValue(result["state"])))
	switch state {
	case "running", "waiting_input", "waiting_approval":
		return ToolStatusInProgress
	}
	if strings.EqualFold(strings.TrimSpace(toolName), "BASH") {
		switch state {
		case "failed", "cancelled", "interrupted", "terminated":
			return ToolStatusFailed
		}
		if exitCode, ok := intValue(result["exit_code"]); ok && exitCode != 0 {
			return ToolStatusFailed
		}
	}
	return ToolStatusCompleted
}

func toolLocations(args map[string]any, result map[string]any) []ToolCallLocation {
	path := ""
	if result != nil {
		path, _ = result["path"].(string)
	}
	if path == "" && args != nil {
		path, _ = args["path"].(string)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return []ToolCallLocation{{Path: path}}
}

func hasToolError(result map[string]any) bool {
	if result == nil {
		return false
	}
	text := strings.TrimSpace(fmt.Sprint(result["error"]))
	return text != "" && text != "<nil>"
}

func sanitizeToolResultForACP(result map[string]any) map[string]any {
	return sanitizeToolResultMapForACP(result, true)
}

func sanitizeToolResultMapForACP(result map[string]any, topLevel bool) map[string]any {
	if len(result) == 0 {
		return result
	}
	out := make(map[string]any, len(result))
	for key, value := range result {
		trimmed := strings.TrimSpace(key)
		if strings.HasPrefix(trimmed, "_ui_") {
			continue
		}
		if topLevel && strings.EqualFold(trimmed, "metadata") {
			continue
		}
		if strings.EqualFold(trimmed, "output_meta") {
			meta, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if sanitized := sanitizeOutputMetaForACP(meta); len(sanitized) > 0 {
				out[key] = sanitized
			}
			continue
		}
		out[key] = sanitizeToolResultValueForACP(value)
	}
	return out
}

func sanitizeToolResultValueForACP(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return sanitizeToolResultMapForACP(typed, false)
	case []any:
		out := make([]any, 0, len(typed))
		for _, one := range typed {
			out = append(out, sanitizeToolResultValueForACP(one))
		}
		return out
	default:
		return value
	}
}

func sanitizeOutputMetaForACP(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := map[string]any{}
	copyIfTrue := func(key string) {
		if typed, ok := meta[key].(bool); ok && typed {
			out[key] = true
		}
	}
	copyIfPositive := func(key string) {
		if value, ok := intValue(meta[key]); ok && value > 0 {
			out[key] = value
		}
	}

	copyIfTrue("capture_truncated")
	copyIfTrue("model_truncated")
	copyIfTrue("stdout_cap_reached")
	copyIfTrue("stderr_cap_reached")
	copyIfPositive("capture_cap_bytes")
	copyIfPositive("stdout_dropped_bytes")
	copyIfPositive("stderr_dropped_bytes")
	copyIfPositive("stdout_earliest_marker")
	copyIfPositive("stderr_earliest_marker")

	if !boolValue(meta["capture_truncated"]) && !boolValue(meta["stdout_cap_reached"]) && !boolValue(meta["stderr_cap_reached"]) {
		delete(out, "capture_cap_bytes")
		delete(out, "stdout_earliest_marker")
		delete(out, "stderr_earliest_marker")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func boolValue(value any) bool {
	typed, ok := value.(bool)
	return ok && typed
}

func (s *serverSession) rememberToolCall(callID string, name string, args map[string]any) {
	if s == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.toolCalls == nil {
		s.toolCalls = map[string]toolCallSnapshot{}
	}
	cp := make(map[string]any, len(args))
	maps.Copy(cp, args)
	s.toolCalls[callID] = toolCallSnapshot{name: strings.TrimSpace(name), args: cp}
}

func normalizeToolArgsForACP(toolName string, args map[string]any, fsys toolexec.FileSystem) map[string]any {
	if len(args) == 0 {
		return args
	}
	keys := toolPathKeysForACP(toolName)
	if len(keys) == 0 {
		return args
	}
	out := make(map[string]any, len(args))
	maps.Copy(out, args)
	for _, key := range keys {
		value := strings.TrimSpace(stringValue(out[key]))
		if value == "" {
			continue
		}
		if normalized, ok := normalizeACPPathValue(fsys, value); ok {
			out[key] = normalized
		}
	}
	return out
}

func toolPathKeysForACP(toolName string) []string {
	switch strings.ToUpper(strings.TrimSpace(toolName)) {
	case "READ", "WRITE", "PATCH", "SEARCH", "LIST", "GLOB":
		return []string{"path"}
	case "BASH":
		return []string{"workdir", "dir"}
	default:
		return nil
	}
}

func normalizeACPPathValue(fsys toolexec.FileSystem, value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if strings.HasPrefix(value, "~/") {
		if fsys != nil {
			if home, err := fsys.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
				value = filepath.Join(home, value[2:])
			}
		}
	}
	if !filepath.IsAbs(value) {
		if fsys == nil {
			return "", false
		}
		wd, err := fsys.Getwd()
		if err != nil || strings.TrimSpace(wd) == "" {
			return "", false
		}
		value = filepath.Join(wd, value)
	}
	return filepath.Clean(value), true
}

func (s *serverSession) rememberAsyncToolResult(toolName string, callID string, result map[string]any) {
	if s == nil || len(result) == 0 {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(toolName), "BASH") {
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	taskID := strings.TrimSpace(stringValue(result["task_id"]))
	sessionID := strings.TrimSpace(stringValue(result["session_id"]))
	if sessionID == "" {
		sessionID = strings.TrimSpace(stringValue(result["_ui_exec_session_id"]))
	}
	if taskID == "" && sessionID == "" {
		return
	}
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if taskID != "" {
		if s.asyncTasks == nil {
			s.asyncTasks = map[string]string{}
		}
		s.asyncTasks[taskID] = callID
	}
	if sessionID != "" {
		if s.asyncSessions == nil {
			s.asyncSessions = map[string]string{}
		}
		s.asyncSessions[sessionID] = callID
	}
}

func (s *serverSession) toolCall(callID string) (toolCallSnapshot, bool) {
	if s == nil {
		return toolCallSnapshot{}, false
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return toolCallSnapshot{}, false
	}
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	snap, ok := s.toolCalls[callID]
	return snap, ok
}

func (s *serverSession) asyncOriginCallID(result map[string]any) string {
	if s == nil || len(result) == 0 {
		return ""
	}
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if taskID := strings.TrimSpace(stringValue(result["task_id"])); taskID != "" && s.asyncTasks != nil {
		if callID := strings.TrimSpace(s.asyncTasks[taskID]); callID != "" {
			return callID
		}
	}
	if sessionID := strings.TrimSpace(stringValue(result["session_id"])); sessionID != "" && s.asyncSessions != nil {
		if callID := strings.TrimSpace(s.asyncSessions[sessionID]); callID != "" {
			return callID
		}
	}
	if sessionID := strings.TrimSpace(stringValue(result["_ui_exec_session_id"])); sessionID != "" && s.asyncSessions != nil {
		if callID := strings.TrimSpace(s.asyncSessions[sessionID]); callID != "" {
			return callID
		}
	}
	return ""
}

func taskActionCallDisplayName(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "wait":
		return "WAIT"
	case "status":
		return "CHECK"
	case "write":
		return "WRITE"
	case "cancel":
		return "CANCEL"
	case "list":
		return "LIST"
	default:
		return "TASK"
	}
}

func friendlyWaitLabelForACP(waitMS int) string {
	switch {
	case waitMS < 0:
		return ""
	case waitMS == 0:
		return "0s"
	case waitMS%1000 == 0:
		return fmt.Sprintf("%d s", waitMS/1000)
	case waitMS < 1000:
		return fmt.Sprintf("%dms", waitMS)
	default:
		return fmt.Sprintf("%.1f s", float64(waitMS)/1000.0)
	}
}

func effectiveTaskWaitMSForACP(action string, args map[string]any) int {
	if !strings.EqualFold(strings.TrimSpace(action), "wait") {
		return -1
	}
	if len(args) == 0 {
		return 5000
	}
	rawWaitMS, ok := args["yield_time_ms"]
	if !ok || rawWaitMS == nil {
		return 5000
	}
	waitMS, ok := intValue(rawWaitMS)
	if !ok {
		return 5000
	}
	if waitMS <= 0 {
		return 5000
	}
	return waitMS
}

func summarizeTaskWriteInputForACP(args map[string]any) string {
	input := stringValue(args["input"])
	if input == "" {
		return ""
	}
	return truncateTaskWriteInputForACP(input, 120)
}

func truncateTaskWriteInputForACP(input string, limit int) string {
	text := strings.NewReplacer(
		"\r\n", "\\n",
		"\n", "\\n",
		"\r", "\\r",
		"\t", "\\t",
	).Replace(input)
	rs := []rune(text)
	if limit <= 0 || len(rs) <= limit {
		return text
	}
	if limit <= 3 {
		return string(rs[:limit])
	}
	return string(rs[:limit-3]) + "..."
}

func supplementalToolCallUpdates(sess *serverSession, resp *model.ToolResponse) []ToolCallUpdate {
	if sess == nil || resp == nil || len(resp.Result) == 0 {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(resp.Name), "TASK") || hasToolError(resp.Result) {
		return nil
	}
	call, ok := sess.toolCall(resp.ID)
	if !ok || !strings.EqualFold(strings.TrimSpace(call.name), "TASK") {
		return nil
	}
	action := strings.TrimSpace(stringValue(call.args["action"]))
	if !strings.EqualFold(action, "cancel") {
		return nil
	}
	state := strings.TrimSpace(stringValue(resp.Result["state"]))
	if !strings.EqualFold(state, "cancelled") {
		return nil
	}
	originCallID := strings.TrimSpace(sess.asyncOriginCallID(resp.Result))
	if originCallID == "" || originCallID == strings.TrimSpace(resp.ID) {
		return nil
	}
	status := ToolStatusCompleted
	return []ToolCallUpdate{{
		SessionUpdate: UpdateToolCallState,
		ToolCallID:    originCallID,
		Status:        ptr(status),
		RawOutput:     sanitizeToolResultForACP(cancelledOriginResult(resp.Result)),
	}}
}

func cancelledOriginResult(result map[string]any) map[string]any {
	if len(result) == 0 {
		return map[string]any{"state": "cancelled", "msg": "cancelled"}
	}
	out := map[string]any{
		"state": "cancelled",
		"msg":   "cancelled",
	}
	for _, key := range []string{"task_id", "session_id"} {
		if value, ok := result[key]; ok && value != nil && strings.TrimSpace(fmt.Sprint(value)) != "" {
			out[key] = value
		}
	}
	for _, key := range []string{"result", "output", "latest_output"} {
		if output, ok := result[key]; ok && output != nil {
			out["result"] = sanitizeToolResultValueForACP(output)
			break
		}
	}
	return out
}
