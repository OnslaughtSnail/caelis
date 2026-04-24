package gateway

import gatewaycore "github.com/OnslaughtSnail/caelis/gateway/core"

type BeginTurnRequest = gatewaycore.BeginTurnRequest
type TurnIntent = gatewaycore.TurnIntent
type StartSessionRequest = gatewaycore.StartSessionRequest
type LoadSessionRequest = gatewaycore.LoadSessionRequest
type ForkSessionRequest = gatewaycore.ForkSessionRequest
type ResumeSessionRequest = gatewaycore.ResumeSessionRequest
type ListSessionsRequest = gatewaycore.ListSessionsRequest
type InterruptRequest = gatewaycore.InterruptRequest
type BindingDescriptor = gatewaycore.BindingDescriptor
type BindSessionRequest = gatewaycore.BindSessionRequest
type ReplayEventsRequest = gatewaycore.ReplayEventsRequest
type HandoffControllerRequest = gatewaycore.HandoffControllerRequest
type AttachParticipantRequest = gatewaycore.AttachParticipantRequest
type DetachParticipantRequest = gatewaycore.DetachParticipantRequest
type ControlPlaneStateRequest = gatewaycore.ControlPlaneStateRequest
type BindingStateRequest = gatewaycore.BindingStateRequest
type ActiveTurnState = gatewaycore.ActiveTurnState
type ControllerState = gatewaycore.ControllerState
type ParticipantState = gatewaycore.ParticipantState
type ACPProjectionState = gatewaycore.ACPProjectionState
type ContinuityState = gatewaycore.ContinuityState
type ControlPlaneState = gatewaycore.ControlPlaneState
type BindingState = gatewaycore.BindingState
type ReplayEventsResult = gatewaycore.ReplayEventsResult
type ResolvedTurn = gatewaycore.ResolvedTurn
type TurnResolver = gatewaycore.TurnResolver
type RequestPolicy = gatewaycore.RequestPolicy
type EventKind = gatewaycore.EventKind
type UsageSnapshot = gatewaycore.UsageSnapshot
type NarrativeRole = gatewaycore.NarrativeRole
type EventScope = gatewaycore.EventScope
type NarrativePayload = gatewaycore.NarrativePayload
type ToolStatus = gatewaycore.ToolStatus
type ToolCallPayload = gatewaycore.ToolCallPayload
type ToolResultPayload = gatewaycore.ToolResultPayload
type PlanEntryPayload = gatewaycore.PlanEntryPayload
type PlanPayload = gatewaycore.PlanPayload
type ApprovalStatus = gatewaycore.ApprovalStatus
type ApprovalOption = gatewaycore.ApprovalOption
type ApprovalPayload = gatewaycore.ApprovalPayload
type ParticipantAction = gatewaycore.ParticipantAction
type ParticipantPayload = gatewaycore.ParticipantPayload
type LifecycleStatus = gatewaycore.LifecycleStatus
type LifecyclePayload = gatewaycore.LifecyclePayload
type EventOrigin = gatewaycore.EventOrigin
type Event = gatewaycore.Event
type EventEnvelope = gatewaycore.EventEnvelope
type SubmissionKind = gatewaycore.SubmissionKind
type ApprovalDecision = gatewaycore.ApprovalDecision
type SubmitRequest = gatewaycore.SubmitRequest
type BeginTurnResult = gatewaycore.BeginTurnResult
type TurnHandle = gatewaycore.TurnHandle
type ErrorKind = gatewaycore.ErrorKind
type Error = gatewaycore.Error
type ModelLookup = gatewaycore.ModelLookup
type ModelResolution = gatewaycore.ModelResolution

const (
	StateCurrentModelAlias  = gatewaycore.StateCurrentModelAlias
	StateCurrentSandboxMode = gatewaycore.StateCurrentSandboxMode
	StateCurrentSessionMode = gatewaycore.StateCurrentSessionMode
)

const (
	NarrativeRoleUser      = gatewaycore.NarrativeRoleUser
	NarrativeRoleAssistant = gatewaycore.NarrativeRoleAssistant
	NarrativeRoleReasoning = gatewaycore.NarrativeRoleReasoning
	NarrativeRoleSystem    = gatewaycore.NarrativeRoleSystem
	NarrativeRoleNotice    = gatewaycore.NarrativeRoleNotice
)

const (
	EventScopeMain        = gatewaycore.EventScopeMain
	EventScopeParticipant = gatewaycore.EventScopeParticipant
	EventScopeSubagent    = gatewaycore.EventScopeSubagent
)

