package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type SessionResources struct {
	Runtime  toolexec.Runtime
	Tools    []tool.Tool
	Policies []policy.Hook
	Close    func(context.Context) error
}

type SessionResourceFactory func(context.Context, string, string, ClientCapabilities, []MCPServer, func() string) (*SessionResources, error)
type AgentSessionConfig struct {
	ModeID       string
	ConfigValues map[string]string
}

type SessionConfigOptionTemplate struct {
	ID           string
	Name         string
	Description  string
	Category     string
	DefaultValue string
	Options      []SessionConfigSelectOption
}

type AgentFactory func(stream bool, sessionCWD string, cfg AgentSessionConfig) (agent.Agent, error)
type ModelFactory func(cfg AgentSessionConfig) (model.LLM, error)
type SessionListFactory func(context.Context, SessionListRequest) (SessionListResponse, error)
type SessionConfigStateFactory func(cfg AgentSessionConfig, templates []SessionConfigOptionTemplate) []SessionConfigOption
type SessionConfigNormalizer func(cfg AgentSessionConfig) AgentSessionConfig
type AvailableCommandsFactory func(cfg AgentSessionConfig) []AvailableCommand

type AuthValidator func(context.Context, AuthenticateRequest) error

type ServerConfig struct {
	Conn                *Conn
	Runtime             *runtime.Runtime
	Store               session.Store
	Model               model.LLM
	NewModel            ModelFactory
	AppName             string
	UserID              string
	WorkspaceRoot       string
	ProtocolVersion     ProtocolVersion
	AgentInfo           *Implementation
	AuthMethods         []AuthMethod
	Authenticate        AuthValidator
	SessionModes        []SessionMode
	DefaultModeID       string
	SessionConfig       []SessionConfigOptionTemplate
	NewSessionResources SessionResourceFactory
	NewAgent            AgentFactory
	ListSessions        SessionListFactory
	AvailableCommands   AvailableCommandsFactory
	SessionConfigState  SessionConfigStateFactory
	NormalizeConfig     SessionConfigNormalizer
	SupportsPromptImage func(AgentSessionConfig) bool
	PromptImageEnabled  func() bool
}

type Server struct {
	cfg ServerConfig

	mu         sync.Mutex
	clientCaps ClientCapabilities
	authOK     bool
	sessions   map[string]*serverSession
	liveStream map[string]*serverSession
}

type serverSession struct {
	id        string
	cwd       string
	resources *SessionResources

	stateMu      sync.Mutex
	modeID       string
	configValues map[string]string
	planEntries  []PlanEntry

	runMu     sync.Mutex
	runCancel context.CancelFunc
	runner    runtime.Runner
	cancelled bool

	streamMu      sync.Mutex
	answerStream  partialContentState
	thoughtStream partialContentState
	toolCalls     map[string]toolCallSnapshot
	asyncTasks    map[string]string
	asyncSessions map[string]string
}

func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Conn == nil {
		return nil, fmt.Errorf("acp: conn is required")
	}
	if cfg.Runtime == nil {
		return nil, fmt.Errorf("acp: runtime is required")
	}
	if cfg.Store == nil {
		return nil, fmt.Errorf("acp: store is required")
	}
	if cfg.Model == nil && cfg.NewModel == nil {
		return nil, fmt.Errorf("acp: model is required")
	}
	if cfg.NewSessionResources == nil {
		return nil, fmt.Errorf("acp: session resource factory is required")
	}
	if cfg.NewAgent == nil {
		return nil, fmt.Errorf("acp: agent factory is required")
	}
	cfg.WorkspaceRoot = filepath.Clean(strings.TrimSpace(cfg.WorkspaceRoot))
	if cfg.WorkspaceRoot == "" {
		return nil, fmt.Errorf("acp: workspace root is required")
	}
	if cfg.ProtocolVersion == 0 {
		cfg.ProtocolVersion = CurrentProtocolVersion
	}
	if cfg.AgentInfo == nil {
		cfg.AgentInfo = &Implementation{Name: "caelis"}
	}
	cfg.DefaultModeID = strings.TrimSpace(cfg.DefaultModeID)
	if len(cfg.SessionModes) > 0 && cfg.DefaultModeID == "" {
		cfg.DefaultModeID = strings.TrimSpace(cfg.SessionModes[0].ID)
	}
	return &Server{
		cfg:        cfg,
		authOK:     len(cfg.AuthMethods) == 0,
		sessions:   map[string]*serverSession{},
		liveStream: map[string]*serverSession{},
	}, nil
}

