package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/idutil"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type runRenderConfig struct {
	ShowReasoning bool
	Verbose       bool
	Writer        io.Writer
	UI            *ui
	OnUsage       func(int) // called with conservative usage floor after run completes
	OnEvent       func(*session.Event) bool
}

func runOnce(ctx context.Context, rt *runtime.Runtime, req runtime.RunRequest, renderCfg runRenderConfig) error {
	invokeCtx := ctx
	out := renderCfg.Writer
	if out == nil {
		out = os.Stdout
	}
	render := &renderState{
		showReasoning:    renderCfg.ShowReasoning,
		verbose:          renderCfg.Verbose,
		out:              out,
		ui:               renderCfg.UI,
		pendingToolCalls: map[string]toolCallSnapshot{},
	}
	for ev, err := range rt.Run(invokeCtx, req) {
		if err != nil {
			return err
		}
		if ev == nil {
			continue
		}
		if ev.Meta != nil {
			if usageFloor := usageFloorFromMeta(ev.Meta); usageFloor > 0 {
				render.lastPromptTokens = usageFloor
			}
		}
		if renderCfg.OnEvent != nil && renderCfg.OnEvent(ev) {
			continue
		}
		printEvent(ev, render)
	}
	if render.partialOpen {
		fmt.Fprintln(render.out)
	}
	if renderCfg.OnUsage != nil && render.lastPromptTokens > 0 {
		renderCfg.OnUsage(render.lastPromptTokens)
	}
	return nil
}

type renderState struct {
	partialOpen          bool
	partialChannel       string
	seenAnswerPartial    bool
	seenReasoningPartial bool
	answerPartialBuffer  strings.Builder
	showReasoning        bool
	verbose              bool
	replayUserMessages   bool
	out                  io.Writer
	ui                   *ui
	pendingToolCalls     map[string]toolCallSnapshot
	lastPromptTokens     int // most recent conservative usage floor from event metadata
}

type toolCallSnapshot struct {
	Args          map[string]any
	RichDiffShown bool
}

