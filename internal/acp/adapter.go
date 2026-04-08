package acp

import (
	"context"
	"iter"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
)

type AdapterCapabilities struct {
	PromptImage bool
	SessionList bool
}

type AdapterNewSessionRequest = NewSessionRequest
type AdapterLoadSessionRequest = LoadSessionRequest
type AdapterSetModeRequest = SetSessionModeRequest
type AdapterSetConfigOptionRequest = SetSessionConfigOptionRequest

type AdapterSessionState struct {
	SessionID         string
	CWD               string
	ConfigOptions     []SessionConfigOption
	Modes             *SessionModeState
	AvailableCommands []AvailableCommand
	PlanEntries       []PlanEntry
}

type LoadedSessionState struct {
	Session AdapterSessionState
	Events  []*session.Event
}

type StartPromptRequest struct {
	SessionID       string
	InputText       string
	ContentParts    []model.ContentPart
	HasImages       bool
	Meta            map[string]any
	OnSessionStream func(sessionstream.Update) error
}

type StartPromptResult struct {
	StopReason string
	Handle     PromptHandle
}

type PromptHandle interface {
	Events() iter.Seq2[*session.Event, error]
	Close() error
}

type Adapter interface {
	Capabilities() AdapterCapabilities
	NewSession(context.Context, AdapterNewSessionRequest, ClientCapabilities) (AdapterSessionState, error)
	ListSessions(context.Context, SessionListRequest) (SessionListResponse, error)
	LoadSession(context.Context, AdapterLoadSessionRequest, ClientCapabilities) (LoadedSessionState, error)
	SetMode(context.Context, AdapterSetModeRequest) (AdapterSessionState, error)
	SetConfigOption(context.Context, AdapterSetConfigOptionRequest) (AdapterSessionState, error)
	StartPrompt(context.Context, StartPromptRequest) (StartPromptResult, error)
	CancelPrompt(string)
	SessionFS(string) toolexec.FileSystem
}
