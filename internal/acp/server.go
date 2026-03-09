package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/OnslaughtSnail/caelis/internal/idutil"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type SessionResources struct {
	Runtime  toolexec.Runtime
	Tools    []tool.Tool
	Policies []policy.Hook
	Close    func(context.Context) error
}

type SessionResourceFactory func(context.Context, string, ClientCapabilities, []MCPServer) (*SessionResources, error)
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

type AgentFactory func(stream bool, cfg AgentSessionConfig) (agent.Agent, error)

type AuthValidator func(context.Context, AuthenticateRequest) error

type ServerConfig struct {
	Conn                *Conn
	Runtime             *runtime.Runtime
	Store               session.Store
	Model               model.LLM
	AppName             string
	UserID              string
	WorkspaceDir        string
	ProtocolVersion     string
	AgentInfo           *Implementation
	AuthMethods         []AuthMethod
	Authenticate        AuthValidator
	SessionModes        []SessionMode
	DefaultModeID       string
	SessionConfig       []SessionConfigOptionTemplate
	NewSessionResources SessionResourceFactory
	NewAgent            AgentFactory
}

type Server struct {
	cfg ServerConfig

	mu         sync.Mutex
	clientCaps ClientCapabilities
	authOK     bool
	sessions   map[string]*serverSession
}