func printEvent(ev *session.Event, state *renderState) {
	if ev == nil {
		return
	}
	// Track usage metadata from events.
	if state != nil && ev.Meta != nil {
		if usageFloor := usageFloorFromMeta(ev.Meta); usageFloor > 0 {
			state.lastPromptTokens = usageFloor
		}
	}
	if state != nil && eventIsPartial(ev) {
		channel := eventChannel(ev)
		chunk := ev.Message.Text
		if channel == "reasoning" {
			chunk = ev.Message.Reasoning
			if !state.showReasoning {
				return
			}
		}
		if chunk == "" {
			return
		}
		if state.partialOpen && state.partialChannel != channel {
			fmt.Fprintln(state.out)
			state.partialOpen = false
		}
		if channel != "reasoning" {
			// Buffer answer partial chunks and render the final full assistant
			// message once, so Markdown structure doesn't break on chunk edges.
			state.answerPartialBuffer.WriteString(chunk)
			state.seenAnswerPartial = true
			return
		}
		if !state.partialOpen {
			if channel == "reasoning" {
				if state.ui != nil {
					fmt.Fprint(state.out, state.ui.ReasoningPrefix())
				} else {
					fmt.Fprint(state.out, "│ ")
				}
			} else {
				if state.ui != nil {
					fmt.Fprint(state.out, state.ui.AssistantPrefix())
				} else {
					fmt.Fprint(state.out, "* ")
				}
			}
		}
		fmt.Fprint(state.out, chunk)
		state.partialOpen = true
		state.partialChannel = channel
		if channel == "reasoning" {
			state.seenReasoningPartial = true
		} else {
			state.seenAnswerPartial = true
		}
		return
	}
	if state != nil && state.partialOpen {
		fmt.Fprintln(state.out)
		state.partialOpen = false
	}

	msg := ev.Message
	if msg.Role == model.RoleSystem {
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			return
		}
		out := io.Writer(nil)
		if state != nil {
			out = state.out
		}
		if out == nil {
			out = os.Stdout
		}
		if state != nil && state.ui != nil {
			switch {
			case strings.HasPrefix(text, "warn:"):
				state.ui.Warn("%s\n", strings.TrimSpace(strings.TrimPrefix(text, "warn:")))
			case strings.HasPrefix(text, "note:"):
				state.ui.Note("%s\n", strings.TrimSpace(strings.TrimPrefix(text, "note:")))
			default:
				fmt.Fprintf(out, "%s%s\n", state.ui.SystemPrefix(), text)
			}
		} else {
			fmt.Fprintln(out, text)
		}
		return
	}
	if msg.Role == model.RoleUser {
		if state != nil && state.replayUserMessages {
			userText := strings.TrimSpace(msg.Text)
			if userText == "" {
				userText = userTextFromContentParts(msg.ContentParts)
			}
			if userText != "" {
				fmt.Fprintf(state.out, "> %s\n", userText)
			}
		}
		// Show image attachment indicators for user messages.
		if msg.HasImages() {
			for _, part := range msg.ContentParts {
				if part.Type == model.ContentPartImage {
					name := strings.TrimSpace(part.FileName)
					if name == "" {
						name = "image"
					}
					if state.ui != nil {
						fmt.Fprintf(state.out, "%s[image: %s]\n", state.ui.SystemPrefix(), name)
					} else {
						fmt.Fprintf(state.out, "📎 [image: %s]\n", name)
					}
				}
			}
		}
		return
	}
	if msg.Role == model.RoleAssistant {
		if state != nil && state.showReasoning && msg.Reasoning != "" && !state.seenReasoningPartial {
			if state.ui != nil {
				fmt.Fprintf(state.out, "%s%s\n", state.ui.ReasoningPrefix(), strings.TrimSpace(msg.Reasoning))
			} else {
				fmt.Fprintf(state.out, "│ %s\n", strings.TrimSpace(msg.Reasoning))
			}
		}
		text := strings.TrimSpace(msg.Text)
		if state != nil && state.seenAnswerPartial {
			if text == "" {
				text = strings.TrimSpace(state.answerPartialBuffer.String())
			}
		}
		if text != "" {
			formatted := renderAssistantMarkdown(text)
			if state.ui != nil {
				fmt.Fprintf(state.out, "%s%s\n", state.ui.AssistantPrefix(), formatted)
			} else {
				fmt.Fprintf(state.out, "* %s\n", formatted)
			}
		}
		if state != nil {
			state.seenAnswerPartial = false
			state.seenReasoningPartial = false
			state.answerPartialBuffer.Reset()
		}
	}
	if len(msg.ToolCalls) > 0 {
		for i, call := range msg.ToolCalls {
			parsedArgs := parseToolArgsForDisplay(call.Args)
			if state != nil && call.ID != "" {
				if state.pendingToolCalls == nil {
					state.pendingToolCalls = map[string]toolCallSnapshot{}
				}
				state.pendingToolCalls[call.ID] = toolCallSnapshot{
					Args: cloneAnyMap(parsedArgs),
				}
			}
			if strings.EqualFold(strings.TrimSpace(call.Name), "TASK") {
				continue
			}
			if state.ui != nil {
				fmt.Fprintf(state.out, "%s%s %s\n", state.ui.ToolCallPrefix(i+1), call.Name, summarizeToolArgs(call.Name, parsedArgs))
			} else {
				fmt.Fprintf(state.out, "▸ %s %s\n", call.Name, summarizeToolArgs(call.Name, parsedArgs))
			}
		}
	}
	if msg.ToolResponse != nil {
		var callArgs map[string]any
		if state != nil && msg.ToolResponse.ID != "" && state.pendingToolCalls != nil {
			if snapshot, ok := state.pendingToolCalls[msg.ToolResponse.ID]; ok {
				callArgs = snapshot.Args
				delete(state.pendingToolCalls, msg.ToolResponse.ID)
			}
		}
		if strings.EqualFold(strings.TrimSpace(msg.ToolResponse.Name), "TASK") && !hasToolError(msg.ToolResponse.Result) {
			return
		}
		// Suppress result line for read-only FS tools (the call line is sufficient).
		if isReadOnlyFSTool(msg.ToolResponse.Name) && !hasToolError(msg.ToolResponse.Result) {
			return
		}
		summary := summarizeToolResponseWithCall(msg.ToolResponse.Name, msg.ToolResponse.Result, callArgs)
		if strings.TrimSpace(summary) == "" {
			return
		}
		if state.ui != nil {
			fmt.Fprint(state.out, formatToolResultLine(state.ui.ToolResultPrefix(), msg.ToolResponse.Name, summary))
		} else {
			fmt.Fprint(state.out, formatToolResultLine("✓ ", msg.ToolResponse.Name, summary))
		}
		return
	}
	if msg.Role == model.RoleAssistant {
		return
	}
	text := strings.TrimSpace(msg.Text)
	if text != "" {
		switch msg.Role {
		case model.RoleSystem:
			if state.ui != nil {
				fmt.Fprintf(state.out, "%s%s\n", state.ui.SystemPrefix(), text)
			} else {
				fmt.Fprintf(state.out, "! %s\n", text)
			}
		default:
			fmt.Fprintf(state.out, "- %s\n", text)
		}
	}
}

