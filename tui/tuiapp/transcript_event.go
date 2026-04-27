package tuiapp

import (
	"strings"
	"time"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	"github.com/OnslaughtSnail/caelis/tui/acpprojector"
	"github.com/OnslaughtSnail/caelis/tui/tuikit"
)

type TranscriptEventKind string

const (
	TranscriptEventNarrative   TranscriptEventKind = "narrative"
	TranscriptEventNotice      TranscriptEventKind = "notice"
	TranscriptEventPlan        TranscriptEventKind = "plan"
	TranscriptEventTool        TranscriptEventKind = "tool"
	TranscriptEventApproval    TranscriptEventKind = "approval"
	TranscriptEventParticipant TranscriptEventKind = "participant"
	TranscriptEventLifecycle   TranscriptEventKind = "lifecycle"
	TranscriptEventUsage       TranscriptEventKind = "usage"
)

type TranscriptNarrativeKind string

const (
	TranscriptNarrativeUser      TranscriptNarrativeKind = "user"
	TranscriptNarrativeAssistant TranscriptNarrativeKind = "assistant"
	TranscriptNarrativeReasoning TranscriptNarrativeKind = "reasoning"
	TranscriptNarrativeSystem    TranscriptNarrativeKind = "system"
	TranscriptNarrativeNotice    TranscriptNarrativeKind = "notice"
)

type TranscriptEvent struct {
	Kind       TranscriptEventKind
	Scope      ACPProjectionScope
	ScopeID    string
	Actor      string
	OccurredAt time.Time

	NarrativeKind TranscriptNarrativeKind
	Text          string
	Final         bool

	ToolCallID string
	ToolName   string
	ToolArgs   string
	ToolOutput string
	ToolStream string
	ToolStatus string
	ToolError  bool
	ToolTaskID string

	PlanEntries []PlanEntry

	ApprovalTool    string
	ApprovalCommand string
	ApprovalStatus  string

	State string

	Usage *appgateway.UsageSnapshot
}

