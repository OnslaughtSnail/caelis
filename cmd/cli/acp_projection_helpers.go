package main

import (
	"strings"
	"time"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/acpprojector"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

func acpPlanEntriesToTUI(entries []internalacp.PlanEntry) []tuievents.PlanEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]tuievents.PlanEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, tuievents.PlanEntry{
			Content: strings.TrimSpace(entry.Content),
			Status:  strings.TrimSpace(entry.Status),
		})
	}
	return out
}

// projectionToACPMsg converts an acpprojector.Projection into a
// tuievents.ACPProjectionMsg, applying SanitizeLogText to text fields. This is
// the single conversion point used by all ACP paths (main, participant,
// subagent) that receive live projections from a LiveProjector.
func projectionToACPMsg(item acpprojector.Projection, scope tuievents.ACPProjectionScope, scopeID string, actor string) tuievents.ACPProjectionMsg {
	if sid := strings.TrimSpace(item.SessionID); sid != "" {
		scopeID = sid
	}
	return tuievents.ACPProjectionMsg{
		Scope:         scope,
		ScopeID:       strings.TrimSpace(scopeID),
		Actor:         strings.TrimSpace(actor),
		OccurredAt:    time.Now(),
		Stream:        item.Stream,
		DeltaText:     tuikit.SanitizeLogText(item.DeltaText),
		FullText:      tuikit.SanitizeLogText(item.FullText),
		ToolCallID:    strings.TrimSpace(item.ToolCallID),
		ToolName:      strings.TrimSpace(item.ToolName),
		ToolArgs:      cloneAnyMap(item.ToolArgs),
		ToolResult:    cloneAnyMap(item.ToolResult),
		ToolStatus:    strings.TrimSpace(item.ToolStatus),
		PlanEntries:   acpPlanEntriesToTUI(item.PlanEntries),
		HasPlanUpdate: item.PlanEntries != nil,
	}
}

// replayProjectionMsgFromEvent reconstructs an ACPProjectionMsg from a
// persisted projection event. Used by all three scope-specific replay methods.
func replayProjectionMsgFromEvent(ev acpProjectionPersistedEvent, scope tuievents.ACPProjectionScope) tuievents.ACPProjectionMsg {
	return tuievents.ACPProjectionMsg{
		Scope:         scope,
		ScopeID:       chooseNonEmptyString(strings.TrimSpace(ev.SessionID), strings.TrimSpace(ev.ScopeID)),
		Actor:         strings.TrimSpace(ev.Actor),
		OccurredAt:    parsePersistedEventTime(ev.Time),
		Stream:        strings.TrimSpace(ev.Stream),
		DeltaText:     ev.DeltaText,
		FullText:      ev.FullText,
		ToolCallID:    strings.TrimSpace(ev.ToolCallID),
		ToolName:      strings.TrimSpace(ev.ToolName),
		ToolArgs:      cloneAnyMap(ev.ToolArgs),
		ToolResult:    cloneAnyMap(ev.ToolResult),
		ToolStatus:    strings.TrimSpace(ev.ToolStatus),
		PlanEntries:   append([]tuievents.PlanEntry(nil), ev.PlanEntries...),
		HasPlanUpdate: ev.HasPlanUpdate,
	}
}

// acpMsgToPersistedProjection converts an ACPProjectionMsg into a persisted
// event suitable for JSONL storage. The msg.Scope and msg.ScopeID are used
// directly, making this a scope-agnostic conversion.
func acpMsgToPersistedProjection(msg tuievents.ACPProjectionMsg) acpProjectionPersistedEvent {
	ev := acpProjectionPersistedEvent{
		Scope:         string(msg.Scope),
		ScopeID:       strings.TrimSpace(msg.ScopeID),
		Kind:          "projection",
		SessionID:     strings.TrimSpace(msg.ScopeID),
		Actor:         strings.TrimSpace(msg.Actor),
		Stream:        strings.TrimSpace(msg.Stream),
		DeltaText:     msg.DeltaText,
		FullText:      msg.FullText,
		ToolCallID:    strings.TrimSpace(msg.ToolCallID),
		ToolName:      strings.TrimSpace(msg.ToolName),
		ToolArgs:      cloneAnyMap(msg.ToolArgs),
		ToolResult:    cloneAnyMap(msg.ToolResult),
		ToolStatus:    strings.TrimSpace(msg.ToolStatus),
		PlanEntries:   append([]tuievents.PlanEntry(nil), msg.PlanEntries...),
		HasPlanUpdate: msg.HasPlanUpdate,
	}
	if !msg.OccurredAt.IsZero() {
		ev.Time = msg.OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	return ev
}
