package acp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/idutil"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func (s *Server) newSession(ctx context.Context, req NewSessionRequest) (NewSessionResponse, error) {
	if err := s.validateSessionCWD(req.CWD); err != nil {
		return NewSessionResponse{}, err
	}
	sessionID := idutil.NewSessionID()
	sess := &serverSession{
		id:           sessionID,
		cwd:          filepath.Clean(req.CWD),
		modeID:       s.initialModeID(),
		configValues: s.initialConfigValues(),
	}
	s.normalizeSessionConfig(sess)
	resources, err := s.newResources(ctx, sessionID, sess.cwd, req.MCPServers, sess.mode)
	if err != nil {
		return NewSessionResponse{}, err
	}
	sess.resources = resources
	s.mu.Lock()
	s.sessions[sessionID] = sess
	s.mu.Unlock()
	sessRef, err := s.cfg.Store.GetOrCreate(ctx, &session.Session{
		AppName: s.cfg.AppName,
		UserID:  s.cfg.UserID,
		ID:      sessionID,
	})
	if err != nil {
		return NewSessionResponse{}, err
	}
	if err := s.persistSessionState(ctx, sessRef, sess); err != nil {
		return NewSessionResponse{}, err
	}
	if err := s.notifyAvailableCommands(sessionID, sess); err != nil {
		return NewSessionResponse{}, err
	}
	if err := s.notifyPlan(sessionID, sess.planSnapshot()); err != nil {
		return NewSessionResponse{}, err
	}
	return NewSessionResponse{
		SessionID:     sessionID,
		ConfigOptions: s.sessionConfigOptions(sess),
		Modes:         s.sessionModeState(sess),
	}, nil
}

func (s *Server) listSessions(ctx context.Context, req SessionListRequest) (SessionListResponse, error) {
	if s.cfg.ListSessions == nil {
		return SessionListResponse{}, fmt.Errorf("session listing is not supported")
	}
	return s.cfg.ListSessions(ctx, req)
}

func (s *Server) loadSession(ctx context.Context, req LoadSessionRequest) (LoadSessionResponse, error) {
	if err := s.validateSessionCWD(req.CWD); err != nil {
		return LoadSessionResponse{}, err
	}
	sessRef := &session.Session{AppName: s.cfg.AppName, UserID: s.cfg.UserID, ID: strings.TrimSpace(req.SessionID)}
	if err := s.ensureSessionExists(ctx, sessRef); err != nil {
		return LoadSessionResponse{}, err
	}
	events, err := s.cfg.Store.ListEvents(ctx, sessRef)
	if err != nil {
		return LoadSessionResponse{}, err
	}
	state, err := s.cfg.Store.SnapshotState(ctx, sessRef)
	if err != nil {
		return LoadSessionResponse{}, err
	}
	modeID, configValues, storedCWD, planEntries := s.restoreSessionState(state)
	resolvedCWD := filepath.Clean(req.CWD)
	if storedCWD != "" && storedCWD != resolvedCWD {
		return LoadSessionResponse{}, fmt.Errorf("cwd %q does not match persisted session cwd %q", resolvedCWD, storedCWD)
	}
	if storedCWD != "" {
		resolvedCWD = storedCWD
	}
	sess := s.loadedSession(req.SessionID)
	if sess != nil {
		if sess.cwd != resolvedCWD {
			return LoadSessionResponse{}, fmt.Errorf("session %q is already loaded with cwd %q", req.SessionID, sess.cwd)
		}
		sess.stateMu.Lock()
		sess.modeID = modeID
		sess.configValues = configValues
		sess.planEntries = planEntries
		sess.stateMu.Unlock()
		s.normalizeSessionConfig(sess)
	} else {
		sess = &serverSession{
			id:           req.SessionID,
			cwd:          resolvedCWD,
			modeID:       modeID,
			configValues: configValues,
			planEntries:  planEntries,
		}
		s.normalizeSessionConfig(sess)
		resources, err := s.newResources(ctx, req.SessionID, sess.cwd, req.MCPServers, sess.mode)
		if err != nil {
			return LoadSessionResponse{}, err
		}
		sess.resources = resources
		s.mu.Lock()
		s.sessions[req.SessionID] = sess
		s.mu.Unlock()
	}
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if lifecycle, ok := runtime.LifecycleFromEvent(ev); ok {
			_ = lifecycle
			continue
		}
		if err := s.notifyEvent(req.SessionID, ev, nil); err != nil {
			return LoadSessionResponse{}, err
		}
	}
	if err := s.notifyAvailableCommands(req.SessionID, sess); err != nil {
		return LoadSessionResponse{}, err
	}
	if err := s.notifyPlan(req.SessionID, sess.planSnapshot()); err != nil {
		return LoadSessionResponse{}, err
	}
	return LoadSessionResponse{
		ConfigOptions: s.sessionConfigOptions(sess),
		Modes:         s.sessionModeState(sess),
	}, nil
}

