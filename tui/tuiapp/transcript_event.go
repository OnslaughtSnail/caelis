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
				if reasoning := strings.TrimSpace(payload.ReasoningText); reasoning != "" {
					out = append(out, TranscriptEvent{
						Kind:          TranscriptEventNarrative,
						Scope:         scope,
						ScopeID:       scopeID,
						Actor:         actor,
						OccurredAt:    occurredAt,
						NarrativeKind: TranscriptNarrativeReasoning,
						Text:          reasoning,
						Final:         payload.Final,
					})
				}
				if text := strings.TrimSpace(payload.Text); text != "" {
					out = append(out, TranscriptEvent{
						Kind:          TranscriptEventNarrative,
						Scope:         scope,
						ScopeID:       scopeID,
						Actor:         actor,
						OccurredAt:    occurredAt,
						NarrativeKind: TranscriptNarrativeAssistant,
						Text:          text,
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
				ToolArgs:   acpprojector.FormatToolStart(payload.ToolName, gatewayToolArgsMap(payload.CommandPreview, payload.ArgsText)),
				ToolStatus: status,
			})
		}
	case appgateway.EventKindToolResult:
		if payload := ev.ToolResult; payload != nil {
			status := strings.TrimSpace(string(payload.Status))
			if status == "" {
				if payload.Error {
					status = string(appgateway.ToolStatusFailed)
				} else {
					status = string(appgateway.ToolStatusCompleted)
				}
			}
			out = append(out, TranscriptEvent{
				Kind:       TranscriptEventTool,
				Scope:      scope,
				ScopeID:    scopeID,
				Actor:      strings.TrimSpace(payload.Actor),
				OccurredAt: occurredAt,
				ToolCallID: strings.TrimSpace(payload.CallID),
				ToolName:   strings.TrimSpace(payload.ToolName),
				ToolArgs:   acpprojector.FormatToolStart(payload.ToolName, gatewayToolArgsMap(payload.CommandPreview, "")),
				ToolOutput: acpprojector.FormatToolResult(payload.ToolName, gatewayToolArgsMap(payload.CommandPreview, ""), gatewayToolResultMap(payload.OutputText, payload.Error), status),
				ToolStream: transcriptToolStream(status, payload.Error),
				ToolStatus: status,
				ToolError:  payload.Error || strings.EqualFold(status, string(appgateway.ToolStatusFailed)),
				Final:      true,
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
		toolName, command := gatewayApprovalSummary(ev)
		out = append(out, TranscriptEvent{
			Kind:            TranscriptEventApproval,
			Scope:           scope,
			ScopeID:         scopeID,
			OccurredAt:      occurredAt,
			ApprovalTool:    toolName,
			ApprovalCommand: command,
			ApprovalStatus:  string(appgateway.ApprovalStatusPending),
			State:           string(appgateway.LifecycleStatusWaitingApproval),
		})
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
	case appgateway.EventKindLifecycle, appgateway.EventKindSessionLifecycle:
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
				ToolArgs:   acpprojector.FormatToolStart(msg.ToolName, msg.ToolArgs),
				ToolOutput: tuikit.SanitizeLogText(chunk),
				ToolStream: stream,
				ToolStatus: strings.ToLower(strings.TrimSpace(msg.ToolStatus)),
				ToolError:  stream == "stderr",
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
	args = acpprojector.FormatToolStart(msg.ToolName, msg.ToolArgs)
	status := strings.ToLower(strings.TrimSpace(msg.ToolStatus))
	switch status {
	case "", "pending", "in_progress", "running":
		return args, "", false, false, true
	case "completed", "failed":
		output = acpprojector.FormatToolResult(msg.ToolName, msg.ToolArgs, msg.ToolResult, msg.ToolStatus)
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
		return "stdout", acpprojector.FormatToolResult(msg.ToolName, msg.ToolArgs, msg.ToolResult, msg.ToolStatus), true, true
	case "failed":
		return "stderr", acpprojector.FormatToolResult(msg.ToolName, msg.ToolArgs, msg.ToolResult, msg.ToolStatus), true, true
	case "", "pending", "in_progress", "running":
		if msg.ToolResult == nil {
			return "", "", false, true
		}
		stream = strings.TrimSpace(firstNonEmpty(asString(msg.ToolResult["stream"]), "stdout"))
		if stream == "" {
			stream = "stdout"
		}
		return stream, acpprojector.FormatToolResult(msg.ToolName, msg.ToolArgs, msg.ToolResult, msg.ToolStatus), false, true
	default:
		return "", "", false, false
	}
}
