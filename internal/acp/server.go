package acp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	imageutil "github.com/OnslaughtSnail/caelis/internal/cli/imageutil"
	"github.com/OnslaughtSnail/caelis/internal/idutil"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/internal/slashcmd"
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
	cancelled bool

	streamMu      sync.Mutex
	answerStream  partialContentState
	thoughtStream partialContentState
	toolCalls     map[string]toolCallSnapshot
	asyncTasks    map[string]string
	asyncSessions map[string]string
}

type partialContentState struct {
	pending      string
	sent         string
	firstBuf     time.Time
	pendingParts int
	policy       adaptivePartialChunkingPolicy
}

type pendingContentUpdate struct {
	updateType string
	text       string
}

type promptInputResult struct {
	text         string
	contentParts []model.ContentPart
	hasImages    bool
}

var defaultACPCommands = slashcmd.New(
	slashcmd.Definition{
		Name:        "help",
		Description: "Show the slash commands available in this ACP session.",
		InputHint:   "/help",
	},
	slashcmd.Definition{
		Name:        "status",
		Description: "Summarize the current ACP session state, model, and mode.",
		InputHint:   "/status",
	},
	slashcmd.Definition{
		Name:        "compact",
		Description: "Manually compact session history. Optionally include a short note.",
		InputHint:   "/compact [note]",
	},
)

