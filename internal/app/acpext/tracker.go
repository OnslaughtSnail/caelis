package acpext

import (
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/runtime"
)

type trackerKey struct {
	agent     string
	sessionID string
}

type remoteSubagentTracker struct {
	mu      sync.RWMutex
	entries map[trackerKey]*remoteSubagentState
}

type remoteSubagentState struct {
	SessionID       string
	DelegationID    string
	Agent           string
	ChildCWD        string
	State           string
	Running         bool
	ApprovalPending bool
	ToolCallPending bool
	Assistant       string
	Reasoning       string
	LogSnapshot     string
	LatestOutput    string
	ProgressSeq     int
	LastTool        string
	toolCallCount   int
	UpdatedAt       time.Time
}

func newRemoteSubagentTracker() *remoteSubagentTracker {
	return &remoteSubagentTracker{entries: map[trackerKey]*remoteSubagentState{}}
}

func (t *remoteSubagentTracker) key(agentName, sessionID string) trackerKey {
	return trackerKey{
		agent:     strings.TrimSpace(agentName),
		sessionID: strings.TrimSpace(sessionID),
	}
}

func (t *remoteSubagentTracker) ensure(agentName, sessionID string) *remoteSubagentState {
	if t == nil {
		return nil
	}
	key := t.key(agentName, sessionID)
	if key.sessionID == "" {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if existing := t.entries[key]; existing != nil {
		return existing
	}
	state := &remoteSubagentState{
		SessionID: key.sessionID,
		Agent:     key.agent,
		UpdatedAt: time.Now(),
	}
	t.entries[key] = state
	return state
}

func (t *remoteSubagentTracker) markRunning(agentName, sessionID, delegationID, childCWD string) {
	state := t.ensure(agentName, sessionID)
	if state == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state.Agent = firstNonEmpty(state.Agent, strings.TrimSpace(agentName))
	state.SessionID = strings.TrimSpace(sessionID)
	state.DelegationID = firstNonEmpty(state.DelegationID, strings.TrimSpace(delegationID))
	state.ChildCWD = firstNonEmpty(strings.TrimSpace(childCWD), state.ChildCWD)
	state.State = string(runtime.RunLifecycleStatusRunning)
	state.Running = true
	state.ApprovalPending = false
	state.UpdatedAt = time.Now()
}

func (t *remoteSubagentTracker) markApprovalPending(agentName, sessionID, delegationID, childCWD string) {
	state := t.ensure(agentName, sessionID)
	if state == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state.Agent = firstNonEmpty(state.Agent, strings.TrimSpace(agentName))
	state.SessionID = strings.TrimSpace(sessionID)
	state.DelegationID = firstNonEmpty(state.DelegationID, strings.TrimSpace(delegationID))
	state.ChildCWD = firstNonEmpty(strings.TrimSpace(childCWD), state.ChildCWD)
	state.State = string(runtime.RunLifecycleStatusWaitingApproval)
	state.Running = true
	state.ApprovalPending = true
	state.UpdatedAt = time.Now()
}

func (t *remoteSubagentTracker) clearApproval(agentName, sessionID string) {
	state := t.ensure(agentName, sessionID)
	if state == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state.State = string(runtime.RunLifecycleStatusRunning)
	state.Running = true
	state.ApprovalPending = false
	state.UpdatedAt = time.Now()
}

func (t *remoteSubagentTracker) beginToolCall(agentName, sessionID string) {
	state := t.ensure(agentName, sessionID)
	if state == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state.toolCallCount++
	state.ToolCallPending = state.toolCallCount > 0
	if state.State == "" {
		state.State = string(runtime.RunLifecycleStatusRunning)
		state.Running = true
	}
	state.ProgressSeq = trackerAdvanceSeq(state.ProgressSeq, 0, state)
	state.UpdatedAt = time.Now()
}

func (t *remoteSubagentTracker) endToolCall(agentName, sessionID string) {
	state := t.ensure(agentName, sessionID)
	if state == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if state.toolCallCount > 0 {
		state.toolCallCount--
	}
	state.ToolCallPending = state.toolCallCount > 0
	if state.State == "" {
		state.State = string(runtime.RunLifecycleStatusRunning)
		state.Running = true
	}
	state.ProgressSeq = trackerAdvanceSeq(state.ProgressSeq, 0, state)
	state.UpdatedAt = time.Now()
}

func (t *remoteSubagentTracker) updateAssistant(agentName, sessionID, text string) {
	state := t.ensure(agentName, sessionID)
	if state == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	prevLen := len(state.Assistant)
	state.Assistant = text
	state.LogSnapshot = logSnapshot(state.Reasoning, state.Assistant)
	if state.State == "" {
		state.State = string(runtime.RunLifecycleStatusRunning)
		state.Running = true
	}
	state.ProgressSeq = trackerAdvanceSeq(state.ProgressSeq, len(text)-prevLen, state)
	state.UpdatedAt = time.Now()
}

func (t *remoteSubagentTracker) updateReasoning(agentName, sessionID, text string) {
	state := t.ensure(agentName, sessionID)
	if state == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	prevLen := len(state.Reasoning)
	state.Reasoning = text
	state.LogSnapshot = logSnapshot(state.Reasoning, state.Assistant)
	if state.State == "" {
		state.State = string(runtime.RunLifecycleStatusRunning)
		state.Running = true
	}
	state.ProgressSeq = trackerAdvanceSeq(state.ProgressSeq, len(text)-prevLen, state)
	state.UpdatedAt = time.Now()
}

func (t *remoteSubagentTracker) updateTool(agentName, sessionID, summary string) {
	state := t.ensure(agentName, sessionID)
	if state == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state.LastTool = strings.TrimSpace(summary)
	if state.State == "" {
		state.State = string(runtime.RunLifecycleStatusRunning)
		state.Running = true
	}
	state.ProgressSeq = trackerAdvanceSeq(state.ProgressSeq, 0, state)
	state.UpdatedAt = time.Now()
}

func (t *remoteSubagentTracker) updateToolOutput(agentName, sessionID, chunk string) {
	state := t.ensure(agentName, sessionID)
	if state == nil {
		return
	}
	chunk = strings.TrimSpace(chunk)
	if chunk == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state.LatestOutput = trackerLatestOutput(state.LatestOutput, chunk)
	if state.State == "" {
		state.State = string(runtime.RunLifecycleStatusRunning)
		state.Running = true
	}
	state.ProgressSeq = trackerAdvanceSeq(state.ProgressSeq, len(chunk), state)
	state.UpdatedAt = time.Now()
}

func (t *remoteSubagentTracker) finish(agentName, sessionID, delegationID, childCWD, stateName, assistant string) {
	state := t.ensure(agentName, sessionID)
	if state == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state.Agent = firstNonEmpty(state.Agent, strings.TrimSpace(agentName))
	state.SessionID = strings.TrimSpace(sessionID)
	state.DelegationID = firstNonEmpty(state.DelegationID, strings.TrimSpace(delegationID))
	state.ChildCWD = firstNonEmpty(strings.TrimSpace(childCWD), state.ChildCWD)
	if strings.TrimSpace(assistant) != "" {
		state.Assistant = strings.TrimSpace(assistant)
	}
	state.LogSnapshot = logSnapshot(state.Reasoning, state.Assistant)
	if strings.TrimSpace(stateName) == "" {
		stateName = string(runtime.RunLifecycleStatusCompleted)
	}
	state.State = strings.TrimSpace(stateName)
	state.Running = false
	state.ApprovalPending = false
	state.ToolCallPending = false
	state.toolCallCount = 0
	state.ProgressSeq = trackerAdvanceSeq(state.ProgressSeq, 0, state)
	state.UpdatedAt = time.Now()
}

func (t *remoteSubagentTracker) inspect(agentName, sessionID string) (remoteSubagentState, bool) {
	if t == nil {
		return remoteSubagentState{}, false
	}
	key := t.key(agentName, sessionID)
	if key.sessionID == "" {
		return remoteSubagentState{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	state := t.entries[key]
	if state == nil {
		return remoteSubagentState{}, false
	}
	return *state, true
}

func logSnapshot(reasoning, assistant string) string {
	var b strings.Builder
	if trimmed := strings.TrimSpace(reasoning); trimmed != "" {
		b.WriteString(trimmed)
		b.WriteByte('\n')
	}
	if trimmed := strings.TrimSpace(assistant); trimmed != "" {
		b.WriteString(trimmed)
		b.WriteByte('\n')
	}
	return b.String()
}

func trackerLatestOutput(existing, chunk string) string {
	const maxLines = 8
	lines := make([]string, 0, maxLines)
	for _, part := range []string{existing, chunk} {
		for _, line := range strings.Split(strings.ReplaceAll(part, "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lines = append(lines, line)
			if len(lines) > maxLines {
				lines = lines[len(lines)-maxLines:]
			}
		}
	}
	return strings.Join(lines, "\n")
}

func trackerAdvanceSeq(current int, delta int, state *remoteSubagentState) int {
	if delta > 0 {
		return current + delta
	}
	if current > 0 {
		return current
	}
	if state == nil {
		return 0
	}
	return len(state.LogSnapshot) + len(state.LatestOutput)
}
