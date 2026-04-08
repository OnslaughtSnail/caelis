package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
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
	cfg   ServerConfig
	svcs  *serverServices
	state *serverState
	core  *serverCore
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
	srv := &Server{
		cfg:   cfg,
		svcs:  newServerServices(cfg),
		state: newServerState(len(cfg.AuthMethods) == 0),
	}
	srv.core = newServerCore(srv)
	return srv, nil
}

func (s *Server) Serve(ctx context.Context) error {
	return s.core.Serve(ctx)
}

func (s *Server) notifyCurrentMode(sessionID string, modeID string) error {
	return s.core.notifyCurrentMode(sessionID, modeID)
}

func (s *Server) notifyAvailableCommands(sessionID string, sess *serverSession) error {
	return s.core.notifyAvailableCommands(sessionID, sess)
}

func (s *Server) notifyPlan(sessionID string, entries []PlanEntry) error {
	return s.core.notifyPlan(sessionID, entries)
}

func (s *Server) notifyConfigOptions(sessionID string, options []SessionConfigOption) error {
	return s.core.notifyConfigOptions(sessionID, options)
}

func (s *Server) notifySessionInfo(sessionID string, title string, updatedAt string) error {
	return s.core.notifySessionInfo(sessionID, title, updatedAt)
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

func invalidParamsError(err error) *RPCError {
	return &RPCError{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)}
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
	if value == nil {
		return nil
	}
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil
	}
	out := make(map[string]any, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		out[iter.Key().String()] = iter.Value().Interface()
	}
	return out
}