type toolCallSnapshot struct {
	name string
	args map[string]any
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
	sess.runMu.Lock()
	if sess.runCancel != nil {
		sess.runMu.Unlock()
		cancel()
		return PromptResponse{}, fmt.Errorf("session %q is already running", req.SessionID)
	}
	sess.runCancel = cancel
	sess.cancelled = false
	sess.runMu.Unlock()
	defer func() {
		sess.runMu.Lock()
		sess.runCancel = nil
		sess.runMu.Unlock()
		cancel()
	}()
	defer func() {
		flushErr := s.flushPendingContent(req.SessionID, sess)
		sess.resetPartialStreams()
		if err == nil && flushErr != nil {
			err = flushErr
			resp = PromptResponse{}
		}
	}()

	if sess.resources == nil {
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
	stopReason := StopReasonEndTurn
	for ev, runErr := range s.cfg.Runtime.Run(runCtx, runtime.RunRequest{
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
	}) {
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

func (s *Server) notifyEvent(sessionID string, ev *session.Event, sess *serverSession) error {
	if ev == nil {
		return nil
	}
	msg := ev.Message
	if sess == nil && eventIsPartial(ev) {
		// Session replay should be authoritative history, not raw transient chunks.
		return nil
	}
	if sess != nil && !eventIsPartial(ev) && msg.Role != model.RoleAssistant {
		if err := s.flushPendingContent(sessionID, sess); err != nil {
			return err
		}
	}
	if eventIsPartial(ev) {
		if err := s.flushPendingContentForChannelSwitch(sessionID, sess, eventChannel(ev)); err != nil {
			return err
		}
		switch eventChannel(ev) {
		case "answer":
			return s.emitBufferedPartial(sessionID, sess, "answer", msg.Text)
		case "reasoning":
			return s.emitBufferedPartial(sessionID, sess, "reasoning", msg.Reasoning)
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
		if sess == nil {
			if strings.TrimSpace(msg.Reasoning) != "" {
				if err := s.emitContentUpdate(sessionID, UpdateAgentThought, strings.TrimSpace(msg.Reasoning)); err != nil {
					return err
				}
			}
			if text := strings.TrimSpace(msg.Text); text != "" {
				if err := s.emitContentUpdate(sessionID, UpdateAgentMessage, text); err != nil {
					return err
				}
			}
		} else {
			for _, update := range sess.finalizeAssistantContent(msg) {
				if err := s.emitContentUpdate(sessionID, update.updateType, update.text); err != nil {
					return err
				}
			}
		}
	}
	for _, call := range msg.ToolCalls {
		args := map[string]any{}
		if raw := strings.TrimSpace(call.Args); raw != "" {
			_ = json.Unmarshal([]byte(raw), &args)
		}
		if sess != nil {
			sess.rememberToolCall(call.ID, call.Name, args)
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
		if sess != nil {
			sess.rememberAsyncToolResult(msg.ToolResponse.Name, msg.ToolResponse.ID, msg.ToolResponse.Result)
		}
		status := ToolStatusCompleted
		if hasToolError(msg.ToolResponse.Result) {
			status = ToolStatusFailed
		}
		update := ToolCallUpdate{
			SessionUpdate: UpdateToolCallState,
			ToolCallID:    msg.ToolResponse.ID,
			Status:        ptr(status),
			RawOutput:     sanitizeToolResultForACP(msg.ToolResponse.Result),
			Locations:     toolLocations(nil, msg.ToolResponse.Result),
		}
		if content := toolCallContentForResult(msg.ToolResponse.Name, msg.ToolResponse.Result); len(content) > 0 {
			update.Content = content
		}
		if err := s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{SessionID: sessionID, Update: update}); err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(msg.ToolResponse.Name), tool.PlanToolName) && !hasToolError(msg.ToolResponse.Result) {
			entries := planEntriesFromResult(msg.ToolResponse.Result)
			if sess != nil {
				sess.setPlan(entries)
			}
			if err := s.notifyPlan(sessionID, entries); err != nil {
				return err
			}
		}
		for _, extra := range supplementalToolCallUpdates(sess, msg.ToolResponse) {
			if err := s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{SessionID: sessionID, Update: extra}); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

func (s *Server) notifySessionStreamUpdate(rootSessionID string, update sessionstream.Update) error {
	if update.Event == nil {
		return nil
	}
	sessionID := strings.TrimSpace(update.SessionID)
	if sessionID == "" || sessionID == strings.TrimSpace(rootSessionID) {
		return nil
	}
	var streamSess *serverSession
	if eventIsPartial(update.Event) || update.Event.Message.Role == model.RoleAssistant {
		streamSess = s.liveStreamSession(sessionID)
	}
	if err := s.notifyEvent(sessionID, update.Event, streamSess); err != nil {
		return err
	}
	if info, ok := runtime.LifecycleFromEvent(update.Event); ok {
		switch info.Status {
		case runtime.RunLifecycleStatusCompleted, runtime.RunLifecycleStatusFailed, runtime.RunLifecycleStatusInterrupted:
			s.dropLiveStreamSession(sessionID)
		}
	}
	return nil
}

func (s *Server) liveStreamSession(sessionID string) *serverSession {
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

func (s *Server) dropLiveStreamSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.liveStream, sessionID)
}

func (s *Server) emitBufferedPartial(sessionID string, sess *serverSession, channel string, text string) error {
	if sess == nil || text == "" {
		return nil
	}
	for _, update := range sess.enqueuePartialContent(channel, text, time.Now()) {
		if err := s.emitContentUpdate(sessionID, update.updateType, update.text); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) flushPendingContent(sessionID string, sess *serverSession) error {
	if sess == nil {
		return nil
	}
	for _, update := range sess.flushPendingContent() {
		if err := s.emitContentUpdate(sessionID, update.updateType, update.text); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) flushPendingContentForChannelSwitch(sessionID string, sess *serverSession, nextChannel string) error {
	if sess == nil {
		return nil
	}
	for _, update := range sess.flushPendingContentForChannelSwitch(nextChannel) {
		if err := s.emitContentUpdate(sessionID, update.updateType, update.text); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) emitContentUpdate(sessionID string, updateType string, text string) error {
	if text == "" {
		return nil
	}
	return s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
		SessionID: sessionID,
		Update: ContentChunk{
			SessionUpdate: updateType,
			Content:       TextContent{Type: "text", Text: text},
		},
	})
}

func (s *Server) promptInput(sessionID string, blocks []json.RawMessage) (promptInputResult, error) {
	result := promptInputResult{}
	orderedParts := make([]model.ContentPart, 0, len(blocks))
	textParts := make([]string, 0, len(blocks))
	for _, raw := range blocks {
		var base struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &base); err != nil {
			return promptInputResult{}, err
		}
		switch strings.TrimSpace(base.Type) {
		case "text":
			var block TextContent
			if err := json.Unmarshal(raw, &block); err != nil {
				return promptInputResult{}, err
			}
			text := strings.TrimSpace(block.Text)
			if text != "" {
				textParts = append(textParts, text)
				orderedParts = append(orderedParts, model.ContentPart{
					Type: model.ContentPartText,
					Text: text,
				})
			}
		case "image":
			var block ImageContent
			if err := json.Unmarshal(raw, &block); err != nil {
				return promptInputResult{}, err
			}
			part, err := s.resolveImageBlock(sessionID, block)
			if err != nil {
				return promptInputResult{}, err
			}
			if part.Type == model.ContentPartImage {
				orderedParts = append(orderedParts, part)
				result.hasImages = true
			}
		case "resource_link":
			var block ResourceLink
			if err := json.Unmarshal(raw, &block); err != nil {
				return promptInputResult{}, err
			}
			part, text, err := s.resolveResourceLink(sessionID, block)
			if err != nil {
				return promptInputResult{}, err
			}
			if part.Type == model.ContentPartImage {
				orderedParts = append(orderedParts, part)
				result.hasImages = true
			}
			if strings.TrimSpace(text) != "" {
				textParts = append(textParts, text)
				orderedParts = append(orderedParts, model.ContentPart{
					Type: model.ContentPartText,
					Text: text,
				})
			}
		case "resource":
			var block EmbeddedResource
			if err := json.Unmarshal(raw, &block); err != nil {
				return promptInputResult{}, err
			}
			part, text, err := s.resolveEmbeddedResource(block)
			if err != nil {
				return promptInputResult{}, err
			}
			if part.Type == model.ContentPartImage {
				orderedParts = append(orderedParts, part)
				result.hasImages = true
			}
			if text != "" {
				textParts = append(textParts, text)
				orderedParts = append(orderedParts, model.ContentPart{
					Type: model.ContentPartText,
					Text: text,
				})
			}
		default:
			return promptInputResult{}, fmt.Errorf("unsupported prompt block type %q", base.Type)
		}
	}
	result.text = strings.TrimSpace(strings.Join(textParts, "\n\n"))
	if result.hasImages {
		result.contentParts = compactContentParts(orderedParts)
	}
	return result, nil
}

func (s *Server) resolveResourceLink(sessionID string, link ResourceLink) (model.ContentPart, string, error) {
	uri := strings.TrimSpace(link.URI)
	if uri == "" {
		return model.ContentPart{}, "", nil
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return model.ContentPart{}, "", err
	}
	if parsed.Scheme != "file" {
		return model.ContentPart{}, "", fmt.Errorf("unsupported resource link scheme %q", parsed.Scheme)
	}
	path := filepath.Clean(parsed.Path)
	if s.linkIsImage(path, link.MimeType) {
		part, err := s.loadImageContentPart(sessionID, path, link.MimeType, link.Name)
		return part, "", err
	}
	var resp ReadTextFileResponse
	err = s.cfg.Conn.Call(context.Background(), MethodReadTextFile, ReadTextFileRequest{
		SessionID: sessionID,
		Path:      path,
	}, &resp)
	if err != nil {
		data, readErr := sessReadFile(s.sessionFS(sessionID), path)
		if readErr != nil {
			return model.ContentPart{}, "", err
		}
		resp.Content = data
	}
	label := strings.TrimSpace(link.Name)
	if label == "" {
		label = path
	}
	return model.ContentPart{}, fmt.Sprintf("[resource: %s]\n%s", label, strings.TrimSpace(resp.Content)), nil
}

func (s *Server) resolveEmbeddedResource(block EmbeddedResource) (model.ContentPart, string, error) {
	resource := block.Resource
	if imageMIME(strings.TrimSpace(resource.MimeType)) {
		raw := strings.TrimSpace(resource.Data)
		if raw == "" {
			raw = strings.TrimSpace(resource.Text)
		}
		if raw == "" {
			return model.ContentPart{}, "", nil
		}
		data, mime, err := decodeEmbeddedImageData(raw, resource.MimeType)
		if err != nil {
			return model.ContentPart{}, "", err
		}
		name := strings.TrimSpace(resource.Name)
		if name == "" {
			name = filepath.Base(strings.TrimSpace(resource.URI))
		}
		part, err := imageutil.ContentPartFromBytes(data, mime, name, nil)
		return part, "", err
	}
	text := strings.TrimSpace(resource.Text)
	if text == "" {
		return model.ContentPart{}, "", nil
	}
	name := strings.TrimSpace(resource.Name)
	if name == "" {
		name = strings.TrimSpace(resource.URI)
	}
	return model.ContentPart{}, fmt.Sprintf("[embedded resource: %s]\n%s", name, text), nil
}

func (s *Server) resolveImageBlock(sessionID string, block ImageContent) (model.ContentPart, error) {
	if uri := strings.TrimSpace(block.URI); uri != "" {
		link := ResourceLink{
			Type:     "resource_link",
			Name:     block.Name,
			URI:      uri,
			MimeType: block.MimeType,
		}
		part, _, err := s.resolveResourceLink(sessionID, link)
		return part, err
	}
	raw := strings.TrimSpace(block.Data)
	if raw == "" {
		return model.ContentPart{}, nil
	}
	data, mime, err := decodeEmbeddedImageData(raw, block.MimeType)
	if err != nil {
		return model.ContentPart{}, err
	}
	name := strings.TrimSpace(block.Name)
	if name == "" {
		name = "image"
	}
	return imageutil.ContentPartFromBytes(data, mime, name, nil)
}

func (s *Server) loadImageContentPart(sessionID string, path string, mime string, name string) (model.ContentPart, error) {
	fsys := s.sessionFS(sessionID)
	if fsys == nil {
		return model.ContentPart{}, fmt.Errorf("session file system is not available")
	}
	data, err := fsys.ReadFile(path)
	if err != nil {
		return model.ContentPart{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(path)
	}
	if strings.TrimSpace(mime) == "" {
		if detected, ok := imageutil.MimeTypeForPath(path); ok {
			mime = detected
		}
	}
	return imageutil.ContentPartFromBytes(data, mime, name, nil)
}

func (s *Server) promptImageEnabled() bool {
	if s == nil || s.cfg.PromptImageEnabled == nil {
		return false
	}
	return s.cfg.PromptImageEnabled()
}

func (s *Server) supportsPromptImage(cfg AgentSessionConfig) bool {
	if s == nil || s.cfg.SupportsPromptImage == nil {
		return false
	}
	return s.cfg.SupportsPromptImage(cfg)
}

func (s *Server) linkIsImage(path string, mime string) bool {
	if imageMIME(mime) {
		return true
	}
	return imageutil.IsImagePath(path)
}

func imageMIME(mime string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mime)), "image/")
}

func decodeEmbeddedImageData(raw string, fallbackMime string) ([]byte, string, error) {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(value), "data:") {
		parts := strings.SplitN(value, ",", 2)
		if len(parts) != 2 {
			return nil, "", fmt.Errorf("invalid image data URL")
		}
		header := strings.ToLower(parts[0])
		mime := fallbackMime
		if strings.HasPrefix(header, "data:") {
			mime = strings.TrimPrefix(parts[0], "data:")
			mime = strings.TrimSuffix(mime, ";base64")
		}
		data, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, "", fmt.Errorf("invalid image data: %w", err)
		}
		return data, mime, nil
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, "", fmt.Errorf("invalid image data: %w", err)
	}
	return data, fallbackMime, nil
}