func (s *Server) Serve(ctx context.Context) error {
	return s.cfg.Conn.Serve(ctx, s.handleRequest, s.handleNotification)
}

func (s *Server) handleRequest(ctx context.Context, msg Message) (any, *RPCError) {
	switch msg.Method {
	case MethodInitialize:
		var req InitializeRequest
		if err := decodeParams(msg.Params, &req); err != nil {
			return nil, invalidParamsError(err)
		}
		s.mu.Lock()
		s.clientCaps = req.ClientCapabilities
		s.mu.Unlock()
		return InitializeResponse{
			ProtocolVersion: s.cfg.ProtocolVersion,
			AgentCapabilities: AgentCapabilities{
				LoadSession: true,
				MCP: McpCapabilities{
					HTTP: true,
					SSE:  true,
				},
				Prompt: PromptCapabilities{
					EmbeddedContext: true,
					Image:           s.promptImageEnabled(),
				},
				Session: SessionCapabilities{
					List: s.sessionListCapability(),
				},
			},
			AgentInfo:   s.cfg.AgentInfo,
			AuthMethods: append([]AuthMethod(nil), s.cfg.AuthMethods...),
		}, nil
	case MethodAuthenticate:
		var req AuthenticateRequest
		if err := decodeParams(msg.Params, &req); err != nil {
			return nil, invalidParamsError(err)
		}
		if err := s.authenticate(ctx, req); err != nil {
			return nil, requestFailed(err)
		}
		return AuthenticateResponse{}, nil
	case MethodSessionNew:
		if err := s.requireAuthenticated(); err != nil {
			return nil, requestFailed(err)
		}
		var req NewSessionRequest
		if err := decodeParams(msg.Params, &req); err != nil {
			return nil, invalidParamsError(err)
		}
		resp, err := s.newSession(ctx, req)
		if err != nil {
			return nil, requestFailed(err)
		}
		return resp, nil
	case MethodSessionList:
		if err := s.requireAuthenticated(); err != nil {
			return nil, requestFailed(err)
		}
		var req SessionListRequest
		if err := decodeParams(msg.Params, &req); err != nil {
			return nil, invalidParamsError(err)
		}
		resp, err := s.listSessions(ctx, req)
		if err != nil {
			return nil, requestFailed(err)
		}
		return resp, nil
	case MethodSessionLoad:
		if err := s.requireAuthenticated(); err != nil {
			return nil, requestFailed(err)
		}
		var req LoadSessionRequest
		if err := decodeParams(msg.Params, &req); err != nil {
			return nil, invalidParamsError(err)
		}
		resp, err := s.loadSession(ctx, req)
		if err != nil {
			return nil, requestFailed(err)
		}
		return resp, nil
	case MethodSessionSetMode:
		if err := s.requireAuthenticated(); err != nil {
			return nil, requestFailed(err)
		}
		var req SetSessionModeRequest
		if err := decodeParams(msg.Params, &req); err != nil {
			return nil, invalidParamsError(err)
		}
		resp, err := s.setSessionMode(ctx, req)
		if err != nil {
			return nil, requestFailed(err)
		}
		return resp, nil
	case MethodSessionSetConfig:
		if err := s.requireAuthenticated(); err != nil {
			return nil, requestFailed(err)
		}
		var req SetSessionConfigOptionRequest
		if err := decodeParams(msg.Params, &req); err != nil {
			return nil, invalidParamsError(err)
		}
		resp, err := s.setSessionConfigOption(ctx, req)
		if err != nil {
			return nil, requestFailed(err)
		}
		return resp, nil
	case MethodSessionPrompt:
		if err := s.requireAuthenticated(); err != nil {
			return nil, requestFailed(err)
		}
		var req PromptRequest
		if err := decodeParams(msg.Params, &req); err != nil {
			return nil, invalidParamsError(err)
		}
		resp, err := s.prompt(ctx, req)
		if err != nil {
			return nil, requestFailed(err)
		}
		return resp, nil
	default:
		return nil, &RPCError{Code: -32601, Message: "method not found"}
	}
}

