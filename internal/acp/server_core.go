package acp

import (
	"context"
	"errors"
	"strings"
	"sync"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	ksession "github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
)

type serverCore struct {
	server *Server
}

func newServerCore(server *Server) *serverCore {
	return &serverCore{server: server}
}

func (c *serverCore) Serve(ctx context.Context) error {
	return c.server.cfg.Conn.Serve(ctx, c.handleRequest, c.handleNotification)
}

func (c *serverCore) handleRequest(ctx context.Context, msg Message) (any, *RPCError) {
	s := c.server
	switch msg.Method {
	case MethodInitialize:
		var req InitializeRequest
		if err := decodeParams(msg.Params, &req); err != nil {
			return nil, invalidParamsError(err)
		}
		s.state.setClientCapabilities(req.ClientCapabilities)
		caps := s.svcs.capabilities()
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
			AuthMethods: s.svcs.authMethodList(),
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
		resp, afterWrite, err := s.newSession(ctx, req)
		if err != nil {
			return nil, requestFailed(err)
		}
		return postWriteResult{Payload: resp, AfterWrite: afterWrite}, nil
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
		resp, err := c.prompt(ctx, req)
		if err != nil {
			return nil, requestFailed(err)
		}
		return resp, nil
	default:
		return nil, &RPCError{Code: -32601, Message: "method not found"}
	}
}

func (c *serverCore) handleNotification(_ context.Context, msg Message) {
	if msg.Method == MethodSessionCancel {
		var req CancelNotification
		if err := decodeParams(msg.Params, &req); err == nil {
			c.server.cancelSession(req.SessionID)
		}
	}
}

func (c *serverCore) prompt(ctx context.Context, req PromptRequest) (resp PromptResponse, err error) {
	s := c.server
	sess, err := s.session(req.SessionID)
	if err != nil {
		return PromptResponse{}, err
	}
	sess.resetPartialStreams()
	input, err := s.promptInput(ctx, req.SessionID, req.Prompt)
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
	runResult, err := s.svcs.startPrompt(runCtx, StartPromptRequest{
		SessionID:    req.SessionID,
		InputText:    input.text,
		ContentParts: append([]model.ContentPart(nil), input.contentParts...),
		HasImages:    input.hasImages,
		Meta:         CloneMeta(req.Meta),
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
		_ = runner.Close()
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

func (c *serverCore) notifyCurrentMode(sessionID string, modeID string) error {
	return c.server.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
		SessionID: sessionID,
		Update: CurrentModeUpdate{
			SessionUpdate: UpdateCurrentMode,
			CurrentModeID: strings.TrimSpace(modeID),
		},
	})
}

func (c *serverCore) notifyAvailableCommands(sessionID string, sess *serverSession) error {
	return c.server.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
		SessionID: sessionID,
		Update: AvailableCommandsUpdate{
			SessionUpdate:     UpdateAvailableCmds,
			AvailableCommands: sess.availableCommandsSnapshot(),
		},
	})
}

func (c *serverCore) notifyPlan(sessionID string, entries []PlanEntry) error {
	return c.server.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
		SessionID: sessionID,
		Update: PlanUpdate{
			SessionUpdate: UpdatePlan,
			Entries:       append([]PlanEntry{}, entries...),
		},
	})
}

func (c *serverCore) notifyConfigOptions(sessionID string, options []SessionConfigOption) error {
	return c.server.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
		SessionID: sessionID,
		Update: ConfigOptionUpdate{
			SessionUpdate: UpdateConfigOption,
			ConfigOptions: options,
		},
	})
}

func (c *serverCore) notifySessionInfo(sessionID string, title string, updatedAt string) error {
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
	return c.server.cfg.Conn.Notify(MethodSessionUpdate, SessionNotification{
		SessionID: sessionID,
		Update:    update,
	})
}

func requestFailed(err error) *RPCError {
	if err == nil {
		return &RPCError{Code: -32603, Message: "internal error"}
	}
	switch {
	case errors.Is(err, errAuthenticationRequired):
		return &RPCError{Code: -32000, Message: err.Error()}
	case errors.Is(err, errSessionNotFound), errors.Is(err, ksession.ErrSessionNotFound):
		return &RPCError{Code: -32002, Message: err.Error()}
	default:
		return &RPCError{Code: -32603, Message: err.Error()}
	}
}