const (
	ToolStatusStarted   = gatewaycore.ToolStatusStarted
	ToolStatusRunning   = gatewaycore.ToolStatusRunning
	ToolStatusCompleted = gatewaycore.ToolStatusCompleted
	ToolStatusFailed    = gatewaycore.ToolStatusFailed
)

const (
	EventKindSessionEvent      = gatewaycore.EventKindSessionEvent
	EventKindUserMessage       = gatewaycore.EventKindUserMessage
	EventKindAssistantMessage  = gatewaycore.EventKindAssistantMessage
	EventKindPlanUpdate        = gatewaycore.EventKindPlanUpdate
	EventKindToolCall          = gatewaycore.EventKindToolCall
	EventKindToolResult        = gatewaycore.EventKindToolResult
	EventKindParticipant       = gatewaycore.EventKindParticipant
	EventKindHandoff           = gatewaycore.EventKindHandoff
	EventKindCompact           = gatewaycore.EventKindCompact
	EventKindNotice            = gatewaycore.EventKindNotice
	EventKindSessionLifecycle  = gatewaycore.EventKindSessionLifecycle
	EventKindSystemMessage     = gatewaycore.EventKindSystemMessage
	EventKindApprovalRequested = gatewaycore.EventKindApprovalRequested
	EventKindLifecycle         = gatewaycore.EventKindLifecycle
)

const (
	ApprovalStatusPending  = gatewaycore.ApprovalStatusPending
	ApprovalStatusApproved = gatewaycore.ApprovalStatusApproved
	ApprovalStatusRejected = gatewaycore.ApprovalStatusRejected
	ApprovalStatusSelected = gatewaycore.ApprovalStatusSelected
)

const (
	ParticipantActionAttached = gatewaycore.ParticipantActionAttached
	ParticipantActionDetached = gatewaycore.ParticipantActionDetached
)

const (
	LifecycleStatusRunning         = gatewaycore.LifecycleStatusRunning
	LifecycleStatusWaitingApproval = gatewaycore.LifecycleStatusWaitingApproval
	LifecycleStatusInterrupted     = gatewaycore.LifecycleStatusInterrupted
	LifecycleStatusFailed          = gatewaycore.LifecycleStatusFailed
	LifecycleStatusCompleted       = gatewaycore.LifecycleStatusCompleted
)

const (
	SubmissionKindConversation = gatewaycore.SubmissionKindConversation
	SubmissionKindOverlay      = gatewaycore.SubmissionKindOverlay
	SubmissionKindApproval     = gatewaycore.SubmissionKindApproval
)

const (
	KindValidation  = gatewaycore.KindValidation
	KindConflict    = gatewaycore.KindConflict
	KindNotFound    = gatewaycore.KindNotFound
	KindInternal    = gatewaycore.KindInternal
	KindApproval    = gatewaycore.KindApproval
	KindUnsupported = gatewaycore.KindUnsupported
)

const (
	CodeNotImplemented          = gatewaycore.CodeNotImplemented
	CodeActiveRunConflict       = gatewaycore.CodeActiveRunConflict
	CodeInvalidRequest          = gatewaycore.CodeInvalidRequest
	CodeCursorNotFound          = gatewaycore.CodeCursorNotFound
	CodeSubmissionUnsupported   = gatewaycore.CodeSubmissionUnsupported
	CodeApprovalNotPending      = gatewaycore.CodeApprovalNotPending
	CodeSessionNotFound         = gatewaycore.CodeSessionNotFound
	CodeSessionAmbiguous        = gatewaycore.CodeSessionAmbiguous
	CodeBindingNotFound         = gatewaycore.CodeBindingNotFound
	CodeNoResumableSession      = gatewaycore.CodeNoResumableSession
	CodeNoActiveRun             = gatewaycore.CodeNoActiveRun
	CodeModeNotFound            = gatewaycore.CodeModeNotFound
	CodeControlPlaneUnsupported = gatewaycore.CodeControlPlaneUnsupported
)

func AssistantText(event Event) string { return gatewaycore.AssistantText(event) }
func PromptTokens(event Event) int     { return gatewaycore.PromptTokens(event) }
func As(err error, target any) bool    { return gatewaycore.As(err, target) }
func CurrentModelAlias(state map[string]any) string {
	return gatewaycore.CurrentModelAlias(state)
}
func CurrentSandboxMode(state map[string]any) string {
	return gatewaycore.CurrentSandboxMode(state)
}
func CurrentSessionMode(state map[string]any) string {
	return gatewaycore.CurrentSessionMode(state)
}