func (s *Server) handleNotification(ctx context.Context, msg Message) {
	switch msg.Method {
	case MethodSessionCancel:
		var req CancelNotification
		if err := decodeParams(msg.Params, &req); err == nil {
			s.cancelSession(req.SessionID)
		}
	}
}

func (s *Server) prompt(ctx context.Context, req PromptRequest) (resp PromptResponse, err error) {
	sess, err := s.session(req.SessionID)
	if err != nil {
		return PromptResponse{}, err
	}
	sess.resetPartialStreams()
	input, err := s.promptInput(req.SessionID, req.Prompt)
	if err != nil {
		return PromptResponse{}, err
	}
	if handled, stopReason, err := s.handleSlashCommand(ctx, req.SessionID, sess, input); handled {
		if err != nil {
			return PromptResponse{}, err
		}
		return PromptResponse{StopReason: stopReason}, nil
	}
	ag, err := s.cfg.NewAgent(true, sess.cwd, sess.agentConfig())
	if err != nil {
		return PromptResponse{}, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer func() {
		flushErr := s.flushPendingContent(req.SessionID, sess)
		sess.resetPartialStreams()
		if err == nil && flushErr != nil {
			err = flushErr
			resp = PromptResponse{}
		}
	}()

	if sess.resources == nil {
		cancel()
		return PromptResponse{}, fmt.Errorf("session %q resources not initialized", req.SessionID)
	}
	approver := newPermissionBridge(s.cfg.Conn, req.SessionID, sess.mode)
	runCtx = toolexec.WithApprover(runCtx, approver)
	runCtx = policy.WithToolAuthorizer(runCtx, approver)
	var (
		sessionStreamErrMu sync.Mutex
		sessionStreamErr   error
	)
	setSessionStreamErr := func(err error) {
		if err == nil {
			return
		}
		sessionStreamErrMu.Lock()
		if sessionStreamErr == nil {
			sessionStreamErr = err
			cancel()
		}
		sessionStreamErrMu.Unlock()
	}
	getSessionStreamErr := func() error {
		sessionStreamErrMu.Lock()
		defer sessionStreamErrMu.Unlock()
		return sessionStreamErr
	}
	runCtx = sessionstream.WithStreamer(runCtx, sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
		if err := s.notifySessionStreamUpdate(req.SessionID, update); err != nil {
			setSessionStreamErr(err)
		}
	}))
	llm, err := s.resolveModel(sess.agentConfig())
	if err != nil {
		return PromptResponse{}, err
	}
	runInput := sessionmode.Inject(input.text, sess.mode())
	runParts := append([]model.ContentPart(nil), input.contentParts...)
	if input.hasImages {
		if controlText := strings.TrimSpace(sessionmode.Inject("", sess.mode())); controlText != "" {
			runParts = append([]model.ContentPart{{
				Type: model.ContentPartText,
				Text: controlText,
			}}, runParts...)
		}
		runInput = ""
	}
	if !s.supportsPromptImage(sess.agentConfig()) {
		runParts = filterImageContentParts(runParts, false)
	}
	submission := runtime.Submission{
		Text:         runInput,
		ContentParts: runParts,
		Mode:         runtime.SubmissionConversation,
	}
	sess.runMu.Lock()
	activeRunner := sess.runner
	sess.runMu.Unlock()
	if activeRunner != nil {
		if submitErr := activeRunner.Submit(submission); submitErr != nil {
			// If the runner was closed between our check and the Submit call,
			// fall through to create a new runner instead of failing.
			if !errors.Is(submitErr, runtime.ErrRunnerClosed) {
				cancel()
				return PromptResponse{}, submitErr
			}
			// Runner closed: fall through to create a new runner with the
			// existing runCtx (cancel has not been called yet).
		} else {
			cancel()
			return PromptResponse{StopReason: StopReasonEndTurn}, nil
		}
	}
	runner, err := s.cfg.Runtime.Run(runCtx, runtime.RunRequest{
		AppName:      s.cfg.AppName,
		UserID:       s.cfg.UserID,
		SessionID:    req.SessionID,
		Input:        runInput,
		ContentParts: runParts,
		Agent:        ag,
		Model:        llm,
		Tools:        sess.resources.Tools,
		CoreTools: tool.CoreToolsConfig{
			Runtime: sess.resources.Runtime,
		},
		Policies: sess.resources.Policies,
	})
	if err != nil {
		cancel()
		return PromptResponse{}, err
	}
	sess.runMu.Lock()
	sess.runner = runner
	sess.runCancel = cancel
	sess.cancelled = false
	sess.runMu.Unlock()
	defer func() {
		sess.runMu.Lock()
		sess.runner = nil
		sess.runCancel = nil
		sess.runMu.Unlock()
		_ = runner.Close() // Close always returns nil; safe to ignore.
	}()
	stopReason := StopReasonEndTurn
	for ev, runErr := range runner.Events() {
		if streamErr := getSessionStreamErr(); streamErr != nil {
			return PromptResponse{}, streamErr
		}
		if runErr != nil {
			if errors.Is(runErr, context.Canceled) || toolexec.IsApprovalAborted(runErr) {
				stopReason = StopReasonCancelled
				return PromptResponse{StopReason: stopReason}, nil
			}
			if toolexec.IsErrorCode(runErr, toolexec.ErrorCodeApprovalAborted) || toolexec.IsErrorCode(runErr, toolexec.ErrorCodeApprovalRequired) {
				stopReason = StopReasonCancelled
				return PromptResponse{StopReason: stopReason}, nil
			}
			return PromptResponse{}, runErr
		}
		if ev == nil {
			continue
		}
		if info, ok := runtime.LifecycleFromEvent(ev); ok {
			if info.Status == runtime.RunLifecycleStatusInterrupted {
				stopReason = StopReasonCancelled
			}
			continue
		}
		if err := s.notifyEvent(req.SessionID, ev, sess); err != nil {
			return PromptResponse{}, err
		}
	}
	if streamErr := getSessionStreamErr(); streamErr != nil {
		return PromptResponse{}, streamErr
	}
	return PromptResponse{StopReason: stopReason}, nil
}