func userTextFromContentParts(parts []model.ContentPart) string {
	if len(parts) == 0 {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type != model.ContentPartText {
			continue
		}
		text := strings.TrimSpace(part.Text)
		if text == "" {
			continue
		}
		texts = append(texts, text)
	}
	return strings.TrimSpace(strings.Join(texts, "\n"))
}

func summarizeToolArgs(toolName string, args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	switch strings.ToUpper(strings.TrimSpace(toolName)) {
	case "BASH":
		command := strings.TrimSpace(asString(args["command"]))
		if command != "" {
			return fmt.Sprintf("{command=%s}", truncateInline(command, 120))
		}
	case "TASK":
		action := strings.TrimSpace(asString(args["action"]))
		taskID := strings.TrimSpace(asString(args["task_id"]))
		if action != "" && taskID != "" {
			return fmt.Sprintf("{action=%s task=%s}", action, idutil.ShortDisplay(taskID))
		}
		if action != "" {
			return fmt.Sprintf("{action=%s}", action)
		}
	case "READ":
		path := strings.TrimSpace(asString(args["path"]))
		if path != "" {
			return displayFileName(path)
		}
	case "PATCH":
		path := strings.TrimSpace(asString(args["path"]))
		return displayFileName(path)
	case "WRITE":
		path := strings.TrimSpace(asString(args["path"]))
		return displayFileName(path)
	case "SEARCH":
		path := strings.TrimSpace(asString(args["path"]))
		query := strings.TrimSpace(asString(args["query"]))
		return fmt.Sprintf("%s {query=%s}", displayFileName(path), truncateInline(query, 60))
	case "GLOB":
		pattern := strings.TrimSpace(asString(args["pattern"]))
		if pattern != "" {
			return fmt.Sprintf("{pattern=%s}", pattern)
		}
	case "LIST", "STAT":
		path := strings.TrimSpace(asString(args["path"]))
		if path != "" {
			return displayFileName(path)
		}
	case "DELEGATE":
		task := strings.TrimSpace(asString(args["task"]))
		if task != "" {
			return fmt.Sprintf("{task=%s}", truncateInline(task, 120))
		}
	}
	if isMCPToolName(toolName) {
		if target := summarizeWebLikeTarget(args); target != "" {
			return target
		}
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := fmt.Sprint(args[key])
		parts = append(parts, fmt.Sprintf("%s=%s", key, truncateInline(value, 72)))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func parseToolArgsForDisplay(raw string) map[string]any {
	parsed, err := model.ParseToolCallArgs(raw)
	if err != nil {
		return map[string]any{}
	}
	return parsed
}

func summarizeToolResponse(toolName string, result map[string]any) string {
	return summarizeToolResponseWithCall(toolName, result, nil)
}

func summarizeToolResponseWithCall(toolName string, result map[string]any, callArgs map[string]any) string {
	if len(result) == 0 {
		return "{}"
	}
	switch strings.ToUpper(strings.TrimSpace(toolName)) {
	case "BASH":
		if taskID := strings.TrimSpace(asString(result["task_id"])); taskID != "" && fmt.Sprint(result["running"]) == "true" {
			if preview := compactTaskPreview(firstNonEmpty(result, "latest_output")); preview != "" {
				return fmt.Sprintf("task=%s running\n%s", idutil.ShortDisplay(taskID), preview)
			}
			return fmt.Sprintf("task=%s running", idutil.ShortDisplay(taskID))
		}
		exitCode, _ := asInt(result["exit_code"])
		stdout := strings.TrimRight(asString(result["stdout"]), "\n")
		stderr := strings.TrimRight(asString(result["stderr"]), "\n")
		if exitCode != 0 {
			output := strings.TrimSpace(stderr)
			if output == "" {
				output = strings.TrimSpace(stdout)
			}
			if output == "" {
				return fmt.Sprintf("exit_code=%d", exitCode)
			}
			return fmt.Sprintf("exit_code=%d\n%s", exitCode, tailLines(output, 6))
		}
		output := strings.TrimSpace(stdout)
		if output == "" {
			if strings.TrimSpace(stderr) != "" {
				return tailLines(strings.TrimSpace(stderr), 5)
			}
			return "exit_code=0"
		}
		return tailLines(output, 5)
	case "READ":
		// Suppressed in printEvent; return empty for read-only tools.
		return ""
	case "PATCH":
		path := strings.TrimSpace(asString(result["path"]))
		created := fmt.Sprint(result["created"]) == "true"
		display := displayFileName(path)
		if path != "" {
			action := "edited"
			if created {
				action = "created"
			}
			return fmt.Sprintf("%s %s", action, display)
		}
	case "WRITE":
		path := strings.TrimSpace(asString(result["path"]))
		created := fmt.Sprint(result["created"]) == "true"
		lineCount, _ := asInt(result["line_count"])
		display := displayFileName(path)
		if path != "" {
			if created {
				return fmt.Sprintf("created %s (%d lines)", display, lineCount)
			}
			return fmt.Sprintf("wrote %s (%d lines)", display, lineCount)
		}
	case "SEARCH":
		count, _ := asInt(result["count"])
		fileCount, _ := asInt(result["file_count"])
		truncated := fmt.Sprint(result["truncated"]) == "true"
		if truncated {
			return fmt.Sprintf("found %d matches in %d files (truncated)", count, fileCount)
		}
		return fmt.Sprintf("found %d matches in %d files", count, fileCount)
	case "GLOB":
		count, _ := asInt(result["count"])
		return fmt.Sprintf("matched %d paths", count)
	case "LIST":
		path := strings.TrimSpace(asString(result["path"]))
		count, _ := asInt(result["count"])
		return fmt.Sprintf("listed %d entries in %s", count, displayFileName(path))
	case "STAT":
		// Suppressed in printEvent; return empty for read-only tools.
		return ""
	case "DELEGATE":
		taskID := strings.TrimSpace(asString(result["task_id"]))
		summary := strings.TrimSpace(firstNonEmpty(result, "assistant", "summary", "output"))
		if taskID != "" && fmt.Sprint(result["running"]) == "true" {
			if preview := compactTaskPreview(firstNonEmpty(result, "latest_output")); preview != "" {
				return fmt.Sprintf("task=%s running\n%s", idutil.ShortDisplay(taskID), preview)
			}
			return fmt.Sprintf("task=%s running", idutil.ShortDisplay(taskID))
		}
		if summary != "" {
			return "\n" + renderDelegateSummaryPreview(summary)
		}
		if state := strings.TrimSpace(asString(result["state"])); state != "" {
			return state
		}
	case "TASK":
		taskID := strings.TrimSpace(asString(result["task_id"]))
		state := strings.TrimSpace(asString(result["state"]))
		output := strings.TrimSpace(firstNonEmptyMap(asMap(result["output"]), "stderr", "stdout", "log"))
		if output == "" {
			output = compactTaskPreview(firstNonEmpty(result, "latest_output"))
		}
		if taskID != "" && output != "" {
			return fmt.Sprintf("task=%s %s\n%s", idutil.ShortDisplay(taskID), state, tailLines(output, 6))
		}
		if taskID != "" && state != "" {
			return fmt.Sprintf("task=%s %s", idutil.ShortDisplay(taskID), state)
		}
	}
	if value := firstNonEmpty(result, "error", "stderr", "message"); value != "" {
		return truncateInline(value, 160)
	}
	if value := firstNonEmpty(result, "summary", "output", "result"); value != "" {
		return truncateInline(value, 160)
	}
	keys := make([]string, 0, len(result))
	for k := range result {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("{keys=%s}", strings.Join(keys, ","))
}

func formatToolResultLine(prefix string, toolName string, summary string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "✓"
	}
	toolName = strings.TrimSpace(toolName)
	summary = strings.TrimRight(summary, "\n")
	if strings.Contains(summary, "\n") || strings.HasPrefix(summary, "\n") {
		body := strings.TrimLeft(summary, "\n")
		if body == "" {
			return fmt.Sprintf("%s %s\n", prefix, toolName)
		}
		return fmt.Sprintf("%s %s\n%s\n", prefix, toolName, body)
	}
	if summary == "" {
		return fmt.Sprintf("%s %s\n", prefix, toolName)
	}
	return fmt.Sprintf("%s %s %s\n", prefix, toolName, summary)
}

func renderDelegateSummaryPreview(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	lines := strings.Split(summary, "\n")
	preview := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := sanitizeDelegatePreviewLine(line, &inFence)
		if trimmed == "" {
			continue
		}
		preview = append(preview, trimmed)
		if len(preview) >= 8 {
			break
		}
	}
	if len(preview) == 0 {
		return summary
	}
	return strings.Join(preview, "\n")
}

func compactTaskPreview(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	preview := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := sanitizeDelegatePreviewLine(line, &inFence)
		if trimmed == "" {
			continue
		}
		preview = append(preview, trimmed)
	}
	if len(preview) == 0 {
		return ""
	}
	return tailLines(strings.Join(preview, "\n"), 4)
}

