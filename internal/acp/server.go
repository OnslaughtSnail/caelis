package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	ksession "github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type SessionResources struct {
	Runtime  toolexec.Runtime
	Tools    []tool.Tool
	Policies []policy.Hook
	Close    func(context.Context) error
}

type SessionResourceFactory func(context.Context, string, string, ClientCapabilities, func() string) (*SessionResources, error)
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

type PromptFactory func(sessionCWD string) (string, error)
type AgentFactory func(stream bool, sessionCWD string, systemPrompt string, cfg AgentSessionConfig) (agent.Agent, error)
type ModelFactory func(cfg AgentSessionConfig) (model.LLM, error)
type SessionListFactory func(context.Context, SessionListRequest) (SessionListResponse, error)
type SessionConfigStateFactory func(cfg AgentSessionConfig, templates []SessionConfigOptionTemplate) []SessionConfigOption
type SessionConfigNormalizer func(cfg AgentSessionConfig) AgentSessionConfig
type AvailableCommandsFactory func(cfg AgentSessionConfig) []AvailableCommand

type AuthValidator func(context.Context, AuthenticateRequest) error

type ServerConfig struct {
	Conn            *Conn
	ProtocolVersion ProtocolVersion
	AgentInfo       *Implementation
	AuthMethods     []AuthMethod
	Authenticate    AuthValidator
	Adapter         Adapter
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

func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Conn == nil {
		return nil, fmt.Errorf("acp: conn is required")
	}
	if cfg.Adapter == nil {
		return nil, fmt.Errorf("acp: adapter is required")
	}
	if cfg.ProtocolVersion == 0 {
		cfg.ProtocolVersion = CurrentProtocolVersion
	}
	if cfg.AgentInfo == nil {
		cfg.AgentInfo = &Implementation{Name: "caelis"}
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
		caps := s.cfg.Adapter.Capabilities()
		var sessionList *SessionListCapability
		if caps.SessionList {
			sessionList = &SessionListCapability{}
		}
		return InitializeResponse{
			ProtocolVersion: s.cfg.ProtocolVersion,
			AgentCapabilities: AgentCapabilities{
				LoadSession:     true,
				MCPCapabilities: MCPCapabilities{},
				Prompt: PromptCapabilities{
					EmbeddedContext: true,
					Image:           caps.PromptImage,
				},
				Session: SessionCapabilities{
					List: sessionList,
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
	runCtx, cancel := context.WithCancel(ctx)
	defer func() {
		flushErr := s.flushPendingContent(req.SessionID, sess)
		sess.resetPartialStreams()
		if err == nil && flushErr != nil {
			err = flushErr
			resp = PromptResponse{}
		}
	}()

	approver := sess.permissionBridge(s.cfg.Conn)
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
	runResult, err := s.cfg.Adapter.StartPrompt(runCtx, StartPromptRequest{
		SessionID:    req.SessionID,
		InputText:    input.text,
		ContentParts: append([]model.ContentPart(nil), input.contentParts...),
		HasImages:    input.hasImages,
		OnSessionStream: func(update sessionstream.Update) error {
			if err := s.notifySessionStreamUpdate(req.SessionID, update); err != nil {
				setSessionStreamErr(err)
				return err
			}
			return nil
		},
	})
	if err != nil {
		cancel()
		return PromptResponse{}, err
	}
	if runResult.Handle == nil {
		cancel()
		stopReason := strings.TrimSpace(runResult.StopReason)
		if stopReason == "" {
			stopReason = StopReasonEndTurn
		}
		return PromptResponse{StopReason: stopReason}, nil
	}
	runner := runResult.Handle
	defer func() {
		cancel()
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
			AvailableCommands: sess.availableCommandsSnapshot(),
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

func (s *Server) notifySessionInfo(sessionID string, title string, updatedAt string) error {
	update := SessionInfoUpdate{SessionUpdate: UpdateSessionInfo}
	if trimmed := strings.TrimSpace(title); trimmed != "" {
		update.Title = ptr(trimmed)
	}
	if trimmed := strings.TrimSpace(updatedAt); trimmed != "" {
		update.UpdatedAt = ptr(trimmed)
	}
	if update.Title == nil && update.UpdatedAt == nil {
		return nil
	}
	return s.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
		SessionID: sessionID,
		Update:    update,
	})
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

func DecodeACPViaJSON(in any, out any) error {
	return decodeACPViaJSON(in, out)
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
	if err == nil {
		return &RPCError{Code: -32603, Message: "internal error"}
	}
	switch {
	case errors.Is(err, errAuthenticationRequired):
		return &RPCError{Code: -32000, Message: err.Error()}
	case errors.Is(err, errSessionNotFound), errors.Is(err, ksession.ErrSessionNotFound), errors.Is(err, os.ErrNotExist):
		return &RPCError{Code: -32002, Message: err.Error()}
	default:
		return &RPCError{Code: -32603, Message: err.Error()}
	}
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