func ProjectGatewayEventToTranscriptEvents(ev appgateway.Event) []TranscriptEvent {
	scope := gatewayEventScope(ev)
	scopeID := gatewayEventScopeID(ev)
	occurredAt := ev.OccurredAt
	out := make([]TranscriptEvent, 0, 4)

	appendUsage := func() {
		if ev.Usage == nil {
			return
		}
		usage := *ev.Usage
		out = append(out, TranscriptEvent{
			Kind:       TranscriptEventUsage,
			Scope:      scope,
			ScopeID:    scopeID,
			OccurredAt: occurredAt,
			Usage:      &usage,
		})
	}

	switch ev.Kind {
	case appgateway.EventKindUserMessage:
		if text := strings.TrimSpace(gatewayUserText(ev)); text != "" {
			out = append(out, TranscriptEvent{
				Kind:          TranscriptEventNarrative,
				Scope:         scope,
				ScopeID:       scopeID,
				OccurredAt:    occurredAt,
				NarrativeKind: TranscriptNarrativeUser,
				Text:          text,
				Final:         true,
			})
		}
	case appgateway.EventKindAssistantMessage:
		payload := ev.Narrative
		if payload != nil {
			actor := strings.TrimSpace(payload.Actor)
			switch payload.Role {
			case appgateway.NarrativeRoleUser:
				if text := strings.TrimSpace(payload.Text); text != "" {
					out = append(out, TranscriptEvent{
						Kind:          TranscriptEventNarrative,
						Scope:         scope,
						ScopeID:       scopeID,
						OccurredAt:    occurredAt,
						NarrativeKind: TranscriptNarrativeUser,
						Text:          text,
						Final:         true,
					})
				}
			case appgateway.NarrativeRoleAssistant:
				if payloadNarrativeChunkHasContent(payload.ReasoningText, payload.Final) {
					out = append(out, TranscriptEvent{
						Kind:          TranscriptEventNarrative,
						Scope:         scope,
						ScopeID:       scopeID,
						Actor:         actor,
						OccurredAt:    occurredAt,
						NarrativeKind: TranscriptNarrativeReasoning,
						Text:          payload.ReasoningText,
						Final:         payload.Final,
					})
				}
				if payloadNarrativeChunkHasContent(payload.Text, payload.Final) {
					out = append(out, TranscriptEvent{
						Kind:          TranscriptEventNarrative,
						Scope:         scope,
						ScopeID:       scopeID,
						Actor:         actor,
						OccurredAt:    occurredAt,
						NarrativeKind: TranscriptNarrativeAssistant,
						Text:          payload.Text,
						Final:         payload.Final,
					})
				}
			case appgateway.NarrativeRoleSystem:
				if text := strings.TrimSpace(payload.Text); text != "" {
					out = append(out, TranscriptEvent{
						Kind:          TranscriptEventNotice,
						Scope:         scope,
						ScopeID:       scopeID,
						Actor:         actor,
						OccurredAt:    occurredAt,
						NarrativeKind: TranscriptNarrativeSystem,
						Text:          text,
						Final:         true,
					})
				}
			case appgateway.NarrativeRoleNotice:
				if text := strings.TrimSpace(payload.Text); text != "" {
					out = append(out, TranscriptEvent{
						Kind:          TranscriptEventNotice,
						Scope:         scope,
						ScopeID:       scopeID,
						Actor:         actor,
						OccurredAt:    occurredAt,
						NarrativeKind: TranscriptNarrativeNotice,
						Text:          text,
						Final:         true,
					})
				}
			}
		}
	case appgateway.EventKindToolCall:
		if payload := ev.ToolCall; payload != nil {
			if strings.EqualFold(strings.TrimSpace(payload.ToolName), "PLAN") {
				break
			}
			status := strings.TrimSpace(string(payload.Status))
			if status == "" || payload.Status == appgateway.ToolStatusStarted {
				status = string(appgateway.ToolStatusRunning)
			}
			out = append(out, TranscriptEvent{
				Kind:       TranscriptEventTool,
				Scope:      scope,
				ScopeID:    scopeID,
				Actor:      strings.TrimSpace(payload.Actor),
				OccurredAt: occurredAt,
				ToolCallID: strings.TrimSpace(payload.CallID),
				ToolName:   strings.TrimSpace(payload.ToolName),
				ToolArgs:   toolDisplayArgs(payload.ToolName, payload.RawInput, acpprojector.FormatToolStart(payload.ToolName, gatewayToolArgsMap(payload.CommandPreview, payload.ArgsText))),
				ToolStatus: status,
				ToolTaskID: toolDisplayTaskID(payload.RawInput, nil),
			})
		}
	case appgateway.EventKindToolResult:
		if payload := ev.ToolResult; payload != nil {
			if strings.EqualFold(strings.TrimSpace(payload.ToolName), "PLAN") {
				break
			}
			status := strings.TrimSpace(string(payload.Status))
			if status == "" {
				if payload.Error {
					status = string(appgateway.ToolStatusFailed)
				} else {
					status = string(appgateway.ToolStatusCompleted)
				}
			}
			toolErr := payload.Error || strings.EqualFold(status, string(appgateway.ToolStatusFailed))
			toolOutput := toolDisplayOutput(payload.ToolName, payload.RawInput, payload.RawOutput, acpprojector.FormatToolResult(payload.ToolName, gatewayToolArgsMap(payload.CommandPreview, ""), gatewayToolResultMap(payload.OutputText, toolErr), status), status, toolErr)
			toolArgs := toolDisplayArgs(payload.ToolName, payload.RawInput, acpprojector.FormatToolStart(payload.ToolName, gatewayToolArgsMap(payload.CommandPreview, "")))
			if !toolErr && (len(payload.RawInput) > 0 || len(payload.RawOutput) > 0) {
				if header := toolDisplayResultHeader(payload.ToolName, toolOutput); header != "" {
					toolArgs = header
				}
			}
			toolOutput = toolDisplayPanelOutput(payload.ToolName, toolOutput)
			out = append(out, TranscriptEvent{
				Kind:       TranscriptEventTool,
				Scope:      scope,
				ScopeID:    scopeID,
				Actor:      strings.TrimSpace(payload.Actor),
				OccurredAt: occurredAt,
				ToolCallID: strings.TrimSpace(payload.CallID),
				ToolName:   strings.TrimSpace(payload.ToolName),
				ToolArgs:   toolArgs,
				ToolOutput: toolOutput,
				ToolStream: transcriptToolStream(status, toolErr),
				ToolStatus: status,
				ToolError:  toolErr,
				ToolTaskID: toolDisplayTaskID(payload.RawInput, payload.RawOutput),
				Final:      transcriptToolStatusFinal(status, toolErr),
			})
		}
	case appgateway.EventKindPlanUpdate:
		if payload := ev.Plan; payload != nil {
			entries := make([]PlanEntry, 0, len(payload.Entries))
			for _, entry := range payload.Entries {
				entries = append(entries, PlanEntry{Content: entry.Content, Status: entry.Status})
			}
			if len(entries) > 0 {
				out = append(out, TranscriptEvent{
					Kind:        TranscriptEventPlan,
					Scope:       scope,
					ScopeID:     scopeID,
					OccurredAt:  occurredAt,
					PlanEntries: entries,
				})
			}
		}
	case appgateway.EventKindApprovalRequested:
		// Approval requests are transient composer overlays driven by
		// PromptRequestMsg. They intentionally do not persist into transcript.
	case appgateway.EventKindParticipant:
		if payload := ev.Participant; payload != nil {
			state := strings.TrimSpace(string(payload.Action))
			if state != "" {
				out = append(out, TranscriptEvent{
					Kind:       TranscriptEventParticipant,
					Scope:      scope,
					ScopeID:    scopeID,
					OccurredAt: occurredAt,
					State:      state,
				})
			}
		}
	case appgateway.EventKindLifecycle:
		if payload := ev.Lifecycle; payload != nil {
			state := strings.ToLower(strings.TrimSpace(string(payload.Status)))
			if state != "" {
				out = append(out, TranscriptEvent{
					Kind:       TranscriptEventLifecycle,
					Scope:      scope,
					ScopeID:    scopeID,
					Actor:      strings.TrimSpace(payload.Actor),
					OccurredAt: occurredAt,
					State:      state,
				})
			}
		}
	case appgateway.EventKindNotice, appgateway.EventKindSystemMessage:
		if text := strings.TrimSpace(gatewayNoticeText(ev)); text != "" {
			out = append(out, TranscriptEvent{
				Kind:          TranscriptEventNotice,
				Scope:         scope,
				ScopeID:       scopeID,
				OccurredAt:    occurredAt,
				NarrativeKind: TranscriptNarrativeNotice,
				Text:          text,
				Final:         true,
			})
		}
	}

	appendUsage()
	return out
}