func (s *Server) sessionRef(sessionID string) *session.Session {
	return &session.Session{
		AppName: s.cfg.AppName,
		UserID:  s.cfg.UserID,
		ID:      strings.TrimSpace(sessionID),
	}
}

func (s *Server) sessionModeState(sess *serverSession) *SessionModeState {
	if sess == nil || len(s.cfg.SessionModes) == 0 {
		return nil
	}
	return &SessionModeState{
		AvailableModes: append([]SessionMode(nil), s.cfg.SessionModes...),
		CurrentModeID:  sess.mode(),
	}
}

func (s *Server) sessionConfigOptions(sess *serverSession) []SessionConfigOption {
	if sess == nil || len(s.cfg.SessionConfig) == 0 {
		return nil
	}
	if s.cfg.SessionConfigState != nil {
		return s.cfg.SessionConfigState(sess.agentConfig(), append([]SessionConfigOptionTemplate(nil), s.cfg.SessionConfig...))
	}
	values := sess.configSnapshot()
	out := make([]SessionConfigOption, 0, len(s.cfg.SessionConfig))
	for _, item := range s.cfg.SessionConfig {
		current := strings.TrimSpace(values[item.ID])
		if current == "" {
			current = strings.TrimSpace(item.DefaultValue)
		}
		out = append(out, SessionConfigOption{
			Type:         "select",
			ID:           item.ID,
			Name:         item.Name,
			Description:  item.Description,
			Category:     item.Category,
			CurrentValue: current,
			Options:      append([]SessionConfigSelectOption(nil), item.Options...),
		})
	}
	return out
}

func (s *Server) configOptionSupports(sess *serverSession, id string, value string) bool {
	id = strings.TrimSpace(id)
	value = strings.TrimSpace(value)
	if id == "" || value == "" {
		return false
	}
	for _, item := range s.sessionConfigOptions(sess) {
		if strings.TrimSpace(item.ID) != id {
			continue
		}
		for _, option := range item.Options {
			if strings.TrimSpace(option.Value) == value {
				return true
			}
		}
		return false
	}
	if template, ok := s.configTemplate(id); ok {
		return template.supports(value)
	}
	return false
}