func (s *Server) authenticate(ctx context.Context, req AuthenticateRequest) error {
	methodID := strings.TrimSpace(req.MethodID)
	if methodID == "" {
		return fmt.Errorf("authentication method is required")
	}
	if !s.hasAuthMethod(methodID) {
		return fmt.Errorf("unsupported authentication method %q", methodID)
	}
	if s.cfg.Authenticate != nil {
		if err := s.cfg.Authenticate(ctx, req); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.authOK = true
	s.mu.Unlock()
	return nil
}

func (s *Server) setSessionMode(ctx context.Context, req SetSessionModeRequest) (SetSessionModeResponse, error) {
	sess, err := s.session(req.SessionID)
	if err != nil {
		return SetSessionModeResponse{}, err
	}
	modeID := strings.TrimSpace(req.ModeID)
	if modeID == "" {
		return SetSessionModeResponse{}, fmt.Errorf("modeId is required")
	}
	if !s.modeExists(modeID) {
		return SetSessionModeResponse{}, fmt.Errorf("unsupported mode %q", modeID)
	}
	sess.stateMu.Lock()
	sess.modeID = modeID
	if s.hasConfigCategory("mode") {
		if sess.configValues == nil {
			sess.configValues = map[string]string{}
		}
		for _, item := range s.cfg.SessionConfig {
			if strings.TrimSpace(item.Category) == "mode" {
				sess.configValues[item.ID] = modeID
			}
		}
	}
	sess.stateMu.Unlock()
	s.normalizeSessionConfig(sess)
	if err := s.persistSessionState(ctx, s.sessionRef(sess.id), sess); err != nil {
		return SetSessionModeResponse{}, err
	}
	if err := s.notifyCurrentMode(req.SessionID, modeID); err != nil {
		return SetSessionModeResponse{}, err
	}
	if err := s.notifyConfigOptions(req.SessionID, s.sessionConfigOptions(sess)); err != nil {
		return SetSessionModeResponse{}, err
	}
	return SetSessionModeResponse{}, nil
}

func (s *Server) setSessionConfigOption(ctx context.Context, req SetSessionConfigOptionRequest) (SetSessionConfigOptionResponse, error) {
	sess, err := s.session(req.SessionID)
	if err != nil {
		return SetSessionConfigOptionResponse{}, err
	}
	cfgID := strings.TrimSpace(req.ConfigID)
	if cfgID == "" {
		return SetSessionConfigOptionResponse{}, fmt.Errorf("configId is required")
	}
	value := strings.TrimSpace(req.Value)
	if !s.configOptionSupports(sess, cfgID, value) {
		if _, ok := s.configTemplate(cfgID); !ok {
			return SetSessionConfigOptionResponse{}, fmt.Errorf("unsupported config option %q", cfgID)
		}
		return SetSessionConfigOptionResponse{}, fmt.Errorf("unsupported value %q for config option %q", value, cfgID)
	}
	template, ok := s.configTemplate(cfgID)
	if !ok {
		return SetSessionConfigOptionResponse{}, fmt.Errorf("unsupported config option %q", cfgID)
	}
	sess.stateMu.Lock()
	if sess.configValues == nil {
		sess.configValues = map[string]string{}
	}
	sess.configValues[cfgID] = value
	if strings.TrimSpace(template.Category) == "mode" && s.modeExists(value) {
		sess.modeID = value
	}
	sess.stateMu.Unlock()
	s.normalizeSessionConfig(sess)
	if err := s.persistSessionState(ctx, s.sessionRef(sess.id), sess); err != nil {
		return SetSessionConfigOptionResponse{}, err
	}
	options := s.sessionConfigOptions(sess)
	if strings.TrimSpace(template.Category) == "mode" {
		if err := s.notifyCurrentMode(req.SessionID, value); err != nil {
			return SetSessionConfigOptionResponse{}, err
		}
	}
	if err := s.notifyConfigOptions(req.SessionID, options); err != nil {
		return SetSessionConfigOptionResponse{}, err
	}
	return SetSessionConfigOptionResponse{ConfigOptions: options}, nil
}

func (s *Server) requireAuthenticated() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authOK {
		return nil
	}
	return fmt.Errorf("authentication required")
}