func payloadNarrativeChunkHasContent(text string, final bool) bool {
	if text == "" {
		return false
	}
	if !final {
		return true
	}
	return strings.TrimSpace(text) != ""
}

func ProjectACPProjectionToTranscriptEvents(msg ACPProjectionMsg) []TranscriptEvent {
	scope := msg.Scope
	scopeID := strings.TrimSpace(msg.ScopeID)
	actor := strings.TrimSpace(msg.Actor)
	occurredAt := msg.OccurredAt
	out := make([]TranscriptEvent, 0, 2)

	if kind, text, final, ok := acpProjectionStreamPayload(msg); ok {
		narrativeKind := TranscriptNarrativeAssistant
		if kind == SEReasoning {
			narrativeKind = TranscriptNarrativeReasoning
		}
		out = append(out, TranscriptEvent{
			Kind:          TranscriptEventNarrative,
			Scope:         scope,
			ScopeID:       scopeID,
			Actor:         actor,
			OccurredAt:    occurredAt,
			NarrativeKind: narrativeKind,
			Text:          text,
			Final:         final,
		})
	}
	if msg.HasPlanUpdate {
		entries := append([]PlanEntry(nil), msg.PlanEntries...)
		out = append(out, TranscriptEvent{
			Kind:        TranscriptEventPlan,
			Scope:       scope,
			ScopeID:     scopeID,
			Actor:       actor,
			OccurredAt:  occurredAt,
			PlanEntries: entries,
		})
	}
	if scope == ACPProjectionSubagent {
		if stream, chunk, final, ok := acpProjectionSubagentToolPayload(msg); ok {
			out = append(out, TranscriptEvent{
				Kind:       TranscriptEventTool,
				Scope:      scope,
				ScopeID:    scopeID,
				Actor:      actor,
				OccurredAt: occurredAt,
				ToolCallID: strings.TrimSpace(msg.ToolCallID),
				ToolName:   strings.TrimSpace(msg.ToolName),
				ToolArgs:   toolDisplayArgs(msg.ToolName, msg.ToolArgs, acpprojector.FormatToolStart(msg.ToolName, msg.ToolArgs)),
				ToolOutput: tuikit.SanitizeLogText(chunk),
				ToolStream: stream,
				ToolStatus: strings.ToLower(strings.TrimSpace(msg.ToolStatus)),
				ToolError:  stream == "stderr",
				ToolTaskID: toolDisplayTaskID(msg.ToolArgs, msg.ToolResult),
				Final:      final,
			})
		}
		return out
	}
	if args, output, final, err, ok := acpProjectionToolPayload(msg); ok {
		out = append(out, TranscriptEvent{
			Kind:       TranscriptEventTool,
			Scope:      scope,
			ScopeID:    scopeID,
			Actor:      actor,
			OccurredAt: occurredAt,
			ToolCallID: strings.TrimSpace(msg.ToolCallID),
			ToolName:   strings.TrimSpace(msg.ToolName),
			ToolArgs:   args,
			ToolOutput: output,
			ToolStream: transcriptToolStream(strings.ToLower(strings.TrimSpace(msg.ToolStatus)), err),
			ToolStatus: strings.ToLower(strings.TrimSpace(msg.ToolStatus)),
			ToolError:  err,
			ToolTaskID: toolDisplayTaskID(msg.ToolArgs, msg.ToolResult),
			Final:      final,
		})
	}
	return out
}