type serverSession struct {
	id        string
	cwd       string
	resources *SessionResources

	stateMu      sync.Mutex
	modeID       string
	configValues map[string]string

	runMu       sync.Mutex
	runCancel   context.CancelFunc
	cancelled   bool
	answerSeen  bool
	thoughtSeen bool
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
	if cfg.Model == nil {
		return nil, fmt.Errorf("acp: model is required")
	}
	if cfg.NewSessionResources == nil {
		return nil, fmt.Errorf("acp: session resource factory is required")
	}
	if cfg.NewAgent == nil {
		return nil, fmt.Errorf("acp: agent factory is required")
	}
	cfg.WorkspaceDir = filepath.Clean(strings.TrimSpace(cfg.WorkspaceDir))
	if cfg.WorkspaceDir == "" {
		return nil, fmt.Errorf("acp: workspace dir is required")
	}
	if cfg.ProtocolVersion == "" {
		cfg.ProtocolVersion = "0.2.0"
	}
	if cfg.AgentInfo == nil {
		cfg.AgentInfo = &Implementation{Name: "caelis"}
	}
	cfg.DefaultModeID = strings.TrimSpace(cfg.DefaultModeID)
	if len(cfg.SessionModes) > 0 && cfg.DefaultModeID == "" {
		cfg.DefaultModeID = strings.TrimSpace(cfg.SessionModes[0].ID)
	}
	return &Server{
		cfg:      cfg,
		authOK:   len(cfg.AuthMethods) == 0,
		sessions: map[string]*serverSession{},
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
				Prompt:  PromptCapabilities{},
				Session: SessionCapabilities{},
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
	case MethodSessionCancel:
		if err := s.requireAuthenticated(); err != nil {
			return nil, requestFailed(err)
		}
		var req CancelNotification
		if err := decodeParams(msg.Params, &req); err != nil {
			return nil, invalidParamsError(err)
		}
		s.cancelSession(req.SessionID)
		return map[string]any{}, nil
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

func (s *Server) newSession(ctx context.Context, req NewSessionRequest) (NewSessionResponse, error) {
	if err := s.validateSessionCWD(req.CWD); err != nil {
		return NewSessionResponse{}, err
	}
	sessionID := idutil.NewSessionID()
	resources, err := s.newResources(ctx, sessionID, req.MCPServers)
	if err != nil {
		return NewSessionResponse{}, err
	}
	s.mu.Lock()
	sess := &serverSession{
		id:           sessionID,
		cwd:          filepath.Clean(req.CWD),
		resources:    resources,
		modeID:       s.initialModeID(),
		configValues: s.initialConfigValues(),
	}
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
	return NewSessionResponse{
		SessionID:     sessionID,
		ConfigOptions: s.sessionConfigOptions(sess),
		Modes:         s.sessionModeState(sess),
	}, nil
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
	modeID, configValues := s.restoreSessionState(state)
	sess := s.loadedSession(req.SessionID)
	if sess != nil {
		sess.stateMu.Lock()
		sess.modeID = modeID
		sess.configValues = configValues
		sess.stateMu.Unlock()
	} else {
		resources, err := s.newResources(ctx, req.SessionID, req.MCPServers)
		if err != nil {
			return LoadSessionResponse{}, err
		}
		s.mu.Lock()
		sess = &serverSession{
			id:           req.SessionID,
			cwd:          filepath.Clean(req.CWD),
			resources:    resources,
			modeID:       modeID,
			configValues: configValues,
		}
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
	return LoadSessionResponse{
		ConfigOptions: s.sessionConfigOptions(sess),
		Modes:         s.sessionModeState(sess),
	}, nil
}

func (s *Server) prompt(ctx context.Context, req PromptRequest) (PromptResponse, error) {
	sess, err := s.session(req.SessionID)
	if err != nil {
		return PromptResponse{}, err
	}
	input, err := s.promptInput(req.SessionID, req.Prompt)
	if err != nil {
		return PromptResponse{}, err
	}
	ag, err := s.cfg.NewAgent(true, sess.agentConfig())
	if err != nil {
		return PromptResponse{}, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	sess.runMu.Lock()
	if sess.runCancel != nil {
		sess.runMu.Unlock()
		cancel()
		return PromptResponse{}, fmt.Errorf("session %q is already running", req.SessionID)
	}
	sess.runCancel = cancel
	sess.cancelled = false
	sess.answerSeen = false
	sess.thoughtSeen = false
	sess.runMu.Unlock()
	defer func() {
		sess.runMu.Lock()
		sess.runCancel = nil
		sess.runMu.Unlock()
		cancel()
	}()

	approver := newPermissionBridge(s.cfg.Conn, req.SessionID, sess.mode)
	runCtx = toolexec.WithApprover(runCtx, approver)
	runCtx = policy.WithToolAuthorizer(runCtx, approver)
	stopReason := StopReasonEndTurn
	for ev, runErr := range s.cfg.Runtime.Run(runCtx, runtime.RunRequest{
		AppName:   s.cfg.AppName,
		UserID:    s.cfg.UserID,
		SessionID: req.SessionID,
		Input:     sessionmode.Inject(input, sess.mode()),
		Agent:     ag,
		Model:     s.cfg.Model,
		Tools:     sess.resources.Tools,
		CoreTools: tool.CoreToolsConfig{
			Runtime: sess.resources.Runtime,
		},
		Policies: sess.resources.Policies,
	}) {
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
	return PromptResponse{StopReason: stopReason}, nil
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
	sess.stateMu.Unlock()
	if err := s.persistSessionState(ctx, s.sessionRef(sess.id), sess); err != nil {
		return SetSessionModeResponse{}, err
	}
	if err := s.notifyCurrentMode(req.SessionID, modeID); err != nil {
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
	template, ok := s.configTemplate(cfgID)
	if !ok {
		return SetSessionConfigOptionResponse{}, fmt.Errorf("unsupported config option %q", cfgID)
	}
	if !template.supports(value) {
		return SetSessionConfigOptionResponse{}, fmt.Errorf("unsupported value %q for config option %q", value, cfgID)
	}
	sess.stateMu.Lock()
	if sess.configValues == nil {
		sess.configValues = map[string]string{}
	}
	sess.configValues[cfgID] = value
	sess.stateMu.Unlock()
	if err := s.persistSessionState(ctx, s.sessionRef(sess.id), sess); err != nil {
		return SetSessionConfigOptionResponse{}, err
	}
	options := s.sessionConfigOptions(sess)
	if err := s.notifyConfigOptions(req.SessionID, options); err != nil {
		return SetSessionConfigOptionResponse{}, err
	}
	return SetSessionConfigOptionResponse{ConfigOptions: options}, nil
}

func (s *Server) notifyEvent(sessionID string, ev *session.Event, sess *serverSession) error {
	if ev == nil {
		return nil
	}
	msg := ev.Message
	if eventIsPartial(ev) {
		switch eventChannel(ev) {
		case "answer":
			if sess != nil {
				sess.answerSeen = true
			}
			return s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
				SessionID: sessionID,
				Update: ContentChunk{
					SessionUpdate: UpdateAgentMessage,
					Content:       TextContent{Type: "text", Text: msg.Text},
				},
			})
		case "reasoning":
			if sess != nil {
				sess.thoughtSeen = true
			}
			return s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
				SessionID: sessionID,
				Update: ContentChunk{
					SessionUpdate: UpdateAgentThought,
					Content:       TextContent{Type: "text", Text: msg.Reasoning},
				},
			})
		}
	}
	if msg.Role == model.RoleUser {
		text := sessionmode.VisibleText(strings.TrimSpace(msg.TextContent()))
		if text == "" {
			return nil
		}
		if sess != nil {
			// ACP clients already know the live prompt they just submitted.
			// Re-emitting it as a session/update duplicates user history on clients
			// like acpx; keep user-message replay only for session/load.
			return nil
		}
		return s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
			SessionID: sessionID,
			Update: ContentChunk{
				SessionUpdate: UpdateUserMessage,
				Content:       TextContent{Type: "text", Text: text},
			},
		})
	}
	if msg.Role == model.RoleAssistant {
		if strings.TrimSpace(msg.Reasoning) != "" && (sess == nil || !sess.thoughtSeen) {
			if err := s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
				SessionID: sessionID,
				Update: ContentChunk{
					SessionUpdate: UpdateAgentThought,
					Content:       TextContent{Type: "text", Text: strings.TrimSpace(msg.Reasoning)},
				},
			}); err != nil {
				return err
			}
		}
		text := strings.TrimSpace(msg.Text)
		if text != "" && (sess == nil || !sess.answerSeen) {
			if err := s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
				SessionID: sessionID,
				Update: ContentChunk{
					SessionUpdate: UpdateAgentMessage,
					Content:       TextContent{Type: "text", Text: text},
				},
			}); err != nil {
				return err
			}
		}
		if sess != nil {
			sess.answerSeen = false
			sess.thoughtSeen = false
		}
	}
	for _, call := range msg.ToolCalls {
		args := map[string]any{}
		if raw := strings.TrimSpace(call.Args); raw != "" {
			_ = json.Unmarshal([]byte(raw), &args)
		}
		update := ToolCall{
			SessionUpdate: UpdateToolCall,
			ToolCallID:    call.ID,
			Title:         summarizeToolCallTitle(call.Name, args),
			Kind:          toolKindForName(call.Name),
			Status:        ToolStatusPending,
			RawInput:      args,
			Locations:     toolLocations(args, nil),
		}
		if err := s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{SessionID: sessionID, Update: update}); err != nil {
			return err
		}
	}
	if msg.ToolResponse != nil {
		status := ToolStatusCompleted
		if hasToolError(msg.ToolResponse.Result) {
			status = ToolStatusFailed
		}
		title := summarizeToolResultTitle(msg.ToolResponse.Name)
		update := ToolCallUpdate{
			SessionUpdate: UpdateToolCallState,
			ToolCallID:    msg.ToolResponse.ID,
			Title:         &title,
			Status:        ptr(status),
			RawOutput:     msg.ToolResponse.Result,
			Locations:     toolLocations(nil, msg.ToolResponse.Result),
		}
		return s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{SessionID: sessionID, Update: update})
	}
	return nil
}

