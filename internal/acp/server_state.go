package acp

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type serverState struct {
	mu         sync.Mutex
	clientCaps ClientCapabilities
	authOK     bool
	sessions   map[string]*serverSession
	liveStream map[string]*serverSession
}

type serverSession struct {
	id  string
	cwd string

	stateMu           sync.Mutex
	currentModeID     string
	availableModes    []SessionMode
	configOptions     []SessionConfigOption
	availableCommands []AvailableCommand
	planEntries       []PlanEntry

	streamMu      sync.Mutex
	answerStream  partialContentState
	thoughtStream partialContentState
	toolCalls     map[string]toolCallSnapshot
	asyncTasks    map[string]string
	asyncSessions map[string]string

	approvalMu sync.Mutex
	approver   *permissionBridge
}

func newServerState(authOK bool) *serverState {
	return &serverState{
		authOK:     authOK,
		sessions:   map[string]*serverSession{},
		liveStream: map[string]*serverSession{},
	}
}

func (s *serverState) setClientCapabilities(caps ClientCapabilities) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clientCaps = caps
}

func (s *serverState) clientCapabilities() ClientCapabilities {
	if s == nil {
		return ClientCapabilities{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientCaps
}

func (s *serverState) markAuthenticated() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authOK = true
}

func (s *serverState) requireAuthenticated() error {
	if s == nil {
		return errAuthenticationRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authOK {
		return nil
	}
	return errAuthenticationRequired
}

func (s *serverState) loadedSession(id string) *serverSession {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[strings.TrimSpace(id)]
}

func (s *serverState) session(id string) (*serverSession, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: %q", errSessionNotFound, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[strings.TrimSpace(id)]
	if !ok || sess == nil {
		return nil, fmt.Errorf("%w: %q", errSessionNotFound, id)
	}
	return sess, nil
}

func (s *serverState) storeSession(sess *serverSession) {
	if s == nil || sess == nil || strings.TrimSpace(sess.id) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[strings.TrimSpace(sess.id)] = sess
}

func (s *serverState) liveStreamSession(sessionID string) *serverSession {
	if s == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.liveStream == nil {
		s.liveStream = map[string]*serverSession{}
	}
	if sess, ok := s.liveStream[sessionID]; ok && sess != nil {
		return sess
	}
	sess := &serverSession{id: sessionID}
	s.liveStream[sessionID] = sess
	return sess
}

func (s *serverState) dropLiveStreamSession(sessionID string) {
	if s == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.liveStream, sessionID)
}

func (s *serverSession) applyState(state AdapterSessionState) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.id = strings.TrimSpace(state.SessionID)
	s.cwd = strings.TrimSpace(state.CWD)
	if state.Modes != nil {
		s.currentModeID = strings.TrimSpace(state.Modes.CurrentModeID)
		s.availableModes = append([]SessionMode(nil), state.Modes.AvailableModes...)
	}
	s.configOptions = append([]SessionConfigOption(nil), state.ConfigOptions...)
	s.availableCommands = append([]AvailableCommand(nil), state.AvailableCommands...)
	s.planEntries = append([]PlanEntry(nil), state.PlanEntries...)
}

func (s *serverSession) currentMode() string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return strings.TrimSpace(s.currentModeID)
}

func (s *serverSession) permissionBridge(conn *Conn) *permissionBridge {
	if s == nil {
		return nil
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if s.approver == nil {
		s.approver = newPermissionBridge(conn, s.id, s.currentMode)
	}
	return s.approver
}

func (s *serverSession) modeState() *SessionModeState {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if len(s.availableModes) == 0 && strings.TrimSpace(s.currentModeID) == "" {
		return nil
	}
	return &SessionModeState{
		AvailableModes: append([]SessionMode(nil), s.availableModes...),
		CurrentModeID:  strings.TrimSpace(s.currentModeID),
	}
}

func (s *serverSession) configOptionsSnapshot() []SessionConfigOption {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return append([]SessionConfigOption(nil), s.configOptions...)
}

func (s *serverSession) availableCommandsSnapshot() []AvailableCommand {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return append([]AvailableCommand(nil), s.availableCommands...)
}

func (s *serverSession) planSnapshot() []PlanEntry {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return append([]PlanEntry(nil), s.planEntries...)
}

func (s *serverSession) setPlan(entries []PlanEntry) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.planEntries = append([]PlanEntry(nil), entries...)
}

func (s *Server) syncSessionSnapshot(sessionID string, sess *serverSession, updatedAt string) error {
	if err := s.notifyAvailableCommands(sessionID, sess); err != nil {
		return err
	}
	if err := s.notifySessionInfo(sessionID, "", updatedAt); err != nil {
		return err
	}
	if err := s.notifyPlan(sessionID, sess.planSnapshot()); err != nil {
		return err
	}
	return nil
}

func currentTimestampRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