func filterImageContentParts(parts []model.ContentPart, keepImages bool) []model.ContentPart {
	if len(parts) == 0 {
		return nil
	}
	filtered := make([]model.ContentPart, 0, len(parts))
	for _, part := range parts {
		if part.Type == model.ContentPartImage && !keepImages {
			continue
		}
		filtered = append(filtered, part)
	}
	return filtered
}

func compactContentParts(parts []model.ContentPart) []model.ContentPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]model.ContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case model.ContentPartText:
			if part.Text == "" {
				continue
			}
			if n := len(out); n > 0 && out[n-1].Type == model.ContentPartText {
				out[n-1].Text += "\n\n" + part.Text
				continue
			}
		case model.ContentPartImage:
			if strings.TrimSpace(part.Data) == "" && strings.TrimSpace(part.FileName) == "" {
				continue
			}
		}
		out = append(out, part)
	}
	return out
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

func (s *Server) advertisedSlashCommands(sess *serverSession) slashcmd.Registry {
	cmds := s.availableCommands(sess)
	defs := make([]slashcmd.Definition, 0, len(cmds))
	for _, item := range cmds {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		if name == "" {
			continue
		}
		hint := strings.TrimSpace(item.Input.Hint)
		if hint == "" {
			hint = "/" + name
		}
		defs = append(defs, slashcmd.Definition{
			Name:        name,
			Description: strings.TrimSpace(item.Description),
			InputHint:   hint,
		})
	}
	if len(defs) == 0 {
		return defaultACPCommands
	}
	return slashcmd.New(defs...)
}