func (s *Server) configTemplate(id string) (SessionConfigOptionTemplate, bool) {
	id = strings.TrimSpace(id)
	for _, item := range s.cfg.SessionConfig {
		if strings.TrimSpace(item.ID) == id {
			return item, true
		}
	}
	return SessionConfigOptionTemplate{}, false
}

func (s *Server) notifyCurrentMode(sessionID string, modeID string) error {
	return s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
		SessionID: sessionID,
		Update: CurrentModeUpdate{
			SessionUpdate: UpdateCurrentMode,
			CurrentModeID: strings.TrimSpace(modeID),
		},
	})
}

func (s *Server) notifyAvailableCommands(sessionID string, sess *serverSession) error {
	return s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
		SessionID: sessionID,
		Update: AvailableCommandsUpdate{
			SessionUpdate:     UpdateAvailableCmds,
			AvailableCommands: s.availableCommands(sess),
		},
	})
}

func (s *Server) notifyPlan(sessionID string, entries []PlanEntry) error {
	return s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
		SessionID: sessionID,
		Update: PlanUpdate{
			SessionUpdate: UpdatePlan,
			Entries:       append([]PlanEntry(nil), entries...),
		},
	})
}

func (s *Server) notifyConfigOptions(sessionID string, options []SessionConfigOption) error {
	return s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
		SessionID: sessionID,
		Update: ConfigOptionUpdate{
			SessionUpdate: UpdateConfigOption,
			ConfigOptions: options,
		},
	})
}

func (s *Server) availableCommands(sess *serverSession) []AvailableCommand {
	if s.cfg.AvailableCommands != nil {
		if cmds := s.cfg.AvailableCommands(sess.agentConfig()); len(cmds) > 0 {
			return cmds
		}
	}
	defs := defaultACPCommands.Definitions()
	out := make([]AvailableCommand, 0, len(defs))
	for _, item := range defs {
		out = append(out, AvailableCommand{
			Name:        item.Name,
			Description: item.Description,
			Input:       AvailableCommandInput{Hint: item.InputHint},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *serverSession) planSnapshot() []PlanEntry {
	if s == nil {
		return nil
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	out := make([]PlanEntry, 0, len(s.planEntries))
	out = append(out, s.planEntries...)
	return out
}

func (s *serverSession) setPlan(entries []PlanEntry) {
	if s == nil {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.planEntries = append([]PlanEntry(nil), entries...)
}

func loadPlanEntries(raw any) []PlanEntry {
	payload := anyMap(raw)
	if payload == nil {
		return nil
	}
	return normalizePlanEntries(payload["entries"])
}

func planEntriesFromResult(result map[string]any) []PlanEntry {
	if len(result) == 0 {
		return nil
	}
	return normalizePlanEntries(result["entries"])
}

func normalizePlanEntries(raw any) []PlanEntry {
	var decoded []PlanEntry
	if err := decodeACPViaJSON(raw, &decoded); err != nil {
		return nil
	}
	out := make([]PlanEntry, 0, len(decoded))
	for _, item := range decoded {
		content := strings.TrimSpace(item.Content)
		status := strings.TrimSpace(item.Status)
		if content == "" || status == "" {
			continue
		}
		out = append(out, PlanEntry{Content: content, Status: status})
	}
	return out
}

func decodeACPViaJSON(in any, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func decodeParams(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func invalidParamsError(err error) *RPCError {
	return &RPCError{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)}
}

func requestFailed(err error) *RPCError {
	return &RPCError{Code: -32000, Message: err.Error()}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		v, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}

func ptr[T any](v T) *T {
	return &v
}

func (t SessionConfigOptionTemplate) supports(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, option := range t.Options {
		if strings.TrimSpace(option.Value) == value {
			return true
		}
	}
	return false
}

func anyMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, one := range typed {
			out[key] = one
		}
		return out
	default:
		return nil
	}
}

func sessReadFile(fsys toolexec.FileSystem, path string) (string, error) {
	if fsys == nil {
		return "", fmt.Errorf("filesystem is unavailable")
	}
	data, err := fsys.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

