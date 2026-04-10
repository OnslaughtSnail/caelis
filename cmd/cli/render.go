package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/pkg/idutil"
)

const defaultFriendlyWaitMS = 5000

type runRenderConfig struct {
	ShowReasoning bool
	Verbose       bool
	Writer        io.Writer
	UI            *ui
	OnUsage       func(int) // called with conservative usage floor after run completes
	OnEvent       func(*session.Event) bool
}

func runRunner(runner runtime.Runner, renderCfg runRenderConfig) error {
	if runner == nil {
		return fmt.Errorf("runtime: runner is nil")
	}
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
	for ev, err := range runner.Events() {
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
	Name          string
	Args          map[string]any
	RichDiffShown bool
	ChangeCounts  mutationChangeCounts
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
		chunk := ev.Message.TextContent()
		if channel == "reasoning" {
			chunk = ev.Message.ReasoningText()
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
	if notice, ok := session.EventNotice(ev); ok {
		renderNotice(notice, state)
		return
	}

	msg := ev.Message
	if msg.Role == model.RoleSystem {
		text := strings.TrimSpace(msg.TextContent())
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
			fmt.Fprintf(out, "%s%s\n", state.ui.SystemPrefix(), text)
		} else {
			fmt.Fprintln(out, text)
		}
		return
	}
	if msg.Role == model.RoleUser {
		if state != nil && state.replayUserMessages {
			userText := visibleUserText(msg)
			if userText == "" {
				return
			}
			fmt.Fprintf(state.out, "> %s\n", userText)
		}
		return
	}
	if msg.Role == model.RoleAssistant {
		reasoning := strings.TrimSpace(msg.ReasoningText())
		if state != nil && state.showReasoning && reasoning != "" && !state.seenReasoningPartial {
			if state.ui != nil {
				fmt.Fprintf(state.out, "%s%s\n", state.ui.ReasoningPrefix(), reasoning)
			} else {
				fmt.Fprintf(state.out, "│ %s\n", reasoning)
			}
		}
		text := strings.TrimSpace(msg.TextContent())
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
	if calls := msg.ToolCalls(); len(calls) > 0 {
		for i, call := range calls {
			parsedArgs := parseToolArgsForDisplay(call.Args)
			if state != nil && call.ID != "" {
				if state.pendingToolCalls == nil {
					state.pendingToolCalls = map[string]toolCallSnapshot{}
				}
				state.pendingToolCalls[call.ID] = toolCallSnapshot{
					Args: cloneAnyMap(parsedArgs),
				}
			}
			displayName := displayToolCallName(call.Name, parsedArgs)
			summary := formatToolCallSummary(state.ui, call.Name, parsedArgs, "")
			if state.ui != nil {
				fmt.Fprintf(state.out, "%s%s %s\n", state.ui.ToolCallPrefix(i+1), displayName, summary)
			} else {
				fmt.Fprintf(state.out, "▸ %s %s\n", displayName, summary)
			}
		}
	}
	if resp := msg.ToolResponse(); resp != nil {
		var callArgs map[string]any
		if state != nil && resp.ID != "" && state.pendingToolCalls != nil {
			if snapshot, ok := state.pendingToolCalls[resp.ID]; ok {
				callArgs = snapshot.Args
				delete(state.pendingToolCalls, resp.ID)
			}
		}
		// Suppress result line for read-only FS tools (the call line is sufficient).
		if isReadOnlyFSTool(resp.Name) && !hasToolError(resp.Result) {
			return
		}
		summary := summarizeToolResponseWithCall(resp.Name, resp.Result, callArgs)
		if strings.TrimSpace(summary) == "" {
			return
		}
		displayName := displayToolResponseName(resp.Name, callArgs, resp.Result)
		if state.ui != nil {
			fmt.Fprint(state.out, formatToolResultLine(state.ui.ToolResultPrefix(), displayName, summary))
		} else {
			fmt.Fprint(state.out, formatToolResultLine("✓ ", displayName, summary))
		}
		return
	}
	if msg.Role == model.RoleAssistant {
		return
	}
	text := strings.TrimSpace(msg.TextContent())
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

func renderNotice(notice session.Notice, state *renderState) {
	text := renderNoticeText(notice)
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
		switch notice.Level {
		case session.NoticeLevelWarn:
			state.ui.Warn("%s\n", text)
		case session.NoticeLevelNote:
			state.ui.Note("%s\n", text)
		default:
			fmt.Fprintf(out, "%s%s\n", state.ui.SystemPrefix(), text)
		}
		return
	}
	switch notice.Level {
	case session.NoticeLevelWarn:
		fmt.Fprintf(out, "! %s\n", text)
	case session.NoticeLevelNote:
		fmt.Fprintf(out, "note: %s\n", text)
	default:
		fmt.Fprintln(out, text)
	}
}

// renderNoticeText produces the human-presentable text for a notice. For
// structured notices (Kind != ""), it renders from the Kind; for legacy
// notices, it returns the raw Text.
func renderNoticeText(notice session.Notice) string {
	if notice.Kind == "" {
		return strings.TrimSpace(notice.Text)
	}
	if notice.Kind == "compaction_notice" {
		phase, _ := notice.Meta["compaction_phase"].(string)
		trigger, _ := notice.Meta["compaction_trigger"].(string)
		before, beforeOK := noticeIntField(notice.Meta, "pre_tokens")
		after, afterOK := noticeIntField(notice.Meta, "post_tokens")
		switch strings.TrimSpace(phase) {
		case "start":
			if beforeOK {
				return fmt.Sprintf("compaction started (%s, %d tokens)", fallbackNoticeValue(trigger, "auto"), before)
			}
			return fmt.Sprintf("compaction started (%s)", fallbackNoticeValue(trigger, "auto"))
		case "done":
			switch {
			case beforeOK && afterOK:
				return fmt.Sprintf("compaction finished (%s, %d -> %d tokens)", fallbackNoticeValue(trigger, "auto"), before, after)
			case afterOK:
				return fmt.Sprintf("compaction finished (%s, now %d tokens)", fallbackNoticeValue(trigger, "auto"), after)
			default:
				return fmt.Sprintf("compaction finished (%s)", fallbackNoticeValue(trigger, "auto"))
			}
		}
	}
	return strings.TrimSpace(notice.Text)
}

func noticeIntField(meta map[string]any, key string) (int, bool) {
	if meta == nil {
		return 0, false
	}
	switch v := meta[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

func fallbackNoticeValue(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
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

func visibleUserText(msg model.Message) string {
	text := sessionmode.VisibleText(strings.TrimSpace(userMessageDisplayText(msg)))
	return strings.TrimSpace(stripHiddenInputReferenceHints(text))
}

func userMessageDisplayText(msg model.Message) string {
	contentParts := model.ContentPartsFromParts(msg.Parts)
	segments := make([]string, 0, max(1, len(contentParts)))
	if len(contentParts) > 0 {
		for _, part := range contentParts {
			switch part.Type {
			case model.ContentPartText:
				text := strings.TrimSpace(part.Text)
				if text != "" {
					segments = append(segments, text)
				}
			case model.ContentPartImage:
				name := strings.TrimSpace(part.FileName)
				if name == "" {
					name = "image"
				}
				segments = append(segments, "[image: "+name+"]")
			}
		}
		if len(segments) > 0 {
			return strings.Join(segments, " ")
		}
	}
	text := strings.TrimSpace(msg.TextContent())
	if text != "" {
		return text
	}
	return userTextFromContentParts(contentParts)
}

func summarizeToolArgs(toolName string, args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	if summary := summarizeACPToolArgs(toolName, args); summary != "" {
		return summary
	}
	switch strings.ToUpper(strings.TrimSpace(toolName)) {
	case "BASH":
		command := strings.TrimSpace(asString(args["command"]))
		if command != "" {
			return truncateInline(command, 120)
		}
	case "TASK":
		action := strings.TrimSpace(asString(args["action"]))
		if strings.EqualFold(action, "wait") {
			if waited := friendlyWaitLabel(effectiveTaskWaitMS(action, args)); waited != "" {
				return waited
			}
			return ""
		}
		switch strings.ToLower(action) {
		case "list":
			return "list"
		case "status", "cancel":
			return ""
		case "write":
			if preview := summarizeTaskWriteInput(args); preview != "" {
				return preview
			}
			return ""
		default:
			taskID := strings.TrimSpace(asString(args["task_id"]))
			if action != "" && taskID != "" {
				return fmt.Sprintf("{action=%s task=%s}", action, idutil.ShortDisplay(taskID))
			}
			if action != "" {
				return fmt.Sprintf("{action=%s}", action)
			}
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
	case "LIST":
		path := strings.TrimSpace(asString(args["path"]))
		if path != "" {
			return displayFileName(path)
		}
	case "SPAWN":
		prompt := strings.TrimSpace(asString(args["prompt"]))
		if prompt != "" {
			return strings.Join(strings.Fields(prompt), " ")
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

func formatToolCallSummary(ui *ui, toolName string, args map[string]any, defaultSpawnAgent string) string {
	summary := summarizeToolArgs(toolName, args)
	if !strings.EqualFold(strings.TrimSpace(toolName), "SPAWN") {
		return summary
	}
	agentName := strings.TrimSpace(asString(args["agent"]))
	if agentName == "" {
		agentName = strings.TrimSpace(defaultSpawnAgent)
	}
	if agentName == "" {
		return summary
	}
	agentTag := "[" + agentName + "]"
	if ui != nil && ui.cmdColor != nil {
		agentTag = ui.cmdColor.Sprint(agentTag)
	}
	if strings.TrimSpace(summary) == "" {
		return agentTag
	}
	return agentTag + " " + summary
}

func displayToolCallName(toolName string, callArgs map[string]any) string {
	displayName := strings.TrimSpace(toolName)
	if strings.EqualFold(displayName, "TASK") {
		return taskActionCallDisplayName(strings.TrimSpace(asString(callArgs["action"])))
	}
	return displayName
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

func summarizeCompactToolResponseForTUI(toolName string, result map[string]any) string {
	if len(result) == 0 {
		return ""
	}
	switch strings.ToUpper(strings.TrimSpace(toolName)) {
	case "READ":
		startLine, startOK := asInt(result["start_line"])
		endLine, endOK := asInt(result["end_line"])
		if startOK && endOK && startLine > 0 && endLine >= startLine {
			if startLine == endLine {
				return fmt.Sprintf("%d", startLine)
			}
			return fmt.Sprintf("%d-%d", startLine, endLine)
		}
		count := countLines(asString(result["content"]))
		return fmt.Sprintf("%d lines", count)
	case "SEARCH":
		count, _ := asInt(result["count"])
		fileCount, _ := asInt(result["file_count"])
		parts := []string{fmt.Sprintf("%d matches", count)}
		if fileCount > 0 {
			parts = append(parts, fmt.Sprintf("%d files", fileCount))
		}
		if fmt.Sprint(result["truncated"]) == "true" {
			parts = append(parts, "truncated")
		}
		return strings.Join(parts, ", ")
	case "LIST":
		count, _ := asInt(result["count"])
		return fmt.Sprintf("%d entries", count)
	case "GLOB":
		count, _ := asInt(result["count"])
		return fmt.Sprintf("%d paths", count)
	default:
		return ""
	}
}

func summarizeToolResponseWithCall(toolName string, result map[string]any, callArgs map[string]any) string {
	if len(result) == 0 {
		return "{}"
	}
	if summary := summarizeACPToolResult(result); summary != "" {
		return summary
	}
	switch strings.ToUpper(strings.TrimSpace(toolName)) {
	case "BASH":
		if errText := summarizeBashToolError(result); errText != "" {
			return errText
		}
		if firstNonEmpty(result, "task_id") != "" {
			message := compactTaskPreview(userFacingTaskMessage(toolStatusMessage(result)))
			progress := compactTaskPreview(toolResultValue(result))
			switch {
			case message != "" && progress != "":
				return message + "\n" + progress
			case message != "":
				return message
			case progress != "":
				return progress
			}
			return "task yielded before completion"
		}
		if output := strings.TrimSpace(toolResultValue(result)); output != "" {
			return tailLines(output, 5)
		}
		if label := friendlyTaskStateLabel(asString(result["state"]), false); label != "" {
			return strings.ToLower(label)
		}
		return "failed"
	case "READ":
		// Suppressed in printEvent; return empty for read-only tools.
		return ""
	case "PATCH":
		path := strings.TrimSpace(asString(result["path"]))
		created := fmt.Sprint(result["created"]) == "true"
		display := displayFileName(path)
		if path != "" {
			if !created && mutationResultHasNoChanges(result) {
				return fmt.Sprintf("unchanged %s", display)
			}
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
			if !created && mutationResultHasNoChanges(result) {
				return fmt.Sprintf("unchanged %s", display)
			}
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
	case "SPAWN":
		summary := strings.TrimSpace(firstNonEmpty(result, "result", "output", "final_result", "final_summary", "summary"))
		if firstNonEmpty(result, "task_id") != "" && taskStateIsActive(asString(result["state"])) {
			if message := compactTaskPreview(userFacingTaskMessage(toolStatusMessage(result))); message != "" {
				return message
			}
			return "task yielded before completion"
		}
		if summary != "" {
			return "\n" + renderSpawnSummaryPreview(summary)
		}
		if label := friendlyTaskStateLabel(asString(result["state"]), false); label != "" {
			return label
		}
	case "TASK":
		action := strings.TrimSpace(asString(callArgs["action"]))
		return summarizeTaskAction(action, callArgs, result)
	}
	if value := firstNonEmpty(result, "error", "msg", "message"); value != "" {
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

func summarizeACPToolArgs(toolName string, args map[string]any) string {
	title := strings.TrimSpace(asString(args["_acp_title"]))
	if title != "" {
		fields := strings.Fields(title)
		if len(fields) > 1 {
			return strings.TrimSpace(strings.TrimPrefix(title, fields[0]))
		}
	}
	parsed, _ := args["parsed_cmd"].(map[string]any)
	parsedType := strings.ToLower(strings.TrimSpace(asString(parsed["type"])))
	switch strings.ToUpper(strings.TrimSpace(toolName)) {
	case "LIST":
		if path := firstNonEmptyText(asString(parsed["path"]), asString(args["path"]), asString(args["cwd"])); path != "" {
			return displayFileName(path)
		}
		if parsedType == "list_files" {
			return "."
		}
	case "READ", "WRITE", "PATCH":
		if path := firstNonEmptyText(asString(parsed["path"]), asString(args["path"]), asString(args["target"])); path != "" {
			return displayFileName(path)
		}
	case "SEARCH":
		path := firstNonEmptyText(asString(parsed["path"]), asString(args["path"]))
		query := firstNonEmptyText(asString(parsed["query"]), asString(args["query"]), asString(args["pattern"]))
		if path != "" || query != "" {
			return fmt.Sprintf("%s {query=%s}", displayFileName(path), truncateInline(query, 60))
		}
	case "GLOB":
		if pattern := firstNonEmptyText(asString(parsed["pattern"]), asString(args["pattern"])); pattern != "" {
			return fmt.Sprintf("{pattern=%s}", pattern)
		}
	}
	if command := strings.TrimSpace(asString(args["command"])); command != "" {
		return truncateInline(command, 120)
	}
	if raw := args["_acp_raw_input"]; raw != nil {
		return truncateInline(fmt.Sprint(raw), 120)
	}
	return ""
}

func summarizeACPToolResult(result map[string]any) string {
	if len(result) == 0 {
		return ""
	}
	if formatted := strings.TrimSpace(asString(result["formatted_output"])); formatted != "" {
		return truncateInline(formatted, 160)
	}
	if aggregated := strings.TrimSpace(asString(result["aggregated_output"])); aggregated != "" {
		return truncateInline(aggregated, 160)
	}
	if stdout := strings.TrimSpace(asString(result["stdout"])); stdout != "" {
		return truncateInline(stdout, 160)
	}
	if stderr := strings.TrimSpace(asString(result["stderr"])); stderr != "" {
		return truncateInline(stderr, 160)
	}
	for _, key := range []string{"content", "detailedContent", "text"} {
		if value := strings.TrimSpace(extractACPTextValue(result[key])); value != "" {
			return truncateInline(value, 160)
		}
	}
	return ""
}

func extractACPTextValue(raw any) string {
	switch typed := raw.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if value := strings.TrimSpace(extractACPTextValue(item)); value != "" {
				parts = append(parts, value)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"text", "value", "content", "detailedContent"} {
			if value := strings.TrimSpace(extractACPTextValue(typed[key])); value != "" {
				return value
			}
		}
	}
	return strings.TrimSpace(asString(raw))
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

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func mutationResultHasNoChanges(result map[string]any) bool {
	if len(result) == 0 {
		return false
	}
	added, addedOK := asInt(result["added_lines"])
	removed, removedOK := asInt(result["removed_lines"])
	return addedOK && removedOK && added == 0 && removed == 0
}

func renderSpawnSummaryPreview(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	lines := strings.Split(summary, "\n")
	preview := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := sanitizeSpawnPreviewLine(line, &inFence)
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
		trimmed := sanitizeSpawnPreviewLine(line, &inFence)
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

func summarizeTaskAction(action string, _ map[string]any, result map[string]any) string {
	messageLine := firstTaskMessageLine(toolStatusMessage(result))
	messagePreview := compactTaskPreview(userFacingTaskMessage(toolStatusMessage(result)))
	output := compactTaskPreview(toolResultValue(result))
	stateLabel := friendlyTaskStateLabel(asString(result["state"]), false)

	switch strings.ToLower(strings.TrimSpace(action)) {
	case "wait":
		if errText := strings.TrimSpace(asString(result["error"])); errText != "" {
			return truncateInline(errText, 160)
		}
		if output != "" && !taskStateIsActive(asString(result["state"])) {
			return output
		}
		if strings.TrimSpace(asString(result["task_id"])) != "" && taskStateIsActive(asString(result["state"])) {
			return "task yielded before completion"
		}
		if stateLabel != "" {
			return stateLabel
		}
		if output != "" {
			return output
		}
		if messageLine != "" {
			return messageLine
		}
	case "status":
		if errText := strings.TrimSpace(asString(result["error"])); errText != "" {
			return truncateInline(errText, 160)
		}
		if messagePreview != "" {
			return messagePreview
		}
		if stateLabel != "" {
			return stateLabel
		}
	case "write":
		if errText := strings.TrimSpace(asString(result["error"])); errText != "" {
			return truncateInline(errText, 160)
		}
		return ""
	case "cancel":
		if output != "" {
			return output
		}
		if strings.EqualFold(strings.TrimSpace(asString(result["state"])), "cancelled") {
			return ""
		}
		if stateLabel != "" {
			return stateLabel
		}
		return ""
	case "list":
		count := taskListCount(result["tasks"])
		runningCount := countRunningTasks(result["tasks"])
		if count == 1 {
			if runningCount == 1 {
				return "listed 1 task (1 active)"
			}
			return "listed 1 task"
		}
		if count > 0 {
			if runningCount > 0 {
				return fmt.Sprintf("listed %d tasks (%d active)", count, runningCount)
			}
			return fmt.Sprintf("listed %d tasks", count)
		}
		return "listed tasks"
	}
	return ""
}

func taskListCount(raw any) int {
	items, ok := raw.([]any)
	if ok {
		return len(items)
	}
	switch value := raw.(type) {
	case []map[string]any:
		return len(value)
	default:
		return 0
	}
}

func firstTaskMessageLine(text string) string {
	text = userFacingTaskMessage(text)
	if text == "" {
		return ""
	}
	line, _, _ := strings.Cut(text, "\n")
	return strings.TrimSpace(line)
}

func toolStatusMessage(result map[string]any) string {
	return firstNonEmpty(result, "msg", "message")
}

func toolResultValue(result map[string]any) string {
	return firstNonEmpty(result, "result", "output", "stdout", "stderr")
}

func taskStateIsActive(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "running", "waiting_input", "waiting_approval":
		return true
	default:
		return false
	}
}

func userFacingTaskMessage(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return ""
	}
	if idx := strings.Index(lines[0], "; use TASK with task_id "); idx >= 0 {
		lines[0] = strings.TrimSpace(lines[0][:idx])
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func displayToolResponseName(toolName string, callArgs map[string]any, _ map[string]any) string {
	displayName := strings.TrimSpace(toolName)
	if strings.EqualFold(displayName, "TASK") {
		return taskActionResultDisplayName(strings.TrimSpace(asString(callArgs["action"])))
	}
	return displayName
}

func taskActionCallDisplayName(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "wait":
		return "WAIT"
	case "status":
		return "CHECK"
	case "write":
		return runtime.SubagentContinuationAnchorTool
	case "cancel":
		return "CANCEL"
	case "list":
		return "TASK"
	default:
		return "TASK"
	}
}

func taskActionResultDisplayName(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "wait":
		return "WAITED"
	case "cancel":
		return "CANCELLED"
	default:
		return taskActionCallDisplayName(action)
	}
}

func friendlyTaskStateLabel(state string, running bool) string {
	if running || strings.EqualFold(strings.TrimSpace(state), "running") {
		return "Running"
	}
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "waiting_input":
		return "Waiting for input"
	case "waiting_approval":
		return "Waiting for approval"
	case "completed":
		return "Completed"
	case "cancelled":
		return "Cancelled"
	case "failed":
		return "Failed"
	case "interrupted":
		return "Interrupted"
	case "terminated":
		return "Terminated"
	default:
		return ""
	}
}

func countRunningTasks(raw any) int {
	items, ok := raw.([]any)
	if !ok {
		return 0
	}
	count := 0
	for _, item := range items {
		one, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(asString(one["task_id"])) == "" {
			continue
		}
		state := strings.ToLower(strings.TrimSpace(asString(one["state"])))
		switch state {
		case "running", "waiting_input", "waiting_approval":
			count++
			continue
		case "completed", "failed", "cancelled", "canceled", "interrupted", "terminated":
			continue
		}
	}
	return count
}

func summarizeTaskWriteInput(args map[string]any) string {
	input := asString(args["input"])
	if input == "" {
		return ""
	}
	return truncateTaskWriteInput(input, 120)
}

func truncateTaskWriteInput(input string, limit int) string {
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

func friendlyWaitLabel(waitMS int) string {
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

func effectiveTaskWaitMS(action string, args map[string]any) int {
	if !strings.EqualFold(strings.TrimSpace(action), "wait") {
		return -1
	}
	if len(args) == 0 {
		return defaultFriendlyWaitMS
	}
	rawWaitMS, ok := args["yield_time_ms"]
	if !ok || rawWaitMS == nil {
		return defaultFriendlyWaitMS
	}
	waitMS, ok := asInt(rawWaitMS)
	if !ok {
		return defaultFriendlyWaitMS
	}
	if waitMS <= 0 {
		return defaultFriendlyWaitMS
	}
	return waitMS
}

func sanitizeSpawnPreviewLine(line string, inFence *bool) string {
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

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	maps.Copy(out, input)
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
	case "READ":
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

func summarizeBashToolError(result map[string]any) string {
	errText := strings.TrimSpace(asString(result["error"]))
	if errText == "" {
		return ""
	}
	errText = strings.TrimPrefix(errText, "tool: ")
	const failedPrefix = "BASH failed (route="
	lower := strings.ToLower(errText)
	if idx := strings.Index(lower, strings.ToLower(failedPrefix)); idx >= 0 {
		trimmed := errText[idx+len(failedPrefix):]
		if routePart, afterClose, cutOK := strings.Cut(trimmed, ")"); cutOK {
			route := strings.TrimSpace(routePart)
			rest := strings.TrimSpace(strings.TrimPrefix(afterClose, ":"))
			rest = strings.TrimPrefix(rest, "tool: ")
			if route != "" && rest != "" {
				errText = route + " route failed: " + rest
			} else if rest != "" {
				errText = rest
			}
		}
	}
	if stderr := strings.TrimSpace(asString(result["stderr"])); stderr != "" && !strings.Contains(errText, stderr) {
		return truncateInline(errText, 160) + "\n" + tailLines(stderr, 6)
	}
	return truncateInline(errText, 160)
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
	return session.IsPartial(ev)
}

func eventChannel(ev *session.Event) string {
	return string(session.PartialChannelOf(ev))
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
