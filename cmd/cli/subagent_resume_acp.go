package main

import (
	"context"
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type resumedSubagentTarget struct {
	SpawnID      string
	SessionID    string
	AttachTarget string
	CallID       string
	AnchorTool   string
	Agent        string
	ChildCWD     string
}

func (c *cliConsole) restoreResumedSubagentPanels(ctx context.Context, rootSessionID string, events []*session.Event) {
	if c == nil || c.tuiSender == nil || len(events) == 0 {
		return
	}
	var projectionIndex map[string][]acpProjectionPersistedEvent
	if index, err := c.acpProjectionStore().LoadIndex(ctx); err == nil && index != nil {
		projectionIndex = index.ByScopeID[tuievents.ACPProjectionSubagent]
	}
	runningTargets := map[string]struct{}{}
	for _, target := range collectResumedSubagentTargets(events, false) {
		if spawnID := strings.TrimSpace(target.SpawnID); spawnID != "" {
			runningTargets[spawnID] = struct{}{}
		}
	}
	for _, target := range collectResumedSubagentTargets(events, true) {
		eventsForTarget := projectionIndex[strings.TrimSpace(target.SpawnID)]
		if len(eventsForTarget) > 0 {
			_ = c.acpProjectionStore().ReplaySubagentEvents(ctx, rootSessionID, eventsForTarget)
			continue
		}
		if _, ok := runningTargets[strings.TrimSpace(target.SpawnID)]; !ok {
			continue
		}
		c.dispatchSubagentDomainUpdate(ctx, subagentDomainUpdate{
			Kind:        subagentDomainBootstrap,
			ClaimAnchor: true,
			Target: subagentProjectionTarget{
				RootSessionID: rootSessionID,
				SpawnID:       target.SpawnID,
				AttachTarget:  target.AttachTarget,
				CallID:        target.CallID,
				AnchorTool:    target.AnchorTool,
				Agent:         target.Agent,
			},
		})
	}
}

func collectResumedSubagentTargets(events []*session.Event, includeCompleted bool) []resumedSubagentTarget {
	if len(events) == 0 {
		return nil
	}
	liveStates := resumedSubagentLiveStateIndex(events)
	orderedSessions := make([]string, 0)
	latestBySession := map[string]resumedSubagentTarget{}
	for _, ev := range events {
		if ev == nil {
			continue
		}
		resp := ev.Message.ToolResponse()
		target, ok := resumedSubagentTargetFromToolResponse(resp, liveStates, includeCompleted)
		if !ok {
			continue
		}
		sessionID := strings.TrimSpace(target.SessionID)
		if _, exists := latestBySession[sessionID]; !exists {
			orderedSessions = append(orderedSessions, sessionID)
		}
		latestBySession[sessionID] = target
	}
	out := make([]resumedSubagentTarget, 0, len(orderedSessions))
	for _, sessionID := range orderedSessions {
		target, ok := latestBySession[sessionID]
		if !ok {
			continue
		}
		out = append(out, target)
	}
	return out
}

func resumedSubagentTargetFromToolResponse(resp *model.ToolResponse, liveStates map[string]string, includeCompleted bool) (resumedSubagentTarget, bool) {
	if resp == nil {
		return resumedSubagentTarget{}, false
	}
	switch {
	case strings.EqualFold(strings.TrimSpace(resp.Name), tool.SpawnToolName):
		sessionID := strings.TrimSpace(firstNonEmpty(resp.Result, "child_session_id"))
		if sessionID == "" || (!includeCompleted && !shouldResumeSubagentTarget(resp.Result, liveStates[sessionID])) {
			return resumedSubagentTarget{}, false
		}
		target := resumedSubagentTarget{
			SpawnID:      sessionID,
			SessionID:    sessionID,
			AttachTarget: strings.TrimSpace(firstNonEmpty(resp.Result, "child_session_id", "delegation_id")),
			CallID:       strings.TrimSpace(resp.ID),
			AnchorTool:   tool.SpawnToolName,
			Agent:        strings.TrimSpace(firstNonEmpty(resp.Result, "agent")),
			ChildCWD:     strings.TrimSpace(firstNonEmpty(resp.Result, "child_cwd")),
		}
		if target.Agent == "" {
			target.Agent = "self"
		}
		return target, true
	case strings.EqualFold(strings.TrimSpace(resp.Name), tool.TaskToolName):
		sessionID := strings.TrimSpace(firstNonEmpty(resp.Result, "child_session_id"))
		spawnID := strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_spawn_id"))
		callID := strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_parent_tool_call_id"))
		if sessionID == "" || spawnID == "" || callID == "" || (!includeCompleted && !shouldResumeSubagentTarget(resp.Result, liveStates[sessionID])) {
			return resumedSubagentTarget{}, false
		}
		target := resumedSubagentTarget{
			SpawnID:      spawnID,
			SessionID:    sessionID,
			AttachTarget: strings.TrimSpace(firstNonEmpty(resp.Result, "child_session_id", "delegation_id")),
			CallID:       callID,
			AnchorTool:   strings.TrimSpace(firstNonEmpty(resp.Result, "_ui_anchor_tool")),
			Agent:        strings.TrimSpace(firstNonEmpty(resp.Result, "agent")),
			ChildCWD:     strings.TrimSpace(firstNonEmpty(resp.Result, "child_cwd")),
		}
		if target.AnchorTool == "" {
			target.AnchorTool = runtime.SubagentContinuationAnchorTool
		}
		if target.Agent == "" {
			target.Agent = "self"
		}
		return target, true
	default:
		return resumedSubagentTarget{}, false
	}
}

func resumedSubagentLiveStateIndex(events []*session.Event) map[string]string {
	if len(events) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if ev.Meta != nil {
			childSessionID := strings.TrimSpace(firstNonEmpty(ev.Meta, "child_session_id"))
			if childSessionID != "" {
				if info, ok := runtime.LifecycleFromEvent(ev); ok {
					out[childSessionID] = strings.ToLower(strings.TrimSpace(string(info.Status)))
				}
			}
		}
		resp := ev.Message.ToolResponse()
		if resp == nil {
			continue
		}
		childSessionID := strings.TrimSpace(firstNonEmpty(resp.Result, "child_session_id"))
		if childSessionID == "" {
			continue
		}
		state := strings.ToLower(strings.TrimSpace(firstNonEmpty(resp.Result, "progress_state", "state")))
		if state == "" {
			continue
		}
		out[childSessionID] = state
	}
	return out
}

