package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/acpprojector"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const acpProjectionLogFileName = "acp_projection.jsonl"

type ACPProjectionStore struct {
	console *cliConsole
}

type ACPProjectionIndex struct {
	Events    []acpProjectionPersistedEvent
	ByCallID  map[string][]acpProjectionPersistedEvent
	ByScopeID map[tuievents.ACPProjectionScope]map[string][]acpProjectionPersistedEvent
}

type acpProjectionPersistedEvent struct {
	Version         int                   `json:"version"`
	Time            string                `json:"time,omitempty"`
	Scope           string                `json:"scope,omitempty"`
	ScopeID         string                `json:"scope_id,omitempty"`
	CallID          string                `json:"call_id,omitempty"`
	RouteKind       string                `json:"route_kind,omitempty"`
	Kind            string                `json:"kind"`
	SessionID       string                `json:"session_id,omitempty"`
	Actor           string                `json:"actor,omitempty"`
	Agent           string                `json:"agent,omitempty"`
	AttachTarget    string                `json:"attach_target,omitempty"`
	AnchorTool      string                `json:"anchor_tool,omitempty"`
	ClaimAnchor     bool                  `json:"claim_anchor,omitempty"`
	Provisional     bool                  `json:"provisional,omitempty"`
	Stream          string                `json:"stream,omitempty"`
	DeltaText       string                `json:"delta_text,omitempty"`
	FullText        string                `json:"full_text,omitempty"`
	ToolCallID      string                `json:"tool_call_id,omitempty"`
	ToolName        string                `json:"tool_name,omitempty"`
	ToolArgs        map[string]any        `json:"tool_args,omitempty"`
	ToolResult      map[string]any        `json:"tool_result,omitempty"`
	ToolStatus      string                `json:"tool_status,omitempty"`
	PlanEntries     []tuievents.PlanEntry `json:"plan_entries,omitempty"`
	HasPlanUpdate   bool                  `json:"has_plan_update,omitempty"`
	Status          string                `json:"status,omitempty"`
	ApprovalTool    string                `json:"approval_tool,omitempty"`
	ApprovalCommand string                `json:"approval_command,omitempty"`
}

type acpProjectionPersistedLine struct {
	Type  string                       `json:"type"`
	Event *acpProjectionPersistedEvent `json:"event,omitempty"`
}

func (c *cliConsole) acpProjectionStore() ACPProjectionStore {
	return ACPProjectionStore{console: c}
}

func (s ACPProjectionStore) logPath() (string, error) {
	c := s.console
	if c == nil || c.sessionStore == nil {
		return "", session.ErrSessionNotFound
	}
	withDir, ok := c.sessionStore.(sessionDirStore)
	if ok {
		dir, err := withDir.SessionDir(c.currentSessionRef())
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(dir) != "" {
			return filepath.Join(dir, acpProjectionLogFileName), nil
		}
	}
	if strings.TrimSpace(c.appName) == "" || strings.TrimSpace(c.userID) == "" || strings.TrimSpace(c.sessionID) == "" {
		return "", fmt.Errorf("session dir is unavailable")
	}
	dir := filepath.Join(os.TempDir(), "caelis-acp-projection", fmt.Sprintf("%p", c.sessionStore), c.appName, c.userID, c.sessionID)
	return filepath.Join(dir, acpProjectionLogFileName), nil
}

