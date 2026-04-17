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

type HandoffControllerRequest struct {
	SessionRef sdksession.SessionRef
	BindingKey string
	Kind       sdksession.ControllerKind
	Agent      string
	Source     string
	Reason     string
}

type ControlPlaneStateRequest struct {
	SessionRef sdksession.SessionRef
	BindingKey string
}

type ControllerState struct {
	Kind         sdksession.ControllerKind `json:"kind,omitempty"`
	ControllerID string                    `json:"controller_id,omitempty"`
	Label        string                    `json:"label,omitempty"`
	EpochID      string                    `json:"epoch_id,omitempty"`
	AttachedAt   time.Time                 `json:"attached_at,omitempty"`
	Source       string                    `json:"source,omitempty"`
}

type ParticipantState struct {
	ID            string                     `json:"id,omitempty"`
	Kind          sdksession.ParticipantKind `json:"kind,omitempty"`
	Role          sdksession.ParticipantRole `json:"role,omitempty"`
	Label         string                     `json:"label,omitempty"`
	SessionID     string                     `json:"session_id,omitempty"`
	Source        string                     `json:"source,omitempty"`
	ParentTurnID  string                     `json:"parent_turn_id,omitempty"`
	DelegationID  string                     `json:"delegation_id,omitempty"`
	AttachedAt    time.Time                  `json:"attached_at,omitempty"`
	ControllerRef string                     `json:"controller_ref,omitempty"`
}

type ControlPlaneState struct {
	SessionRef    sdksession.SessionRef `json:"session_ref"`
	Controller    ControllerState       `json:"controller"`
	Participants  []ParticipantState    `json:"participants,omitempty"`
	RunState      sdkruntime.RunState   `json:"run_state,omitempty"`
	HasActiveTurn bool                  `json:"has_active_turn,omitempty"`
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
	EventKindUserMessage       EventKind = "user_message"
	EventKindAssistantMessage  EventKind = "assistant_message"
	EventKindPlanUpdate        EventKind = "plan_update"
	EventKindToolCall          EventKind = "tool_call"
	EventKindToolResult        EventKind = "tool_result"
	EventKindParticipant       EventKind = "participant"
	EventKindHandoff           EventKind = "handoff"
	EventKindCompact           EventKind = "compact"
	EventKindNotice            EventKind = "notice"
	EventKindSessionLifecycle  EventKind = "session_lifecycle"
	EventKindSystemMessage     EventKind = "system_message"
	EventKindApprovalRequested EventKind = "approval_requested"
	EventKindLifecycle         EventKind = "lifecycle"
)

type UsageSnapshot struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type Event struct {
	Kind         EventKind
	HandleID     string
	RunID        string
	TurnID       string
	SessionRef   sdksession.SessionRef
	SessionEvent *sdksession.Event
	Usage        *UsageSnapshot
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