func (s *Server) hasAuthMethod(methodID string) bool {
	methodID = strings.TrimSpace(methodID)
	for _, method := range s.cfg.AuthMethods {
		if strings.TrimSpace(method.ID) == methodID {
			return true
		}
	}
	return false
}

func (s *Server) initialModeID() string {
	if s.modeExists(s.cfg.DefaultModeID) {
		return s.cfg.DefaultModeID
	}
	return ""
}

func (s *Server) hasConfigCategory(category string) bool {
	category = strings.TrimSpace(category)
	if category == "" {
		return false
	}
	for _, item := range s.cfg.SessionConfig {
		if strings.TrimSpace(item.Category) == category {
			return true
		}
	}
	return false
}

func (s *Server) modeExists(modeID string) bool {
	modeID = strings.TrimSpace(modeID)
	if modeID == "" {
		return false
	}
	for _, mode := range s.cfg.SessionModes {
		if strings.TrimSpace(mode.ID) == modeID {
			return true
		}
	}
	return false
}

func (s *Server) initialConfigValues() map[string]string {
	if len(s.cfg.SessionConfig) == 0 {
		return nil
	}
	values := make(map[string]string, len(s.cfg.SessionConfig))
	for _, item := range s.cfg.SessionConfig {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		values[item.ID] = strings.TrimSpace(item.DefaultValue)
	}
	return values
}

func (s *Server) restoreSessionState(state map[string]any) (string, map[string]string, string, []PlanEntry) {
	modeID := sessionmode.LoadSnapshot(state)
	if !s.modeExists(modeID) {
		modeID = s.initialModeID()
	}
	values := s.initialConfigValues()
	raw := anyMap(state["acp"])
	if raw == nil {
		return modeID, values, "", loadPlanEntries(state["plan"])
	}
	cwd, _ := raw["cwd"].(string)
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	if !filepath.IsAbs(cwd) {
		cwd = ""
	}
	if storedMode, _ := raw["modeId"].(string); s.modeExists(storedMode) {
		modeID = strings.TrimSpace(storedMode)
	}
	storedValues := anyMap(raw["configValues"])
	for _, template := range s.cfg.SessionConfig {
		if storedValues == nil {
			break
		}
		rawValue, _ := storedValues[template.ID].(string)
		if template.supports(rawValue) {
			if values == nil {
				values = map[string]string{}
			}
			values[template.ID] = strings.TrimSpace(rawValue)
		}
	}
	return modeID, values, cwd, loadPlanEntries(state["plan"])
}

func (s *Server) persistSessionState(ctx context.Context, sessRef *session.Session, sess *serverSession) error {
	if sessRef == nil || sess == nil {
		return nil
	}
	modeID := sess.mode()
	configValues := sess.configSnapshot()
	planEntries := sess.planSnapshot()
	if updater, ok := s.cfg.Store.(session.StateUpdateStore); ok {
		return updater.UpdateState(ctx, sessRef, func(values map[string]any) (map[string]any, error) {
			if values == nil {
				values = map[string]any{}
			}
			values = sessionmode.StoreSnapshot(values, modeID)
			values["acp"] = map[string]any{
				"cwd":          sess.cwd,
				"modeId":       modeID,
				"configValues": configValues,
			}
			values["plan"] = map[string]any{
				"version": 1,
				"entries": planEntries,
			}
			return values, nil
		})
	}
	values, err := s.cfg.Store.SnapshotState(ctx, sessRef)
	if err != nil {
		return err
	}
	if values == nil {
		values = map[string]any{}
	}
	values = sessionmode.StoreSnapshot(values, modeID)
	values["acp"] = map[string]any{
		"cwd":          sess.cwd,
		"modeId":       modeID,
		"configValues": configValues,
	}
	values["plan"] = map[string]any{
		"version": 1,
		"entries": planEntries,
	}
	return s.cfg.Store.ReplaceState(ctx, sessRef, values)
}