func (s ACPProjectionStore) AppendEvent(_ context.Context, ev acpProjectionPersistedEvent) error {
	c := s.console
	if c == nil {
		return session.ErrSessionNotFound
	}
	path, err := s.logPath()
	if err != nil {
		return err
	}
	ev.Version = 1
	if strings.TrimSpace(ev.Time) == "" {
		ev.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	line, err := json.Marshal(acpProjectionPersistedLine{
		Type:  "event",
		Event: &ev,
	})
	if err != nil {
		return err
	}
	c.outMu.Lock()
	defer c.outMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func (s ACPProjectionStore) LoadEvents(_ context.Context) ([]acpProjectionPersistedEvent, error) {
	c := s.console
	if c == nil {
		return nil, nil
	}
	path, err := s.logPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	out := make([]acpProjectionPersistedEvent, 0, 64)
	for scanner.Scan() {
		var line acpProjectionPersistedLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			return nil, err
		}
		if line.Type != "event" || line.Event == nil {
			continue
		}
		out = append(out, *line.Event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s ACPProjectionStore) LoadIndex(ctx context.Context) (*ACPProjectionIndex, error) {
	events, err := s.LoadEvents(ctx)
	if err != nil {
		return nil, err
	}
	index := &ACPProjectionIndex{
		Events:    events,
		ByCallID:  map[string][]acpProjectionPersistedEvent{},
		ByScopeID: map[tuievents.ACPProjectionScope]map[string][]acpProjectionPersistedEvent{},
	}
	for _, ev := range events {
		callID := strings.TrimSpace(ev.CallID)
		if callID != "" {
			index.ByCallID[callID] = append(index.ByCallID[callID], ev)
		}
		scope := tuievents.ACPProjectionScope(strings.TrimSpace(ev.Scope))
		scopeID := strings.TrimSpace(ev.ScopeID)
		if scope == "" || scopeID == "" {
			continue
		}
		scopeIndex := index.ByScopeID[scope]
		if scopeIndex == nil {
			scopeIndex = map[string][]acpProjectionPersistedEvent{}
			index.ByScopeID[scope] = scopeIndex
		}
		scopeIndex[scopeID] = append(scopeIndex[scopeID], ev)
	}
	return index, nil
}

func (s ACPProjectionStore) IndexByCallID(ctx context.Context) map[string][]acpProjectionPersistedEvent {
	index, err := s.LoadIndex(ctx)
	if err != nil || index == nil || len(index.ByCallID) == 0 {
		return nil
	}
	return index.ByCallID
}

func (s ACPProjectionStore) IndexByScopeID(ctx context.Context, scope tuievents.ACPProjectionScope) map[string][]acpProjectionPersistedEvent {
	index, err := s.LoadIndex(ctx)
	if err != nil || index == nil {
		return nil
	}
	return index.ByScopeID[scope]
}

func (c *cliConsole) loadACPProjectionIndex(ctx context.Context) *ACPProjectionIndex {
	if c == nil {
		return nil
	}
	index, err := c.acpProjectionStore().LoadIndex(ctx)
	if err != nil {
		return nil
	}
	return index
}

func chooseNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parsePersistedEventTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return ts
}

func projectionNarrativeSnapshotFromEvents(events []acpProjectionPersistedEvent) (assistant string, reasoning string) {
	for _, ev := range events {
		if !strings.EqualFold(strings.TrimSpace(ev.Kind), "projection") {
			continue
		}
		stream := strings.ToLower(strings.TrimSpace(ev.Stream))
		switch stream {
		case "assistant", "answer":
			switch {
			case strings.TrimSpace(ev.FullText) != "":
				assistant = ev.FullText
			case strings.TrimSpace(ev.DeltaText) != "":
				next, _, _ := acpprojector.MergeNarrativeChunk(assistant, ev.DeltaText)
				assistant = next
			}
		case "reasoning":
			switch {
			case strings.TrimSpace(ev.FullText) != "":
				reasoning = ev.FullText
			case strings.TrimSpace(ev.DeltaText) != "":
				next, _, _ := acpprojector.MergeNarrativeChunk(reasoning, ev.DeltaText)
				reasoning = next
			}
		}
	}
	return strings.TrimSpace(assistant), strings.TrimSpace(reasoning)
}

func splitACPProjectionTurns(events []acpProjectionPersistedEvent) [][]acpProjectionPersistedEvent {
	if len(events) == 0 {
		return nil
	}
	turns := make([][]acpProjectionPersistedEvent, 0, 8)
	current := make([]acpProjectionPersistedEvent, 0, 8)
	flush := func() {
		if len(current) == 0 {
			return
		}
		turn := append([]acpProjectionPersistedEvent(nil), current...)
		turns = append(turns, turn)
		current = current[:0]
	}
	for _, ev := range events {
		if strings.EqualFold(strings.TrimSpace(ev.Kind), "turn_start") {
			flush()
		}
		current = append(current, ev)
	}
	flush()
	return turns
}

func isReplayContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	replay, _ := ctx.Value(replayContextMarker).(bool)
	return replay
}

func participantProjectionBaseEvent(turn *externalAgentTurn) acpProjectionPersistedEvent {
	if turn == nil {
		return acpProjectionPersistedEvent{}
	}
	childSessionID := strings.TrimSpace(turn.participant.ChildSessionID)
	return acpProjectionPersistedEvent{
		Scope:     string(tuievents.ACPProjectionParticipant),
		ScopeID:   childSessionID,
		CallID:    strings.TrimSpace(turn.callID),
		RouteKind: strings.TrimSpace(turn.routeKind),
		SessionID: childSessionID,
		Actor:     strings.TrimSpace(turn.participant.DisplayLabel),
	}
}

func (s ACPProjectionStore) AppendParticipantTurnStart(ctx context.Context, turn *externalAgentTurn) error {
	ev := participantProjectionBaseEvent(turn)
	if strings.TrimSpace(ev.CallID) == "" || strings.TrimSpace(ev.ScopeID) == "" {
		return nil
	}
	ev.Kind = "turn_start"
	return s.AppendEvent(ctx, ev)
}

func (s ACPProjectionStore) AppendParticipantProjection(ctx context.Context, turn *externalAgentTurn, item acpprojector.Projection) error {
	ev := participantProjectionBaseEvent(turn)
	if strings.TrimSpace(ev.CallID) == "" {
		return nil
	}
	ev.Kind = "projection"
	ev.SessionID = chooseNonEmptyString(strings.TrimSpace(item.SessionID), ev.SessionID)
	ev.Stream = strings.TrimSpace(item.Stream)
	ev.DeltaText = item.DeltaText
	ev.FullText = item.FullText
	ev.ToolCallID = strings.TrimSpace(item.ToolCallID)
	ev.ToolName = strings.TrimSpace(item.ToolName)
	ev.ToolArgs = cloneAnyMap(item.ToolArgs)
	ev.ToolResult = cloneAnyMap(item.ToolResult)
	ev.ToolStatus = strings.TrimSpace(item.ToolStatus)
	ev.PlanEntries = acpPlanEntriesToTUI(item.PlanEntries)
	ev.HasPlanUpdate = item.PlanEntries != nil
	return s.AppendEvent(ctx, ev)
}

func (s ACPProjectionStore) AppendParticipantStreamSnapshot(ctx context.Context, turn *externalAgentTurn, stream string, fullText string) error {
	ev := participantProjectionBaseEvent(turn)
	if strings.TrimSpace(ev.CallID) == "" {
		return nil
	}
	ev.Kind = "projection"
	ev.Stream = strings.TrimSpace(stream)
	ev.FullText = fullText
	return s.AppendEvent(ctx, ev)
}

func (s ACPProjectionStore) AppendParticipantStatus(ctx context.Context, turn *externalAgentTurn, status string) error {
	ev := participantProjectionBaseEvent(turn)
	if strings.TrimSpace(ev.CallID) == "" {
		return nil
	}
	ev.Kind = "status"
	ev.Status = strings.TrimSpace(status)
	return s.AppendEvent(ctx, ev)
}

func (s ACPProjectionStore) AppendParticipantStatusByIDs(ctx context.Context, callID string, sessionID string, status string) error {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil
	}
	return s.AppendEvent(ctx, acpProjectionPersistedEvent{
		Scope:     string(tuievents.ACPProjectionParticipant),
		ScopeID:   strings.TrimSpace(sessionID),
		CallID:    callID,
		Kind:      "status",
		SessionID: strings.TrimSpace(sessionID),
		Status:    strings.TrimSpace(status),
	})
}