func (s *Server) handleSlashCommand(ctx context.Context, sessionID string, sess *serverSession, input promptInputResult) (bool, string, error) {
	if input.hasImages || len(input.contentParts) > 0 {
		return false, "", nil
	}
	inv, ok := slashcmd.Parse(input.text)
	registry := s.advertisedSlashCommands(sess)
	if !ok || !registry.Has(inv.Name) {
		return false, "", nil
	}
	sess.resetPartialStreams()
	switch inv.Name {
	case "help":
		lines := make([]string, 0, 1+len(s.availableCommands(sess)))
		lines = append(lines, "Available commands:")
		for _, item := range s.availableCommands(sess) {
			line := "/" + item.Name
			if hint := strings.TrimSpace(item.Input.Hint); hint != "" {
				line = hint
			}
			if desc := strings.TrimSpace(item.Description); desc != "" {
				line += " - " + desc
			}
			lines = append(lines, line)
		}
		return true, StopReasonEndTurn, s.appendAssistantText(ctx, sessionID, strings.Join(lines, "\n"))
	case "status":
		return true, StopReasonEndTurn, s.appendAssistantText(ctx, sessionID, s.formatSlashStatus(sess))
	case "compact":
		return true, StopReasonEndTurn, s.handleSlashCompact(ctx, sessionID, sess, strings.TrimSpace(strings.Join(inv.Args, " ")))
	default:
		return true, StopReasonEndTurn, s.appendAssistantText(ctx, sessionID, fmt.Sprintf("Command /%s is not supported in this session.", inv.Name))
	}
}