func sanitizeDelegatePreviewLine(line string, inFence *bool) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "```") {
		if inFence != nil {
			*inFence = !*inFence
		}
		return ""
	}
	if inFence != nil && *inFence {
		return ""
	}
	return trimmed
}

func isMCPToolName(toolName string) bool {
	return strings.Contains(strings.TrimSpace(strings.ToLower(toolName)), "__")
}

func summarizeWebLikeTarget(args map[string]any) string {
	for _, key := range []string{"url", "uri", "endpoint"} {
		if value := strings.TrimSpace(asString(args[key])); value != "" {
			return fmt.Sprintf("{url=%s}", truncateInline(value, 120))
		}
	}
	for _, key := range []string{"query", "q"} {
		if value := strings.TrimSpace(asString(args[key])); value != "" {
			return fmt.Sprintf("{query=%s}", truncateInline(value, 120))
		}
	}
	return ""
}

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return fmt.Sprint(v)
}

func asInt(v any) (int, bool) {
	switch value := v.(type) {
	case int:
		return value, true
	case int8:
		return int(value), true
	case int16:
		return int(value), true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float32:
		return int(value), true
	case float64:
		return int(value), true
	default:
		return 0, false
	}
}

func valueOrDash(v any) string {
	text := strings.TrimSpace(asString(v))
	if text == "" {
		return "-"
	}
	return text
}