func (s *Server) promptInput(sessionID string, blocks []json.RawMessage) (string, error) {
	parts := make([]string, 0, len(blocks))
	for _, raw := range blocks {
		var base struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &base); err != nil {
			return "", err
		}
		switch strings.TrimSpace(base.Type) {
		case "text":
			var block TextContent
			if err := json.Unmarshal(raw, &block); err != nil {
				return "", err
			}
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		case "resource_link":
			var block ResourceLink
			if err := json.Unmarshal(raw, &block); err != nil {
				return "", err
			}
			text, err := s.resolveResourceLink(sessionID, block)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		case "resource":
			var block EmbeddedResource
			if err := json.Unmarshal(raw, &block); err != nil {
				return "", err
			}
			text := strings.TrimSpace(block.Resource.Text)
			if text != "" {
				name := strings.TrimSpace(block.Resource.Name)
				if name == "" {
					name = strings.TrimSpace(block.Resource.URI)
				}
				parts = append(parts, fmt.Sprintf("[embedded resource: %s]\n%s", name, text))
			}
		default:
			return "", fmt.Errorf("unsupported prompt block type %q", base.Type)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), nil
}

func (s *Server) resolveResourceLink(sessionID string, link ResourceLink) (string, error) {
	uri := strings.TrimSpace(link.URI)
	if uri == "" {
		return "", nil
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("unsupported resource link scheme %q", parsed.Scheme)
	}
	path := filepath.Clean(parsed.Path)
	var resp ReadTextFileResponse
	err = s.cfg.Conn.Call(context.Background(), MethodReadTextFile, ReadTextFileRequest{
		SessionID: sessionID,
		Path:      path,
	}, &resp)
	if err != nil {
		data, readErr := sessReadFile(s.sessionFS(sessionID), path)
		if readErr != nil {
			return "", err
		}
		resp.Content = data
	}
	label := strings.TrimSpace(link.Name)
	if label == "" {
		label = path
	}
	return fmt.Sprintf("[resource: %s]\n%s", label, strings.TrimSpace(resp.Content)), nil
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
	if value != s.cfg.WorkspaceDir {
		return fmt.Errorf("cwd %q does not match server workspace %q", value, s.cfg.WorkspaceDir)
	}
	return nil
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

func (s *Server) restoreSessionState(state map[string]any) (string, map[string]string) {
	modeID := sessionmode.LoadSnapshot(state)
	if !s.modeExists(modeID) {
		modeID = s.initialModeID()
	}
	values := s.initialConfigValues()
	raw := anyMap(state["acp"])
	if raw == nil {
		return modeID, values
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
	return modeID, values
}

func (s *Server) persistSessionState(ctx context.Context, sessRef *session.Session, sess *serverSession) error {
	if sessRef == nil || sess == nil {
		return nil
	}
	values, err := s.cfg.Store.SnapshotState(ctx, sessRef)
	if err != nil {
		return err
	}
	if values == nil {
		values = map[string]any{}
	}
	values = sessionmode.StoreSnapshot(values, sess.mode())
	values["acp"] = map[string]any{
		"modeId":       sess.mode(),
		"configValues": sess.configSnapshot(),
	}
	return s.cfg.Store.ReplaceState(ctx, sessRef, values)
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

func (s *Server) notifyConfigOptions(sessionID string, options []SessionConfigOption) error {
	return s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
		SessionID: sessionID,
		Update: ConfigOptionUpdate{
			SessionUpdate: UpdateConfigOption,
			ConfigOptions: options,
		},
	})
}

func (s *Server) newResources(ctx context.Context, sessionID string, mcpServers []MCPServer) (*SessionResources, error) {
	s.mu.Lock()
	caps := s.clientCaps
	s.mu.Unlock()
	return s.cfg.NewSessionResources(ctx, sessionID, caps, mcpServers)
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

func eventIsPartial(ev *session.Event) bool {
	if ev == nil || ev.Meta == nil {
		return false
	}
	raw, ok := ev.Meta["partial"].(bool)
	return ok && raw
}

func eventChannel(ev *session.Event) string {
	if ev == nil || ev.Meta == nil {
		return ""
	}
	value, _ := ev.Meta["channel"].(string)
	return strings.TrimSpace(value)
}

func toolKindForName(name string) string {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "READ", "STAT":
		return ToolKindRead
	case "WRITE", "PATCH":
		return ToolKindEdit
	case "SEARCH", "GLOB", "LIST":
		return ToolKindSearch
	case "BASH", "TASK":
		return ToolKindExecute
	case "DELEGATE":
		return ToolKindOther
	default:
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(name)), "MCP__") {
			return ToolKindFetch
		}
		return ToolKindOther
	}
}

func summarizeToolCallTitle(name string, args map[string]any) string {
	name = strings.TrimSpace(name)
	switch strings.ToUpper(name) {
	case "READ", "WRITE", "PATCH", "STAT", "SEARCH", "LIST", "GLOB":
		if path, _ := args["path"].(string); strings.TrimSpace(path) != "" {
			return fmt.Sprintf("%s %s", name, strings.TrimSpace(path))
		}
	case "BASH":
		if command, _ := args["command"].(string); strings.TrimSpace(command) != "" {
			return fmt.Sprintf("BASH %s", strings.TrimSpace(command))
		}
	}
	return name
}

func summarizeToolResultTitle(name string) string {
	return strings.TrimSpace(name)
}

func toolLocations(args map[string]any, result map[string]any) []ToolCallLocation {
	path := ""
	if result != nil {
		path, _ = result["path"].(string)
	}
	if path == "" && args != nil {
		path, _ = args["path"].(string)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return []ToolCallLocation{{Path: path}}
}

func hasToolError(result map[string]any) bool {
	if result == nil {
		return false
	}
	text := strings.TrimSpace(fmt.Sprint(result["error"]))
	return text != "" && text != "<nil>"
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
