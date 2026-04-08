package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/internal/acpprojector"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuiapp"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	prompt := "演示一下你的工具调用能力"
	if len(os.Args) > 1 {
		prompt = strings.Join(os.Args[1:], " ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	started := time.Now()
	projector := acpprojector.NewLiveProjector()
	block := tuiapp.NewParticipantTurnBlock("probe-session", "probe(copilot)")
	var mu sync.Mutex

	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		prefix := fmt.Sprintf("[%7s] ", time.Since(started).Round(time.Millisecond))
		fmt.Printf(prefix+format+"\n", args...)
	}

	client, err := acpclient.Start(ctx, acpclient.Config{
		Command:    "copilot",
		Args:       []string{"--acp", "--stdio"},
		WorkDir:    mustGetwd(),
		Workspace:  mustGetwd(),
		ClientInfo: acpclient.DefaultClientInfo("probe"),
		OnUpdate: func(env acpclient.UpdateEnvelope) {
			logf("RAW  session=%s update=%s detail=%s", env.SessionID, updateKind(env.Update), describeUpdate(env.Update))
			for _, item := range projector.Project(env) {
				logf("PROJ session=%s stream=%s delta=%q full=%q tool=%s status=%s plan=%d result=%s",
					emptyDefault(item.SessionID, env.SessionID),
					item.Stream,
					item.DeltaText,
					truncate(item.FullText, 120),
					item.ToolName,
					item.ToolStatus,
					len(item.PlanEntries),
					describeMap(item.ToolResult),
				)
				applyToParticipantBlock(block, item)
				logf("BLOCK assistant=%q", truncate(blockAssistantText(block), 240))
			}
		},
	})
	if err != nil {
		logf("start error: %v", err)
		return err
	}
	defer func() {
		_ = client.Close()
	}()

	initResp, err := client.Initialize(ctx)
	if err != nil {
		logf("initialize error: %v", err)
		logf("stderr tail: %s", client.StderrTail(4096))
		return err
	}
	logf("initialized protocol=%d agent=%s", initResp.ProtocolVersion, implName(initResp.AgentInfo))

	newResp, err := client.NewSession(ctx, mustGetwd(), nil)
	if err != nil {
		logf("new session error: %v", err)
		logf("stderr tail: %s", client.StderrTail(4096))
		return err
	}
	sessionID := strings.TrimSpace(newResp.SessionID)
	logf("session/new -> %s", sessionID)

	promptResp, err := client.Prompt(ctx, sessionID, prompt, nil)
	if err != nil {
		logf("prompt error: %v", err)
		logf("stderr tail: %s", client.StderrTail(4096))
		return err
	}
	logf("prompt response stopReason=%s", promptResp.StopReason)

	assistant, reasoning := projector.Snapshot()
	logf("snapshot assistant=%q", truncate(assistant, 240))
	logf("snapshot reasoning=%q", truncate(reasoning, 240))
	logf("block assistant=%q", truncate(blockAssistantText(block), 240))
	if tail := strings.TrimSpace(client.StderrTail(4096)); tail != "" {
		logf("stderr tail: %s", tail)
	}
	return nil
}

func applyToParticipantBlock(block *tuiapp.ParticipantTurnBlock, item acpprojector.Projection) {
	if block == nil {
		return
	}
	switch normalizeProbeStream(item.Stream) {
	case "assistant":
		if strings.TrimSpace(item.DeltaText) != "" {
			block.AppendStreamChunk(tuiapp.SEAssistant, item.DeltaText)
		}
		if strings.TrimSpace(item.FullText) != "" && strings.TrimSpace(item.DeltaText) == "" {
			block.ReplaceFinalStreamChunk(tuiapp.SEAssistant, item.FullText)
		}
	case "reasoning":
		if strings.TrimSpace(item.DeltaText) != "" {
			block.AppendStreamChunk(tuiapp.SEReasoning, item.DeltaText)
		}
		if strings.TrimSpace(item.FullText) != "" && strings.TrimSpace(item.DeltaText) == "" {
			block.ReplaceFinalStreamChunk(tuiapp.SEReasoning, item.FullText)
		}
	}
	if strings.TrimSpace(item.ToolCallID) != "" && strings.TrimSpace(item.ToolName) != "" {
		switch status := strings.ToLower(strings.TrimSpace(item.ToolStatus)); status {
		case "", "in_progress", "running":
			block.UpdateTool(item.ToolCallID, item.ToolName, acpprojector.FormatToolStart(item.ToolName, item.ToolArgs), "", false, false)
		case "completed", "failed":
			block.UpdateTool(
				item.ToolCallID,
				item.ToolName,
				acpprojector.FormatToolStart(item.ToolName, item.ToolArgs),
				acpprojector.FormatToolResult(item.ToolName, item.ToolArgs, item.ToolResult, item.ToolStatus),
				true,
				status == "failed",
			)
		}
	}
}