func (s *Server) appendAssistantText(ctx context.Context, sessionID string, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	sessRef := s.sessionRef(sessionID)
	ev := &session.Event{
		ID:        fmt.Sprintf("ev_%d", time.Now().UnixNano()),
		SessionID: sessionID,
		Time:      time.Now(),
		Message: model.Message{
			Role: model.RoleAssistant,
			Text: text,
		},
	}
	if err := s.cfg.Store.AppendEvent(ctx, sessRef, ev); err != nil {
		return err
	}
	return s.notifyEvent(sessionID, ev, nil)
}

func (s *Server) formatSlashStatus(sess *serverSession) string {
	mode := ""
	if sess != nil {
		mode = strings.TrimSpace(sess.mode())
	}
	if mode == "" {
		mode = "default"
	}
	lines := []string{
		"Session status:",
		"mode: " + mode,
	}
	if sess != nil && strings.TrimSpace(sess.cwd) != "" {
		lines = append(lines, "cwd: "+sess.cwd)
	}
	if modelID := s.currentModelID(sess); modelID != "" {
		lines = append(lines, "model: "+modelID)
	}
	if entries := sess.planSnapshot(); len(entries) > 0 {
		lines = append(lines, fmt.Sprintf("plan items: %d", len(entries)))
	}
	return strings.Join(lines, "\n")
}

func (s *Server) currentModelID(sess *serverSession) string {
	if sess == nil {
		return ""
	}
	for _, item := range s.sessionConfigOptions(sess) {
		if strings.TrimSpace(item.Category) != "model" {
			continue
		}
		return strings.TrimSpace(item.CurrentValue)
	}
	return ""
}