func (s ACPProjectionStore) AppendMainTurnStart(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	return s.AppendEvent(ctx, acpProjectionPersistedEvent{
		Scope:     string(tuievents.ACPProjectionMain),
		ScopeID:   sessionID,
		Kind:      "turn_start",
		SessionID: sessionID,
	})
}

func (s ACPProjectionStore) AppendMainProjection(ctx context.Context, msg tuievents.ACPProjectionMsg) error {
	sessionID := strings.TrimSpace(msg.ScopeID)
	if sessionID == "" {
		return nil
	}
	msg.Scope = tuievents.ACPProjectionMain
	msg.ScopeID = sessionID
	return s.AppendEvent(ctx, acpMsgToPersistedProjection(msg))
}

func subagentProjectionEvent(msg any) (acpProjectionPersistedEvent, bool) {
	switch typed := msg.(type) {
	case tuievents.SubagentStartMsg:
		return acpProjectionPersistedEvent{
			Scope:        string(tuievents.ACPProjectionSubagent),
			ScopeID:      strings.TrimSpace(typed.SpawnID),
			CallID:       strings.TrimSpace(typed.CallID),
			Kind:         "turn_start",
			SessionID:    strings.TrimSpace(typed.SpawnID),
			Agent:        strings.TrimSpace(typed.Agent),
			AttachTarget: strings.TrimSpace(typed.AttachTarget),
			AnchorTool:   strings.TrimSpace(typed.AnchorTool),
			ClaimAnchor:  typed.ClaimAnchor,
			Provisional:  typed.Provisional,
		}, true
	case tuievents.SubagentStatusMsg:
		return acpProjectionPersistedEvent{
			Scope:           string(tuievents.ACPProjectionSubagent),
			ScopeID:         strings.TrimSpace(typed.SpawnID),
			Kind:            "status",
			SessionID:       strings.TrimSpace(typed.SpawnID),
			Status:          strings.TrimSpace(typed.State),
			ApprovalTool:    strings.TrimSpace(typed.ApprovalTool),
			ApprovalCommand: strings.TrimSpace(typed.ApprovalCommand),
		}, true
	case tuievents.SubagentDoneMsg:
		return acpProjectionPersistedEvent{
			Scope:     string(tuievents.ACPProjectionSubagent),
			ScopeID:   strings.TrimSpace(typed.SpawnID),
			Kind:      "status",
			SessionID: strings.TrimSpace(typed.SpawnID),
			Status:    strings.TrimSpace(typed.State),
		}, true
	case tuievents.ACPProjectionMsg:
		if typed.Scope != tuievents.ACPProjectionSubagent {
			return acpProjectionPersistedEvent{}, false
		}
		return acpMsgToPersistedProjection(typed), true
	default:
		return acpProjectionPersistedEvent{}, false
	}
}

