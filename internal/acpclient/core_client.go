package acpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Update any

type UpdateEnvelope struct {
	SessionID string
	Update    Update
}

type CoreClientConfig struct {
	MCPServers          []MCPServer
	ClientInfo          *Implementation
	OnUpdate            func(UpdateEnvelope)
	OnPermissionRequest func(context.Context, RequestPermissionRequest) (RequestPermissionResponse, error)
}

type CoreClient struct {
	cfg  CoreClientConfig
	conn *Conn
}

var errUnknownSessionUpdate = errors.New("acpclient: unknown session update")

func NewCoreClient(conn *Conn, cfg CoreClientConfig) *CoreClient {
	return &CoreClient{
		cfg:  cfg,
		conn: conn,
	}
}

func (c *CoreClient) Initialize(ctx context.Context) (InitializeResponse, error) {
	var resp InitializeResponse
	err := c.conn.Call(ctx, MethodInitialize, InitializeRequest{
		ProtocolVersion: 1,
		ClientCapabilities: ClientCapabilities{
			FS:       FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
			Terminal: true,
		},
		ClientInfo: c.cfg.ClientInfo,
	}, &resp)
	if err != nil {
		return InitializeResponse{}, err
	}
	for _, method := range resp.AuthMethods {
		credential := lookupAuthCredential(method.ID)
		if strings.TrimSpace(credential) == "" {
			continue
		}
		if err := c.conn.Call(ctx, MethodAuthenticate, AuthenticateRequest{MethodID: method.ID}, &AuthenticateResponse{}); err != nil {
			return InitializeResponse{}, err
		}
		break
	}
	return resp, nil
}

func (c *CoreClient) NewSession(ctx context.Context, cwd string, meta map[string]any) (NewSessionResponse, error) {
	var resp NewSessionResponse
	err := c.conn.Call(ctx, MethodSessionNew, NewSessionRequest{
		CWD:        cwd,
		MCPServers: c.mcpServers(),
		Meta:       meta,
	}, &resp)
	return resp, err
}

func (c *CoreClient) LoadSession(ctx context.Context, sessionID string, cwd string, meta map[string]any) (LoadSessionResponse, error) {
	var resp LoadSessionResponse
	err := c.conn.Call(ctx, MethodSessionLoad, LoadSessionRequest{
		SessionID:  sessionID,
		CWD:        cwd,
		MCPServers: c.mcpServers(),
		Meta:       meta,
	}, &resp)
	return resp, err
}

func (c *CoreClient) SetMode(ctx context.Context, sessionID string, modeID string) error {
	return c.conn.Call(ctx, MethodSessionSetMode, SetSessionModeRequest{
		SessionID: sessionID,
		ModeID:    modeID,
	}, &SetSessionModeResponse{})
}

func (c *CoreClient) Prompt(ctx context.Context, sessionID string, text string, meta map[string]any) (PromptResponse, error) {
	var resp PromptResponse
	err := c.conn.Call(ctx, MethodSessionPrompt, PromptRequest{
		SessionID: sessionID,
		Prompt: []json.RawMessage{
			mustMarshalRaw(TextContent{Type: "text", Text: text}),
		},
		Meta: meta,
	}, &resp)
	return resp, err
}

func (c *CoreClient) Cancel(ctx context.Context, sessionID string) error {
	return c.conn.Call(ctx, MethodSessionCancel, CancelRequest{SessionID: sessionID}, &CancelResponse{})
}

func (c *CoreClient) TerminalOutput(ctx context.Context, sessionID, terminalID string) (TerminalOutputResponse, error) {
	var resp TerminalOutputResponse
	if c == nil || c.conn == nil {
		return resp, fmt.Errorf("acpclient: client is unavailable")
	}
	err := c.conn.Call(ctx, MethodTerminalOutput, TerminalOutputRequest{
		SessionID:  strings.TrimSpace(sessionID),
		TerminalID: strings.TrimSpace(terminalID),
	}, &resp)
	return resp, err
}

func (c *CoreClient) TerminalRelease(ctx context.Context, sessionID, terminalID string) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("acpclient: client is unavailable")
	}
	return c.conn.Call(ctx, MethodTerminalRelease, ReleaseTerminalRequest{
		SessionID:  strings.TrimSpace(sessionID),
		TerminalID: strings.TrimSpace(terminalID),
	}, nil)
}

func (c *CoreClient) handleNotification(ctx context.Context, msg Message) {
	if c == nil || msg.Method != MethodSessionUpdate || c.cfg.OnUpdate == nil {
		return
	}
	var note SessionNotification
	if err := decodeParams(msg.Params, &note); err != nil {
		return
	}
	update, err := decodeUpdate(note.Update)
	if err != nil {
		if errors.Is(err, errUnknownSessionUpdate) {
			return
		}
		return
	}
	if update == nil {
		return
	}
	c.cfg.OnUpdate(UpdateEnvelope{
		SessionID: strings.TrimSpace(note.SessionID),
		Update:    update,
	})
	_ = ctx
}

func (c *CoreClient) handlePermissionRequest(ctx context.Context, msg Message) (any, *RPCError) {
	var req RequestPermissionRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	if c.cfg.OnPermissionRequest != nil {
		resp, err := c.cfg.OnPermissionRequest(ctx, req)
		if err != nil {
			return nil, &RPCError{Code: -32000, Message: err.Error()}
		}
		return resp, nil
	}
	outcome := map[string]any{
		"outcome":  "selected",
		"optionId": "allow_once",
	}
	return RequestPermissionResponse{Outcome: mustMarshalRaw(outcome)}, nil
}

func (c *CoreClient) mcpServers() []MCPServer {
	if c == nil || len(c.cfg.MCPServers) == 0 {
		return []MCPServer{}
	}
	return append([]MCPServer(nil), c.cfg.MCPServers...)
}
