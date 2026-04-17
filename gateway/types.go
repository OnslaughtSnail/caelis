package gateway

import (
	"context"
	"time"

	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

type BeginTurnRequest struct {
	SessionRef   sdksession.SessionRef
	Input        string
	ContentParts []sdkmodel.ContentPart
	ModeName     string
	ModelHint    string
	Surface      string
	Metadata     map[string]any
}

type TurnIntent = BeginTurnRequest

type StartSessionRequest struct {
	AppName            string
	UserID             string
	Workspace          sdksession.WorkspaceRef
	PreferredSessionID string
	Title              string
	Metadata           map[string]any
	BindingKey         string
}

type LoadSessionRequest struct {
	SessionRef       sdksession.SessionRef
	Limit            int
	IncludeTransient bool
	BindingKey       string
}

type ForkSessionRequest struct {
	SourceSessionRef   sdksession.SessionRef
	PreferredSessionID string
	Title              string
	Metadata           map[string]any
	BindingKey         string
}

type ResumeSessionRequest struct {
	AppName          string
	UserID           string
	Workspace        sdksession.WorkspaceRef
	SessionID        string
	ExcludeSessionID string
	Limit            int
	IncludeTransient bool
	BindingKey       string
}

type ListSessionsRequest struct {
	AppName      string
	UserID       string
	WorkspaceKey string
	Cursor       string
	Limit        int
}

type InterruptRequest struct {
	SessionRef sdksession.SessionRef
	BindingKey string
	Reason     string
}

type ResolvedTurn struct {
	RunRequest sdkruntime.RunRequest
}

type TurnResolver interface {
	ResolveTurn(context.Context, TurnIntent) (ResolvedTurn, error)
}

type EventKind string

const (
	EventKindSessionEvent      EventKind = "session_event"
	EventKindApprovalRequested EventKind = "approval_requested"
	EventKindLifecycle         EventKind = "lifecycle"
)

type Event struct {
	Kind         EventKind
	HandleID     string
	RunID        string
	TurnID       string
	SessionRef   sdksession.SessionRef
	SessionEvent *sdksession.Event
	Approval     *sdkruntime.ApprovalRequest
}

type EventEnvelope struct {
	Cursor string
	Event  Event
	Err    error
}

type SubmissionKind string

const (
	SubmissionKindConversation SubmissionKind = "conversation"
	SubmissionKindOverlay      SubmissionKind = "overlay"
	SubmissionKindApproval     SubmissionKind = "approval"
)

type ApprovalDecision struct {
	Outcome  string
	OptionID string
	Approved bool
}

type SubmitRequest struct {
	Kind     SubmissionKind
	Text     string
	Metadata map[string]any
	Approval *ApprovalDecision
}

type BeginTurnResult struct {
	Session sdksession.Session
	Handle  TurnHandle
}

type TurnHandle interface {
	HandleID() string
	RunID() string
	TurnID() string
	SessionRef() sdksession.SessionRef
	CreatedAt() time.Time
	Events() <-chan EventEnvelope
	EventsAfter(string) ([]EventEnvelope, string, error)
	Submit(context.Context, SubmitRequest) error
	Cancel() bool
	Close() error
}