func blockAssistantText(block *tuiapp.ParticipantTurnBlock) string {
	if block == nil {
		return ""
	}
	var parts []string
	for _, ev := range block.Events {
		if ev.Kind == tuiapp.SEAssistant {
			parts = append(parts, strings.TrimSpace(ev.Text))
		}
	}
	return strings.Join(parts, " || ")
}

func normalizeProbeStream(stream string) string {
	switch strings.ToLower(strings.TrimSpace(stream)) {
	case "answer":
		return "assistant"
	default:
		return strings.ToLower(strings.TrimSpace(stream))
	}
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return wd
}

func implName(info *internalacp.Implementation) string {
	if info == nil {
		return ""
	}
	if strings.TrimSpace(info.Title) != "" {
		return info.Title
	}
	return strings.TrimSpace(info.Name)
}

func updateKind(update any) string {
	switch typed := update.(type) {
	case acpclient.ContentChunk:
		return strings.TrimSpace(typed.SessionUpdate)
	case acpclient.ToolCall:
		return strings.TrimSpace(typed.SessionUpdate)
	case acpclient.ToolCallUpdate:
		return strings.TrimSpace(typed.SessionUpdate)
	case acpclient.PlanUpdate:
		return strings.TrimSpace(typed.SessionUpdate)
	case acpclient.SessionInfoUpdate:
		return strings.TrimSpace(typed.SessionUpdate)
	case acpclient.CurrentModeUpdate:
		return strings.TrimSpace(typed.SessionUpdate)
	default:
		return fmt.Sprintf("%T", update)
	}
}

func describeUpdate(update any) string {
	switch typed := update.(type) {
	case acpclient.ContentChunk:
		return fmt.Sprintf("text=%q", decodeRawText(typed.Content))
	case acpclient.ToolCall:
		return fmt.Sprintf("call=%s title=%q kind=%q input=%s", typed.ToolCallID, typed.Title, typed.Kind, describeAny(typed.RawInput))
	case acpclient.ToolCallUpdate:
		status := ""
		if typed.Status != nil {
			status = *typed.Status
		}
		return fmt.Sprintf("call=%s status=%q title=%q kind=%q output=%s content=%d", typed.ToolCallID, status, deref(typed.Title), deref(typed.Kind), describeAny(typed.RawOutput), len(typed.Content))
	case acpclient.PlanUpdate:
		return fmt.Sprintf("entries=%d", len(typed.Entries))
	case acpclient.SessionInfoUpdate:
		return fmt.Sprintf("title=%q", deref(typed.Title))
	case acpclient.CurrentModeUpdate:
		return fmt.Sprintf("mode=%q", strings.TrimSpace(typed.CurrentModeID))
	default:
		return describeAny(update)
	}
}

func decodeRawText(raw json.RawMessage) string {
	var text struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &text); err != nil {
		return string(raw)
	}
	return text.Text
}

func describeAny(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return truncate(string(data), 160)
}

func describeMap(v map[string]any) string {
	if len(v) == 0 {
		return ""
	}
	return describeAny(v)
}

func emptyDefault(v string, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func truncate(s string, limit int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\n", "\\n"), "\t", " "))
	if limit <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