func (s *Server) handleSlashCompact(ctx context.Context, sessionID string, sess *serverSession, note string) error {
	if s.cfg.Runtime == nil {
		return fmt.Errorf("runtime compaction is unavailable")
	}
	modelValue, err := s.cfg.NewModel(sess.agentConfig())
	if err != nil {
		return err
	}
	ev, err := s.cfg.Runtime.Compact(ctx, runtime.CompactRequest{
		AppName:   s.cfg.AppName,
		UserID:    s.cfg.UserID,
		SessionID: sessionID,
		Model:     modelValue,
		Note:      note,
	})
	if err != nil {
		return err
	}
	if ev == nil {
		return s.appendAssistantText(ctx, sessionID, "Compact skipped.")
	}
	return s.appendAssistantText(ctx, sessionID, "Compact completed.")
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

func (s *serverSession) resetPartialStreams() {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	s.answerStream = partialContentState{}
	s.thoughtStream = partialContentState{}
}

func (s *serverSession) enqueuePartialContent(channel string, text string, now time.Time) []pendingContentUpdate {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	state, updateType := s.partialState(channel)
	if state == nil || text == "" {
		return nil
	}
	state.pending += text
	state.pendingParts++
	if state.firstBuf.IsZero() {
		state.firstBuf = now
	}
	if !shouldFlushPartialState(state, now) {
		return nil
	}
	update := flushPartialState(state, updateType)
	if update == nil {
		return nil
	}
	return []pendingContentUpdate{*update}
}

func (s *serverSession) flushPendingContent() []pendingContentUpdate {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	out := make([]pendingContentUpdate, 0, 2)
	if update := flushPartialState(&s.thoughtStream, UpdateAgentThought); update != nil {
		out = append(out, *update)
	}
	if update := flushPartialState(&s.answerStream, UpdateAgentMessage); update != nil {
		out = append(out, *update)
	}
	return out
}

func (s *serverSession) flushPendingContentForChannelSwitch(nextChannel string) []pendingContentUpdate {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	var out []pendingContentUpdate
	switch strings.TrimSpace(nextChannel) {
	case "answer":
		if update := flushPartialState(&s.thoughtStream, UpdateAgentThought); update != nil {
			out = append(out, *update)
		}
	case "reasoning":
		if update := flushPartialState(&s.answerStream, UpdateAgentMessage); update != nil {
			out = append(out, *update)
		}
	}
	return out
}

func (s *serverSession) finalizeAssistantContent(msg model.Message) []pendingContentUpdate {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	out := make([]pendingContentUpdate, 0, 2)
	if update := finalizePartialState(&s.thoughtStream, UpdateAgentThought, strings.TrimSpace(msg.Reasoning)); update != nil {
		out = append(out, *update)
	}
	if update := finalizePartialState(&s.answerStream, UpdateAgentMessage, strings.TrimSpace(msg.Text)); update != nil {
		out = append(out, *update)
	}
	return out
}

func (s *serverSession) partialState(channel string) (*partialContentState, string) {
	switch strings.TrimSpace(channel) {
	case "reasoning":
		return &s.thoughtStream, UpdateAgentThought
	case "answer":
		return &s.answerStream, UpdateAgentMessage
	default:
		return nil, ""
	}
}

func shouldFlushPartialState(state *partialContentState, now time.Time) bool {
	if state == nil || state.pending == "" {
		return false
	}
	snapshot := partialQueueSnapshot{
		queuedParts: state.pendingParts,
	}
	if !state.firstBuf.IsZero() {
		snapshot.oldestAge = now.Sub(state.firstBuf)
	}
	thresholds := state.policy.thresholds(snapshot, now)
	if len(state.pending) >= thresholds.hardLimit {
		return true
	}
	if state.pendingParts >= thresholds.minTimedFlushPart && !state.firstBuf.IsZero() && now.Sub(state.firstBuf) >= thresholds.interval {
		return true
	}
	return len(state.pending) >= thresholds.softLimit && endsPartialFlushBoundary(state.pending)
}

func endsPartialFlushBoundary(text string) bool {
	if text == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(text)
	if last == utf8.RuneError {
		return false
	}
	return unicode.IsSpace(last) || unicode.IsPunct(last)
}

func flushPartialState(state *partialContentState, updateType string) *pendingContentUpdate {
	if state == nil || state.pending == "" {
		return nil
	}
	text := state.pending
	state.sent += text
	state.pending = ""
	state.firstBuf = time.Time{}
	state.pendingParts = 0
	return &pendingContentUpdate{updateType: updateType, text: text}
}

func finalizePartialState(state *partialContentState, updateType string, finalText string) *pendingContentUpdate {
	if state == nil {
		return nil
	}
	var text string
	switch {
	case finalText != "" && state.sent == "":
		text = finalText
	case finalText != "" && strings.HasPrefix(finalText, state.sent):
		text = finalText[len(state.sent):]
	case finalText != "" && state.pending != "":
		text = state.pending
	case finalText != "":
		text = ""
	case state.pending != "":
		text = state.pending
	}
	*state = partialContentState{}
	if text == "" {
		return nil
	}
	return &pendingContentUpdate{updateType: updateType, text: text}
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
	case "READ":
		return ToolKindRead
	case "WRITE", "PATCH":
		return ToolKindEdit
	case "SEARCH", "GLOB", "LIST":
		return ToolKindSearch
	case "PLAN":
		return ToolKindOther
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
	case "READ", "WRITE", "PATCH", "SEARCH", "LIST", "GLOB":
		if path, _ := args["path"].(string); strings.TrimSpace(path) != "" {
			return fmt.Sprintf("%s %s", name, strings.TrimSpace(path))
		}
	case "BASH":
		if command, _ := args["command"].(string); strings.TrimSpace(command) != "" {
			return fmt.Sprintf("BASH %s", strings.TrimSpace(command))
		}
	case "TASK":
		action := strings.TrimSpace(stringValue(args["action"]))
		display := taskActionCallDisplayName(action)
		switch strings.ToLower(action) {
		case "wait":
			if waited := friendlyWaitLabelForACP(effectiveTaskWaitMSForACP(action, args)); waited != "" {
				return fmt.Sprintf("%s %s", display, waited)
			}
			return display
		case "status", "cancel":
			if taskID := strings.TrimSpace(stringValue(args["task_id"])); taskID != "" {
				return fmt.Sprintf("%s %s", display, idutil.ShortDisplay(taskID))
			}
			return display
		default:
			taskID := strings.TrimSpace(stringValue(args["task_id"]))
			if action != "" && taskID != "" {
				return fmt.Sprintf("%s {task=%s}", display, idutil.ShortDisplay(taskID))
			}
			if action != "" {
				return display
			}
		}
	}
	return name
}

