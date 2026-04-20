package core

import (
	"context"
	"strings"
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
	Request      sdkruntime.ModelRequestOptions
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
	Binding            BindingDescriptor
}

type LoadSessionRequest struct {
	SessionRef       sdksession.SessionRef
	Limit            int
	IncludeTransient bool
	BindingKey       string
	Binding          BindingDescriptor
}

type ForkSessionRequest struct {
	SourceSessionRef   sdksession.SessionRef
	PreferredSessionID string
	Title              string
	Metadata           map[string]any
	BindingKey         string
	Binding            BindingDescriptor
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
	Binding          BindingDescriptor
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

type BindingDescriptor struct {
	Surface   string    `json:"surface,omitempty"`
	ActorKind string    `json:"actor_kind,omitempty"`
	ActorID   string    `json:"actor_id,omitempty"`
	Owner     string    `json:"owner,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type BindSessionRequest struct {
	SessionRef sdksession.SessionRef `json:"session_ref"`
	BindingKey string                `json:"binding_key,omitempty"`
	Binding    BindingDescriptor     `json:"binding,omitempty"`
}

type ReplayEventsRequest struct {
	SessionRef       sdksession.SessionRef `json:"session_ref"`
	BindingKey       string                `json:"binding_key,omitempty"`
	Cursor           string                `json:"cursor,omitempty"`
	Limit            int                   `json:"limit,omitempty"`
	IncludeTransient bool                  `json:"include_transient,omitempty"`
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

type BindingStateRequest struct {
	BindingKey string `json:"binding_key,omitempty"`
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

type ACPProjectionState struct {
	Cursor    string `json:"cursor,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	EventType string `json:"event_type,omitempty"`
}

type ContinuityState struct {
	LastEventCursor    string             `json:"last_event_cursor,omitempty"`
	ControllerCursor   string             `json:"controller_cursor,omitempty"`
	ParticipantCursors map[string]string  `json:"participant_cursors,omitempty"`
	ACPProjection      ACPProjectionState `json:"acp_projection,omitempty"`
}

type ControlPlaneState struct {
	SessionRef    sdksession.SessionRef `json:"session_ref"`
	Controller    ControllerState       `json:"controller"`
	Participants  []ParticipantState    `json:"participants,omitempty"`
	Continuity    ContinuityState       `json:"continuity,omitempty"`
	RunState      sdkruntime.RunState   `json:"run_state,omitempty"`
	HasActiveTurn bool                  `json:"has_active_turn,omitempty"`
}

type BindingState struct {
	BindingKey      string                `json:"binding_key,omitempty"`
	SessionRef      sdksession.SessionRef `json:"session_ref"`
	Surface         string                `json:"surface,omitempty"`
	ActorKind       string                `json:"actor_kind,omitempty"`
	ActorID         string                `json:"actor_id,omitempty"`
	Owner           string                `json:"owner,omitempty"`
	BoundAt         time.Time             `json:"bound_at,omitempty"`
	UpdatedAt       time.Time             `json:"updated_at,omitempty"`
	ExpiresAt       time.Time             `json:"expires_at,omitempty"`
	LastHandleID    string                `json:"last_handle_id,omitempty"`
	LastRunID       string                `json:"last_run_id,omitempty"`
	LastTurnID      string                `json:"last_turn_id,omitempty"`
	LastEventCursor string                `json:"last_event_cursor,omitempty"`
	HasActiveTurn   bool                  `json:"has_active_turn,omitempty"`
}

type ReplayEventsResult struct {
	SessionRef    sdksession.SessionRef `json:"session_ref"`
	Events        []EventEnvelope       `json:"events,omitempty"`
	NextCursor    string                `json:"next_cursor,omitempty"`
	Durable       bool                  `json:"durable,omitempty"`
	HasLiveHandle bool                  `json:"has_live_handle,omitempty"`
	ControlPlane  ControlPlaneState     `json:"control_plane"`
}

type ResolvedTurn struct {
	RunRequest sdkruntime.RunRequest
}

type TurnResolver interface {
	ResolveTurn(context.Context, TurnIntent) (ResolvedTurn, error)
}

type RequestPolicy interface {
	ResolveTurnRequest(BeginTurnRequest) sdkruntime.ModelRequestOptions
}

type SurfaceClass string

const (
	SurfaceClassInteractive SurfaceClass = "interactive"
	SurfaceClassBatch       SurfaceClass = "batch"
)

func ClassifySurface(surface string) SurfaceClass {
	normalized := strings.ToLower(strings.TrimSpace(surface))
	switch {
	case normalized == "":
		return SurfaceClassInteractive
	case strings.HasPrefix(normalized, "headless"),
		strings.HasPrefix(normalized, "batch"),
		strings.HasPrefix(normalized, "cron"),
		strings.HasPrefix(normalized, "export"),
		strings.HasPrefix(normalized, "script"):
		return SurfaceClassBatch
	default:
		return SurfaceClassInteractive
	}
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

type NarrativeRole string

const (
	NarrativeRoleUser      NarrativeRole = "user"
	NarrativeRoleAssistant NarrativeRole = "assistant"
	NarrativeRoleReasoning NarrativeRole = "reasoning"
	NarrativeRoleSystem    NarrativeRole = "system"
	NarrativeRoleNotice    NarrativeRole = "notice"
)

type EventScope string

const (
	EventScopeMain        EventScope = "main"
	EventScopeParticipant EventScope = "participant"
	EventScopeSubagent    EventScope = "subagent"
)

type NarrativePayload struct {
	Role          NarrativeRole `json:"role,omitempty"`
	Actor         string        `json:"actor,omitempty"`
	Text          string        `json:"text,omitempty"`
	ReasoningText string        `json:"reasoning_text,omitempty"`
	Final         bool          `json:"final,omitempty"`
	Visibility    string        `json:"visibility,omitempty"`
	UpdateType    string        `json:"update_type,omitempty"`
	Scope         EventScope    `json:"scope,omitempty"`
	ParticipantID string        `json:"participant_id,omitempty"`
}

type ToolCallPayload struct {
	CallID         string     `json:"call_id,omitempty"`
	ToolName       string     `json:"tool_name,omitempty"`
	ArgsText       string     `json:"args_text,omitempty"`
	CommandPreview string     `json:"command_preview,omitempty"`
	Status         string     `json:"status,omitempty"`
	Actor          string     `json:"actor,omitempty"`
	Scope          EventScope `json:"scope,omitempty"`
	ParticipantID  string     `json:"participant_id,omitempty"`
}

type ToolResultPayload struct {
	CallID         string     `json:"call_id,omitempty"`
	ToolName       string     `json:"tool_name,omitempty"`
	OutputText     string     `json:"output_text,omitempty"`
	CommandPreview string     `json:"command_preview,omitempty"`
	Status         string     `json:"status,omitempty"`
	Error          bool       `json:"error,omitempty"`
	Actor          string     `json:"actor,omitempty"`
	Scope          EventScope `json:"scope,omitempty"`
	ParticipantID  string     `json:"participant_id,omitempty"`
}

type PlanEntryPayload struct {
	Content  string `json:"content,omitempty"`
	Status   string `json:"status,omitempty"`
	Priority string `json:"priority,omitempty"`
}

type PlanPayload struct {
	Entries []PlanEntryPayload `json:"entries,omitempty"`
}

type ApprovalOption struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Kind string `json:"kind,omitempty"`
}

type ApprovalPayload struct {
	ToolName       string           `json:"tool_name,omitempty"`
	CommandPreview string           `json:"command_preview,omitempty"`
	Options        []ApprovalOption `json:"options,omitempty"`
}

type ParticipantPayload struct {
	ParticipantID   string     `json:"participant_id,omitempty"`
	ParticipantKind string     `json:"participant_kind,omitempty"`
	Role            string     `json:"role,omitempty"`
	Label           string     `json:"label,omitempty"`
	Action          string     `json:"action,omitempty"`
	SessionID       string     `json:"session_id,omitempty"`
	ParentTurnID    string     `json:"parent_turn_id,omitempty"`
	DelegationID    string     `json:"delegation_id,omitempty"`
	Actor           string     `json:"actor,omitempty"`
	Scope           EventScope `json:"scope,omitempty"`
}

type LifecyclePayload struct {
	Status        string     `json:"status,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	Actor         string     `json:"actor,omitempty"`
	Scope         EventScope `json:"scope,omitempty"`
	ParticipantID string     `json:"participant_id,omitempty"`
}

type EventOrigin struct {
	Scope                EventScope `json:"scope,omitempty"`
	ScopeID              string     `json:"scope_id,omitempty"`
	Actor                string     `json:"actor,omitempty"`
	ParticipantID        string     `json:"participant_id,omitempty"`
	ParticipantKind      string     `json:"participant_kind,omitempty"`
	ParticipantSessionID string     `json:"participant_session_id,omitempty"`
}

type Event struct {
	Kind            EventKind
	HandleID        string
	RunID           string
	TurnID          string
	OccurredAt      time.Time
	SessionRef      sdksession.SessionRef
	Origin          *EventOrigin
	SessionEvent    *sdksession.Event
	Usage           *UsageSnapshot
	Approval        *sdkruntime.ApprovalRequest
	Narrative       *NarrativePayload
	ToolCall        *ToolCallPayload
	ToolResult      *ToolResultPayload
	Plan            *PlanPayload
	ApprovalPayload *ApprovalPayload
	Participant     *ParticipantPayload
	Lifecycle       *LifecyclePayload
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