func (s ACPProjectionStore) PersistSubagentProjectionMsg(ctx context.Context, rootSessionID string, msg any) {
	c := s.console
	if c == nil || isReplayContext(ctx) {
		return
	}
	if rootSessionID != "" && strings.TrimSpace(c.sessionID) != strings.TrimSpace(rootSessionID) {
		return
	}
	ev, ok := subagentProjectionEvent(msg)
	if !ok {
		return
	}
	_ = s.AppendEvent(ctx, ev)
}

func participantReplayMessage(ev acpProjectionPersistedEvent) (any, bool) {
	sessionID := strings.TrimSpace(ev.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(ev.ScopeID)
	}
	actor := strings.TrimSpace(ev.Actor)
	if actor == "" {
		actor = sessionID
	}
	switch strings.TrimSpace(ev.Kind) {
	case "turn_start":
		if sessionID == "" {
			return nil, false
		}
		return tuievents.ParticipantTurnStartMsg{
			SessionID:  sessionID,
			Actor:      actor,
			OccurredAt: parsePersistedEventTime(ev.Time),
		}, true
	case "projection":
		msg := replayProjectionMsgFromEvent(ev, tuievents.ACPProjectionParticipant)
		if actor == "" {
			actor = sessionID
		}
		msg.Actor = actor
		return msg, true
	case "status":
		return tuievents.ParticipantStatusMsg{
			SessionID:       chooseNonEmptyString(sessionID, strings.TrimSpace(ev.ScopeID)),
			State:           strings.TrimSpace(ev.Status),
			ApprovalTool:    strings.TrimSpace(ev.ApprovalTool),
			ApprovalCommand: strings.TrimSpace(ev.ApprovalCommand),
			OccurredAt:      parsePersistedEventTime(ev.Time),
		}, true
	default:
		return nil, false
	}
}

func mainReplayMessage(ev acpProjectionPersistedEvent) (any, bool) {
	sessionID := chooseNonEmptyString(strings.TrimSpace(ev.SessionID), strings.TrimSpace(ev.ScopeID))
	switch strings.TrimSpace(ev.Kind) {
	case "turn_start":
		if sessionID == "" {
			return nil, false
		}
		return tuievents.ACPMainTurnStartMsg{
			SessionID:  sessionID,
			OccurredAt: parsePersistedEventTime(ev.Time),
		}, true
	case "projection":
		return replayProjectionMsgFromEvent(ev, tuievents.ACPProjectionMain), true
	default:
		return nil, false
	}
}