func countLines(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func firstNonEmpty(values map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, ok := values[key]
		if !ok {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(raw))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

func asMap(v any) map[string]any {
	value, _ := v.(map[string]any)
	return value
}

func firstNonEmptyMap(values map[string]any, keys ...string) string {
	if len(values) == 0 {
		return ""
	}
	return firstNonEmpty(values, keys...)
}

func truncateInline(input string, limit int) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	rs := []rune(text)
	if limit <= 0 || len(rs) <= limit {
		return text
	}
	if limit <= 3 {
		return string(rs[:limit])
	}
	return string(rs[:limit-3]) + "..."
}

func displayFileName(path string) string {
	text := strings.TrimSpace(path)
	if text == "" {
		return path
	}
	base := filepath.Base(text)
	if strings.TrimSpace(base) == "" || base == "." || base == string(filepath.Separator) {
		return text
	}
	return base
}

// isReadOnlyFSTool returns true for FS tools whose result line can be suppressed.
func isReadOnlyFSTool(toolName string) bool {
	switch strings.ToUpper(strings.TrimSpace(toolName)) {
	case "READ", "STAT":
		return true
	default:
		return false
	}
}

func isFileMutationTool(toolName string) bool {
	switch strings.ToUpper(strings.TrimSpace(toolName)) {
	case "PATCH", "WRITE":
		return true
	default:
		return false
	}
}

// hasToolError returns true when a tool result contains an error field.
func hasToolError(result map[string]any) bool {
	return strings.TrimSpace(asString(result["error"])) != ""
}

// tailLines returns the last n non-empty lines of text.  When the total line
// count exceeds n, a "…" prefix is prepended to indicate truncation.
func tailLines(text string, n int) string {
	if n <= 0 {
		n = 5
	}
	rawLines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return "...\n" + strings.Join(lines[len(lines)-n:], "\n")
}

func eventIsPartial(ev *session.Event) bool {
	if ev == nil || ev.Meta == nil {
		return false
	}
	raw, ok := ev.Meta["partial"]
	if !ok {
		return false
	}
	flag, ok := raw.(bool)
	return ok && flag
}

// isCompactionMeta returns true if the event is an internal compaction summary.
func isCompactionMeta(ev *session.Event) bool {
	if ev == nil || ev.Meta == nil {
		return false
	}
	kind, ok := ev.Meta["kind"].(string)
	return ok && strings.TrimSpace(kind) == "compaction"
}

func eventChannel(ev *session.Event) string {
	if ev == nil || ev.Meta == nil {
		return "answer"
	}
	raw, ok := ev.Meta["channel"]
	if !ok {
		return "answer"
	}
	channel, ok := raw.(string)
	if !ok || channel == "" {
		return "answer"
	}
	return channel
}

// extractLastUsage scans events in reverse to find the most recent usage
// metadata and returns a conservative token floor derived from it.
func extractLastUsage(events []*session.Event) int {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev == nil || ev.Meta == nil {
			continue
		}
		if usageFloor := usageFloorFromMeta(ev.Meta); usageFloor > 0 {
			return usageFloor
		}
	}
	return 0
}

func usageFloorFromMeta(meta map[string]any) int {
	if len(meta) == 0 {
		return 0
	}
	raw, ok := meta["usage"]
	if !ok {
		return 0
	}
	usageMap, ok := raw.(map[string]any)
	if !ok {
		return 0
	}
	prompt := toInt(usageMap["prompt_tokens"])
	completion := toInt(usageMap["completion_tokens"])
	total := toInt(usageMap["total_tokens"])
	if total > 0 {
		return total
	}
	if prompt > 0 || completion > 0 {
		return prompt + completion
	}
	return 0
}

// formatTokenCount returns a human-readable token count string.
// Examples: 500 → "500", 1234 → "1.2k", 21063 → "21.1k", 1234567 → "1.2m".
func formatTokenCount(v int) string {
	if v <= 0 {
		return ""
	}
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(v)/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%.1fk", float64(v)/1_000)
	default:
		return fmt.Sprintf("%d", v)
	}
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return 0
	}
}