func shouldResumeSubagentTarget(result map[string]any, liveState string) bool {
	state := strings.ToLower(strings.TrimSpace(liveState))
	if state == "" {
		state = strings.ToLower(strings.TrimSpace(firstNonEmpty(result, "progress_state", "state")))
	}
	switch state {
	case "", "running", "waiting_approval":
		return true
	default:
		return false
	}
}

func (c *cliConsole) sendSubagentProjectionMsg(ctx context.Context, rootSessionID string, msg any) {
	if c == nil || c.tuiSender == nil {
		return
	}
	if rootSessionID != "" && strings.TrimSpace(c.sessionID) != strings.TrimSpace(rootSessionID) {
		return
	}
	switch typed := msg.(type) {
	case tuievents.SubagentStreamMsg:
		projection := tuievents.ACPProjectionMsg{
			Scope:     tuievents.ACPProjectionSubagent,
			ScopeID:   typed.SpawnID,
			Stream:    typed.Stream,
			DeltaText: typed.Chunk,
		}
		c.tuiSender.Send(projection)
		c.acpProjectionStore().PersistSubagentProjectionMsg(ctx, rootSessionID, projection)
		return
	case tuievents.SubagentToolCallMsg:
		projection := tuievents.ACPProjectionMsg{
			Scope:      tuievents.ACPProjectionSubagent,
			ScopeID:    typed.SpawnID,
			ToolCallID: typed.CallID,
			ToolName:   typed.ToolName,
		}
		if args := strings.TrimSpace(typed.Args); args != "" {
			projection.ToolArgs = map[string]any{"_display": args}
		}
		if typed.Final {
			projection.ToolStatus = "completed"
			if strings.EqualFold(strings.TrimSpace(typed.Stream), "stderr") {
				projection.ToolStatus = "failed"
			}
			if chunk := strings.TrimSpace(typed.Chunk); chunk != "" {
				projection.ToolResult = map[string]any{"summary": typed.Chunk}
			}
		} else if strings.TrimSpace(typed.Chunk) != "" {
			projection.ToolResult = map[string]any{
				"summary": typed.Chunk,
				"stream":  typed.Stream,
			}
		}
		c.tuiSender.Send(projection)
		c.acpProjectionStore().PersistSubagentProjectionMsg(ctx, rootSessionID, projection)
		return
	case tuievents.SubagentPlanMsg:
		projection := tuievents.ACPProjectionMsg{
			Scope:         tuievents.ACPProjectionSubagent,
			ScopeID:       typed.SpawnID,
			PlanEntries:   append([]tuievents.PlanEntry(nil), typed.Entries...),
			HasPlanUpdate: true,
		}
		c.tuiSender.Send(projection)
		c.acpProjectionStore().PersistSubagentProjectionMsg(ctx, rootSessionID, projection)
		return
	}
	c.tuiSender.Send(msg)
	c.acpProjectionStore().PersistSubagentProjectionMsg(ctx, rootSessionID, msg)
}