func transcriptToolStream(status string, isErr bool) string {
	if isErr || strings.EqualFold(strings.TrimSpace(status), "failed") {
		return "stderr"
	}
	return "stdout"
}

func transcriptToolStatusFinal(status string, isErr bool) bool {
	if isErr {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "interrupted", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func acpProjectionStreamPayload(msg ACPProjectionMsg) (SubagentEventKind, string, bool, bool) {
	if strings.TrimSpace(msg.Stream) == "" {
		return 0, "", false, false
	}
	hasDelta := msg.DeltaText != ""
	hasFull := msg.FullText != ""
	text := msg.DeltaText
	if !hasDelta {
		text = msg.FullText
	}
	text = tuikit.SanitizeLogText(text)
	if text == "" {
		return 0, "", false, false
	}
	kind := SEAssistant
	if normalizeStreamKind(msg.Stream) == "reasoning" {
		kind = SEReasoning
	}
	return kind, text, !hasDelta && hasFull, true
}

func acpProjectionToolPayload(msg ACPProjectionMsg) (args string, output string, final bool, err bool, ok bool) {
	callID := strings.TrimSpace(msg.ToolCallID)
	toolName := strings.TrimSpace(msg.ToolName)
	if callID == "" || toolName == "" {
		return "", "", false, false, false
	}
	if strings.EqualFold(toolName, "PLAN") {
		return "", "", false, false, false
	}
	args = toolDisplayArgs(msg.ToolName, msg.ToolArgs, acpprojector.FormatToolStart(msg.ToolName, msg.ToolArgs))
	status := strings.ToLower(strings.TrimSpace(msg.ToolStatus))
	switch status {
	case "", "pending", "in_progress", "running":
		return args, "", false, false, true
	case "completed", "failed":
		output = toolDisplayOutput(msg.ToolName, msg.ToolArgs, msg.ToolResult, acpprojector.FormatToolResult(msg.ToolName, msg.ToolArgs, msg.ToolResult, msg.ToolStatus), msg.ToolStatus, status == "failed")
		if header := toolDisplayResultHeader(msg.ToolName, output); header != "" {
			args = header
		}
		output = toolDisplayPanelOutput(msg.ToolName, output)
		return args, output, true, status == "failed", true
	default:
		return "", "", false, false, false
	}
}

func acpProjectionSubagentToolPayload(msg ACPProjectionMsg) (stream string, chunk string, final bool, ok bool) {
	callID := strings.TrimSpace(msg.ToolCallID)
	toolName := strings.TrimSpace(msg.ToolName)
	if callID == "" || toolName == "" {
		return "", "", false, false
	}
	status := strings.ToLower(strings.TrimSpace(msg.ToolStatus))
	switch status {
	case "completed":
		return "stdout", toolDisplayPanelOutput(msg.ToolName, toolDisplayOutput(msg.ToolName, msg.ToolArgs, msg.ToolResult, acpprojector.FormatToolResult(msg.ToolName, msg.ToolArgs, msg.ToolResult, msg.ToolStatus), msg.ToolStatus, false)), true, true
	case "failed":
		return "stderr", toolDisplayPanelOutput(msg.ToolName, toolDisplayOutput(msg.ToolName, msg.ToolArgs, msg.ToolResult, acpprojector.FormatToolResult(msg.ToolName, msg.ToolArgs, msg.ToolResult, msg.ToolStatus), msg.ToolStatus, true)), true, true
	case "", "pending", "in_progress", "running":
		if msg.ToolResult == nil {
			return "", "", false, true
		}
		stream = strings.TrimSpace(firstNonEmpty(asString(msg.ToolResult["stream"]), "stdout"))
		if stream == "" {
			stream = "stdout"
		}
		return stream, toolDisplayPanelOutput(msg.ToolName, toolDisplayOutput(msg.ToolName, msg.ToolArgs, msg.ToolResult, acpprojector.FormatToolResult(msg.ToolName, msg.ToolArgs, msg.ToolResult, msg.ToolStatus), msg.ToolStatus, false)), false, true
	default:
		return "", "", false, false
	}
}
