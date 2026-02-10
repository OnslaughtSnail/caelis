package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type runRenderConfig struct {
	ShowReasoning bool
	Writer        io.Writer
}

func runOnce(ctx context.Context, rt *runtime.Runtime, req runtime.RunRequest, renderCfg runRenderConfig) error {
	invokeCtx := ctx
	out := renderCfg.Writer
	if out == nil {
		out = os.Stdout
	}
	render := &renderState{
		showReasoning:    renderCfg.ShowReasoning,
		out:              out,
		pendingToolCalls: map[string]toolCallSnapshot{},
	}
	for ev, err := range rt.Run(invokeCtx, req) {
		if err != nil {
			return err
		}
		if ev == nil {
			continue
		}
		printEvent(ev, render)
	}
	if render.partialOpen {
		fmt.Fprintln(render.out)
	}
	return nil
}

type renderState struct {
	partialOpen          bool
	partialChannel       string
	seenAnswerPartial    bool
	seenReasoningPartial bool
	showReasoning        bool
	out                  io.Writer
	pendingToolCalls     map[string]toolCallSnapshot
}

type toolCallSnapshot struct {
	Args map[string]any
}

func printEvent(ev *session.Event, state *renderState) {
	if ev == nil {
		return
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
		if !state.partialOpen {
			if channel == "reasoning" {
				fmt.Fprint(state.out, "~ ")
			} else {
				fmt.Fprint(state.out, "* ")
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
	if msg.Role == model.RoleUser {
		return
	}
	if msg.ToolResponse != nil {
		var callArgs map[string]any
		if state != nil && msg.ToolResponse.ID != "" && state.pendingToolCalls != nil {
			if snapshot, ok := state.pendingToolCalls[msg.ToolResponse.ID]; ok {
				callArgs = snapshot.Args
				delete(state.pendingToolCalls, msg.ToolResponse.ID)
			}
		}
		fmt.Fprintf(state.out, "= %s %s\n", msg.ToolResponse.Name, summarizeToolResponseWithCall(msg.ToolResponse.Name, msg.ToolResponse.Result, callArgs))
		return
	}
	if len(msg.ToolCalls) > 0 {
		for i, call := range msg.ToolCalls {
			if state != nil && call.ID != "" {
				if state.pendingToolCalls == nil {
					state.pendingToolCalls = map[string]toolCallSnapshot{}
				}
				state.pendingToolCalls[call.ID] = toolCallSnapshot{
					Args: cloneAnyMap(call.Args),
				}
			}
			fmt.Fprintf(state.out, "#%d %s %s\n", i+1, call.Name, summarizeToolArgs(call.Name, call.Args))
		}
		return
	}
	if msg.Role == model.RoleAssistant && state != nil && state.showReasoning && msg.Reasoning != "" && !state.seenReasoningPartial {
		fmt.Fprintf(state.out, "~ %s\n", strings.TrimSpace(msg.Reasoning))
	}
	if msg.Role == model.RoleAssistant && state != nil && state.seenAnswerPartial && msg.Text != "" {
		// Streaming mode already printed partial answer chunks.
	} else {
		text := strings.TrimSpace(msg.Text)
		if text != "" {
			switch msg.Role {
			case model.RoleAssistant:
				fmt.Fprintf(state.out, "* %s\n", text)
			case model.RoleSystem:
				fmt.Fprintf(state.out, "! %s\n", text)
			default:
				fmt.Fprintf(state.out, "- %s\n", text)
			}
		}
	}
	if state != nil && msg.Role == model.RoleAssistant {
		state.seenAnswerPartial = false
		state.seenReasoningPartial = false
	}
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
	case "READ":
		path := strings.TrimSpace(asString(args["path"]))
		if path != "" {
			return fmt.Sprintf("{path=%s, offset=%s, limit=%s}", path, valueOrDash(args["offset"]), valueOrDash(args["limit"]))
		}
	case "PATCH":
		path := strings.TrimSpace(asString(args["path"]))
		oldValue := asString(args["old"])
		newValue := asString(args["new"])
		return fmt.Sprintf("{path=%s, lines -%d/+%d}", path, countLines(oldValue), countLines(newValue))
	case "WRITE":
		path := strings.TrimSpace(asString(args["path"]))
		content := asString(args["content"])
		return fmt.Sprintf("{path=%s, lines=%d}", path, countLines(content))
	case "SEARCH":
		path := strings.TrimSpace(asString(args["path"]))
		query := strings.TrimSpace(asString(args["query"]))
		return fmt.Sprintf("{path=%s, query=%s}", path, truncateInline(query, 60))
	case "GLOB":
		pattern := strings.TrimSpace(asString(args["pattern"]))
		if pattern != "" {
			return fmt.Sprintf("{pattern=%s}", pattern)
		}
	case "LIST", "STAT":
		path := strings.TrimSpace(asString(args["path"]))
		if path != "" {
			return fmt.Sprintf("{path=%s}", path)
		}
	case "LSP_ACTIVATE":
		language := strings.TrimSpace(asString(args["language"]))
		workspace := strings.TrimSpace(asString(args["workspace"]))
		if workspace != "" {
			return fmt.Sprintf("{language=%s, workspace=%s}", language, workspace)
		}
		if language != "" {
			return fmt.Sprintf("{language=%s}", language)
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

func summarizeToolResponse(toolName string, result map[string]any) string {
	return summarizeToolResponseWithCall(toolName, result, nil)
}

func summarizeToolResponseWithCall(toolName string, result map[string]any, callArgs map[string]any) string {
	if len(result) == 0 {
		return "{}"
	}
	switch strings.ToUpper(strings.TrimSpace(toolName)) {
	case "READ":
		path := strings.TrimSpace(asString(result["path"]))
		start, _ := asInt(result["start_line"])
		end, _ := asInt(result["end_line"])
		nextOffset, _ := asInt(result["next_offset"])
		hasMore := fmt.Sprint(result["has_more"]) == "true"
		if path != "" {
			display := displayFileName(path)
			if start > 0 && end > 0 {
				if hasMore {
					return fmt.Sprintf("read %s lines %d-%d (truncated, next_offset=%d)", display, start, end, nextOffset)
				}
				return fmt.Sprintf("read %s lines %d-%d", display, start, end)
			}
			return fmt.Sprintf("read %s (empty)", display)
		}
	case "PATCH":
		path := strings.TrimSpace(asString(result["path"]))
		replaced, _ := asInt(result["replaced"])
		oldCount, _ := asInt(result["old_count"])
		created := fmt.Sprint(result["created"]) == "true"
		preview := patchPreviewFromEvent(callArgs)
		if strings.TrimSpace(preview) == "" {
			preview = patchPreviewFromMetadata(result)
		}
		hunk := patchHunkFromResult(result)
		if strings.TrimSpace(hunk) != "" && strings.TrimSpace(preview) != "" {
			preview = hunk + "\n" + preview
		}
		if path != "" {
			action := "edited"
			if created {
				action = "created"
			}
			summary := fmt.Sprintf("%s %s (replaced=%d/%d)", action, path, replaced, oldCount)
			if strings.TrimSpace(preview) == "" {
				return summary
			}
			return summary + "\n" + indentMultiline(preview, "  ")
		}
	case "WRITE":
		path := strings.TrimSpace(asString(result["path"]))
		created := fmt.Sprint(result["created"]) == "true"
		lineCount, _ := asInt(result["line_count"])
		if path != "" {
			if created {
				return fmt.Sprintf("created %s (%d lines)", path, lineCount)
			}
			return fmt.Sprintf("wrote %s (%d lines)", path, lineCount)
		}
	case "SEARCH":
		path := strings.TrimSpace(asString(result["path"]))
		count, _ := asInt(result["count"])
		fileCount, _ := asInt(result["file_count"])
		truncated := fmt.Sprint(result["truncated"]) == "true"
		if truncated {
			return fmt.Sprintf("found %d matches in %d files under %s (truncated)", count, fileCount, path)
		}
		return fmt.Sprintf("found %d matches in %d files under %s", count, fileCount, path)
	case "GLOB":
		pattern := strings.TrimSpace(asString(result["pattern"]))
		count, _ := asInt(result["count"])
		return fmt.Sprintf("matched %d paths for %s", count, pattern)
	case "LIST":
		path := strings.TrimSpace(asString(result["path"]))
		count, _ := asInt(result["count"])
		return fmt.Sprintf("listed %d entries in %s", count, path)
	case "STAT":
		path := strings.TrimSpace(asString(result["path"]))
		size, _ := asInt(result["size"])
		isDir := fmt.Sprint(result["is_dir"]) == "true"
		if isDir {
			return fmt.Sprintf("directory %s", path)
		}
		return fmt.Sprintf("file %s (size=%d)", path, size)
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

const (
	patchPreviewSideLines = 4
	patchPreviewLineWidth = 120
)

func patchPreviewFromEvent(callArgs map[string]any) string {
	if len(callArgs) == 0 {
		return ""
	}
	oldValue, oldOK := callArgs["old"].(string)
	newValue, newOK := callArgs["new"].(string)
	if !oldOK && !newOK {
		return ""
	}
	return buildPatchPreview(oldValue, newValue)
}

func patchPreviewFromMetadata(result map[string]any) string {
	patchMeta := patchMetadataFromResult(result)
	preview, _ := patchMeta["preview"].(string)
	return preview
}

func patchHunkFromResult(result map[string]any) string {
	patchMeta := patchMetadataFromResult(result)
	hunk, _ := patchMeta["hunk"].(string)
	return hunk
}

func patchMetadataFromResult(result map[string]any) map[string]any {
	if len(result) == 0 {
		return nil
	}
	metadata, _ := result["metadata"].(map[string]any)
	patchMeta, _ := metadata["patch"].(map[string]any)
	return patchMeta
}

func buildPatchPreview(oldValue, newValue string) string {
	oldLines, oldTruncated := buildPatchSide(oldValue, "-")
	newLines, newTruncated := buildPatchSide(newValue, "+")
	if len(oldLines) == 0 && len(newLines) == 0 {
		return ""
	}
	lines := make([]string, 0, 2+len(oldLines)+len(newLines)+1)
	lines = append(lines, "--- old", "+++ new")
	lines = append(lines, oldLines...)
	lines = append(lines, newLines...)
	if oldTruncated || newTruncated {
		lines = append(lines, "... (preview truncated)")
	}
	return strings.Join(lines, "\n")
}

func buildPatchSide(content, prefix string) ([]string, bool) {
	if content == "" {
		return nil, false
	}
	if strings.HasSuffix(content, "\n") {
		content = strings.TrimSuffix(content, "\n")
	}
	rawLines := strings.Split(content, "\n")
	truncated := false
	if len(rawLines) > patchPreviewSideLines {
		rawLines = rawLines[:patchPreviewSideLines]
		truncated = true
	}
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		lines = append(lines, prefix+truncatePatchLine(line, patchPreviewLineWidth))
	}
	return lines, truncated
}

func truncatePatchLine(line string, width int) string {
	rs := []rune(line)
	if width <= 0 || len(rs) <= width {
		return line
	}
	if width <= 3 {
		return string(rs[:width])
	}
	return string(rs[:width-3]) + "..."
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

func indentMultiline(input, indent string) string {
	if input == "" {
		return ""
	}
	lines := strings.Split(input, "\n")
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
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