func (s *Server) sessionListCapability() *SessionListCapability {
	if s.cfg.ListSessions == nil {
		return nil
	}
	return &SessionListCapability{}
}

func (s *Server) newResources(ctx context.Context, sessionID string, sessionCWD string, mcpServers []MCPServer, modeResolver func() string) (*SessionResources, error) {
	s.mu.Lock()
	caps := s.clientCaps
	s.mu.Unlock()
	return s.cfg.NewSessionResources(ctx, sessionID, sessionCWD, caps, mcpServers, modeResolver)
}

func (s *Server) ensureSessionExists(ctx context.Context, sessRef *session.Session) error {
	existsStore, ok := s.cfg.Store.(session.ExistenceStore)
	if !ok {
		return nil
	}
	exists, err := existsStore.SessionExists(ctx, sessRef)
	if err != nil {
		return err
	}
	if !exists {
		return session.ErrSessionNotFound
	}
	return nil
}

func (s *Server) resolveModel(cfg AgentSessionConfig) (model.LLM, error) {
	if s.cfg.NewModel != nil {
		return s.cfg.NewModel(cfg)
	}
	if s.cfg.Model == nil {
		return nil, fmt.Errorf("acp: model is not configured")
	}
	return s.cfg.Model, nil
}

func (s *Server) normalizeSessionConfig(sess *serverSession) {
	if sess == nil || s.cfg.NormalizeConfig == nil {
		return
	}
	next := s.cfg.NormalizeConfig(sess.agentConfig())
	sess.stateMu.Lock()
	if strings.TrimSpace(next.ModeID) != "" && s.modeExists(next.ModeID) {
		sess.modeID = strings.TrimSpace(next.ModeID)
	}
	sess.configValues = cloneStringMap(next.ConfigValues)
	sess.stateMu.Unlock()
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func (s *Server) loadedSession(id string) *serverSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[strings.TrimSpace(id)]
}

func (s *Server) session(id string) (*serverSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[strings.TrimSpace(id)]
	if !ok || sess == nil {
		return nil, fmt.Errorf("unknown session %q", id)
	}
	return sess, nil
}

func (s *serverSession) mode() string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return strings.TrimSpace(s.modeID)
}

func (s *serverSession) configSnapshot() map[string]string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if len(s.configValues) == 0 {
		return nil
	}
	out := make(map[string]string, len(s.configValues))
	for key, value := range s.configValues {
		out[key] = value
	}
	return out
}

func (s *serverSession) agentConfig() AgentSessionConfig {
	return AgentSessionConfig{
		ModeID:       s.mode(),
		ConfigValues: s.configSnapshot(),
	}
}

func pathWithinRoot(root string, path string) bool {
	root = resolvePathForContainment(root)
	path = resolvePathForContainment(path)
	if root == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

func resolvePathForContainment(path string) string {
	current := filepath.Clean(strings.TrimSpace(path))
	if current == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(current); err == nil {
		return filepath.Clean(resolved)
	}
	suffix := make([]string, 0, 4)
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(strings.TrimSpace(path))
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
		if _, err := os.Lstat(current); err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil {
			continue
		}
		for i := len(suffix) - 1; i >= 0; i-- {
			resolved = filepath.Join(resolved, suffix[i])
		}
		return filepath.Clean(resolved)
	}
}

func (s *Server) sessionFS(sessionID string) toolexec.FileSystem {
	sess, err := s.session(sessionID)
	if err != nil || sess.resources == nil || sess.resources.Runtime == nil {
		return nil
	}
	return sess.resources.Runtime.FileSystem()
}

func (s *Server) validateSessionCWD(cwd string) error {
	value := filepath.Clean(strings.TrimSpace(cwd))
	if value == "" {
		return fmt.Errorf("cwd is required")
	}
	if !filepath.IsAbs(value) {
		return fmt.Errorf("cwd %q must be an absolute path", value)
	}
	if !pathWithinRoot(s.cfg.WorkspaceRoot, value) {
		return fmt.Errorf("cwd %q is outside workspace root %q", value, s.cfg.WorkspaceRoot)
	}
	return nil
}

func (s *Server) cancelSession(id string) {
	s.mu.Lock()
	sess := s.sessions[strings.TrimSpace(id)]
	s.mu.Unlock()
	if sess == nil {
		return
	}
	sess.runMu.Lock()
	sess.cancelled = true
	cancel := sess.runCancel
	sess.runMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