func subagentReplayMessage(ev acpProjectionPersistedEvent) (any, bool) {
	switch strings.TrimSpace(ev.Kind) {
	case "turn_start":
		return tuievents.SubagentStartMsg{
			SpawnID:      strings.TrimSpace(ev.ScopeID),
			AttachTarget: strings.TrimSpace(ev.AttachTarget),
			Agent:        strings.TrimSpace(ev.Agent),
			CallID:       strings.TrimSpace(ev.CallID),
			AnchorTool:   strings.TrimSpace(ev.AnchorTool),
			ClaimAnchor:  ev.ClaimAnchor,
			Provisional:  ev.Provisional,
			OccurredAt:   parsePersistedEventTime(ev.Time),
		}, true
	case "projection":
		return replayProjectionMsgFromEvent(ev, tuievents.ACPProjectionSubagent), true
	case "status":
		state := strings.ToLower(strings.TrimSpace(ev.Status))
		if state == "completed" || state == "failed" || state == "interrupted" || state == "timed_out" {
			return tuievents.SubagentDoneMsg{
				SpawnID:    strings.TrimSpace(ev.ScopeID),
				State:      state,
				OccurredAt: parsePersistedEventTime(ev.Time),
			}, true
		}
		return tuievents.SubagentStatusMsg{
			SpawnID:         strings.TrimSpace(ev.ScopeID),
			State:           state,
			ApprovalTool:    strings.TrimSpace(ev.ApprovalTool),
			ApprovalCommand: strings.TrimSpace(ev.ApprovalCommand),
			OccurredAt:      parsePersistedEventTime(ev.Time),
		}, true
	default:
		return nil, false
	}
}

func (s ACPProjectionStore) ReplaySubagentEvents(ctx context.Context, rootSessionID string, events []acpProjectionPersistedEvent) bool {
	c := s.console
	if c == nil || c.tuiSender == nil || len(events) == 0 {
		return false
	}
	replayed := false
	replayCtx := replaySubagentContext(ctx)
	for _, ev := range events {
		msg, ok := subagentReplayMessage(ev)
		if !ok {
			continue
		}
		c.sendSubagentProjectionMsg(replayCtx, rootSessionID, msg)
		replayed = true
	}
	return replayed
}

func (s ACPProjectionStore) LatestScopeNarrativeSnapshot(ctx context.Context, scope tuievents.ACPProjectionScope, scopeID string) (assistant string, reasoning string) {
	scopeID = strings.TrimSpace(scopeID)
	if scopeID == "" {
		return "", ""
	}
	index, err := s.LoadIndex(ctx)
	if err != nil || index == nil {
		return "", ""
	}
	scopeIndex := index.ByScopeID[scope]
	if len(scopeIndex) == 0 {
		return "", ""
	}
	events := scopeIndex[scopeID]
	if len(events) == 0 {
		return "", ""
	}
	turns := splitACPProjectionTurns(events)
	for i := len(turns) - 1; i >= 0; i-- {
		assistant, reasoning = projectionNarrativeSnapshotFromEvents(turns[i])
		if assistant != "" || reasoning != "" {
			return assistant, reasoning
		}
	}
	return projectionNarrativeSnapshotFromEvents(events)
}

func replaySubagentContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, replayContextMarker, true)
}

func (s ACPProjectionStore) ReplayParticipantEvents(events []acpProjectionPersistedEvent) bool {
	c := s.console
	if c == nil || c.tuiSender == nil || len(events) == 0 {
		return false
	}
	replayed := false
	for _, ev := range events {
		msg, ok := participantReplayMessage(ev)
		if !ok {
			continue
		}
		c.tuiSender.Send(msg)
		replayed = true
	}
	return replayed
}

func (s ACPProjectionStore) ReplayMainEvents(events []acpProjectionPersistedEvent) bool {
	c := s.console
	if c == nil || c.tuiSender == nil || len(events) == 0 {
		return false
	}
	replayed := false
	for _, ev := range events {
		msg, ok := mainReplayMessage(ev)
		if !ok {
			continue
		}
		c.tuiSender.Send(msg)
		replayed = true
	}
	return replayed
}