func summarizeToolResultTitle(name string) string {
	return strings.TrimSpace(name)
}

func toolCallContentForResult(toolName string, result map[string]any) []ToolCallContent {
	if !strings.EqualFold(strings.TrimSpace(toolName), "BASH") {
		return nil
	}
	terminalID := strings.TrimSpace(stringValue(result["session_id"]))
	if terminalID == "" {
		return nil
	}
	return []ToolCallContent{{
		Type:       "terminal",
		TerminalID: terminalID,
	}}
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

func sanitizeToolResultForACP(result map[string]any) map[string]any {
	return sanitizeToolResultMapForACP(result, true)
}

func (s *serverSession) rememberToolCall(callID string, name string, args map[string]any) {
	if s == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.toolCalls == nil {
		s.toolCalls = map[string]toolCallSnapshot{}
	}
	cp := make(map[string]any, len(args))
	for key, value := range args {
		cp[key] = value
	}
	s.toolCalls[callID] = toolCallSnapshot{name: strings.TrimSpace(name), args: cp}
}

func (s *serverSession) rememberAsyncToolResult(toolName string, callID string, result map[string]any) {
	if s == nil || len(result) == 0 {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(toolName), "BASH") {
		return
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	taskID := strings.TrimSpace(stringValue(result["task_id"]))
	sessionID := strings.TrimSpace(stringValue(result["session_id"]))
	if taskID == "" && sessionID == "" {
		return
	}
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if taskID != "" {
		if s.asyncTasks == nil {
			s.asyncTasks = map[string]string{}
		}
		s.asyncTasks[taskID] = callID
	}
	if sessionID != "" {
		if s.asyncSessions == nil {
			s.asyncSessions = map[string]string{}
		}
		s.asyncSessions[sessionID] = callID
	}
}

func (s *serverSession) toolCall(callID string) (toolCallSnapshot, bool) {
	if s == nil {
		return toolCallSnapshot{}, false
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return toolCallSnapshot{}, false
	}
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	snap, ok := s.toolCalls[callID]
	return snap, ok
}

func (s *serverSession) asyncOriginCallID(result map[string]any) string {
	if s == nil || len(result) == 0 {
		return ""
	}
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if taskID := strings.TrimSpace(stringValue(result["task_id"])); taskID != "" && s.asyncTasks != nil {
		if callID := strings.TrimSpace(s.asyncTasks[taskID]); callID != "" {
			return callID
		}
	}
	if sessionID := strings.TrimSpace(stringValue(result["session_id"])); sessionID != "" && s.asyncSessions != nil {
		if callID := strings.TrimSpace(s.asyncSessions[sessionID]); callID != "" {
			return callID
		}
	}
	return ""
}

func taskActionCallDisplayName(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "wait":
		return "WAIT"
	case "status":
		return "CHECK"
	case "write":
		return "WRITE"
	case "cancel":
		return "CANCEL"
	case "list":
		return "LIST"
	default:
		return "TASK"
	}
}

func friendlyWaitLabelForACP(waitMS int) string {
	switch {
	case waitMS < 0:
		return ""
	case waitMS == 0:
		return "0s"
	case waitMS%1000 == 0:
		return fmt.Sprintf("%d s", waitMS/1000)
	case waitMS < 1000:
		return fmt.Sprintf("%dms", waitMS)
	default:
		return fmt.Sprintf("%.1f s", float64(waitMS)/1000.0)
	}
}

func effectiveTaskWaitMSForACP(action string, args map[string]any) int {
	if !strings.EqualFold(strings.TrimSpace(action), "wait") {
		return -1
	}
	if len(args) == 0 {
		return 5000
	}
	rawWaitMS, ok := args["yield_time_ms"]
	if !ok || rawWaitMS == nil {
		return 5000
	}
	waitMS, ok := intValue(rawWaitMS)
	if !ok {
		return 5000
	}
	if waitMS <= 0 {
		return 5000
	}
	return waitMS
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

func supplementalToolCallUpdates(sess *serverSession, resp *model.ToolResponse) []ToolCallUpdate {
	if sess == nil || resp == nil || len(resp.Result) == 0 {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(resp.Name), "TASK") || hasToolError(resp.Result) {
		return nil
	}
	call, ok := sess.toolCall(resp.ID)
	if !ok || !strings.EqualFold(strings.TrimSpace(call.name), "TASK") {
		return nil
	}
	action := strings.TrimSpace(stringValue(call.args["action"]))
	if !strings.EqualFold(action, "cancel") {
		return nil
	}
	state := strings.TrimSpace(stringValue(resp.Result["state"]))
	if !strings.EqualFold(state, "cancelled") {
		return nil
	}
	originCallID := strings.TrimSpace(sess.asyncOriginCallID(resp.Result))
	if originCallID == "" || originCallID == strings.TrimSpace(resp.ID) {
		return nil
	}
	status := ToolStatusCompleted
	return []ToolCallUpdate{{
		SessionUpdate: UpdateToolCallState,
		ToolCallID:    originCallID,
		Status:        ptr(status),
		RawOutput:     sanitizeToolResultForACP(cancelledOriginResult(resp.Result)),
	}}
}

func cancelledOriginResult(result map[string]any) map[string]any {
	if len(result) == 0 {
		return map[string]any{"state": "cancelled", "cancelled": true}
	}
	out := map[string]any{
		"state":     "cancelled",
		"cancelled": true,
	}
	for _, key := range []string{"task_id", "session_id", "command", "workdir", "route", "tty", "latest_output"} {
		if value, ok := result[key]; ok && value != nil && strings.TrimSpace(fmt.Sprint(value)) != "" {
			out[key] = value
		}
	}
	if output, ok := result["output"]; ok && output != nil {
		out["output"] = sanitizeToolResultValueForACP(output)
	}
	return out
}

func sanitizeToolResultMapForACP(result map[string]any, topLevel bool) map[string]any {
	if len(result) == 0 {
		return result
	}
	out := make(map[string]any, len(result))
	for key, value := range result {
		trimmed := strings.TrimSpace(key)
		if strings.HasPrefix(trimmed, "_ui_") {
			continue
		}
		if topLevel && strings.EqualFold(trimmed, "metadata") {
			continue
		}
		out[key] = sanitizeToolResultValueForACP(value)
	}
	return out
}

func sanitizeToolResultValueForACP(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return sanitizeToolResultMapForACP(typed, false)
	case []any:
		out := make([]any, 0, len(typed))
		for _, one := range typed {
			out = append(out, sanitizeToolResultValueForACP(one))
		}
		return out
	default:
		return value
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
