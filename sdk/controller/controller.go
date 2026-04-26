package controller

import (
	"context"
	"iter"
	"strings"
	"time"

	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

// ApprovalOption is one controller-side approval choice surfaced by a remote
// ACP controller.
type ApprovalOption struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Kind string `json:"kind,omitempty"`
}

// ApprovalToolCall describes the remote tool invocation asking for approval.
type ApprovalToolCall struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
}

// ApprovalRequest is the runtime-owned approval bridge payload used by remote
// ACP controllers. It is system-controlled and never exposed to the model.
type ApprovalRequest struct {
	SessionRef sdksession.SessionRef `json:"session_ref,omitempty"`
	Session    sdksession.Session    `json:"session,omitempty"`
	Agent      string                `json:"agent,omitempty"`
	Mode       string                `json:"mode,omitempty"`
	ToolCall   ApprovalToolCall      `json:"tool_call,omitempty"`
	Options    []ApprovalOption      `json:"options,omitempty"`
}

// ApprovalResponse is one bridged controller approval outcome.
type ApprovalResponse struct {
	Outcome  string `json:"outcome,omitempty"`
	OptionID string `json:"option_id,omitempty"`
	Approved bool   `json:"approved,omitempty"`
}

// ApprovalRequester bridges a remote controller approval request into the
// parent runtime's approval surface.
type ApprovalRequester interface {
	RequestControllerApproval(context.Context, ApprovalRequest) (ApprovalResponse, error)
}

// AttachRequest creates one ACP-backed participant attachment.
type AttachRequest struct {
	SessionRef sdksession.SessionRef      `json:"session_ref,omitempty"`
	Session    sdksession.Session         `json:"session,omitempty"`
	Agent      string                     `json:"agent,omitempty"`
	Role       sdksession.ParticipantRole `json:"role,omitempty"`
	Source     string                     `json:"source,omitempty"`
	Label      string                     `json:"label,omitempty"`
}

// DetachRequest removes one ACP-backed participant attachment.
type DetachRequest struct {
	SessionRef    sdksession.SessionRef `json:"session_ref,omitempty"`
	Session       sdksession.Session    `json:"session,omitempty"`
	ParticipantID string                `json:"participant_id,omitempty"`
	Source        string                `json:"source,omitempty"`
}

// HandoffRequest activates one ACP controller for a session.
type HandoffRequest struct {
	SessionRef sdksession.SessionRef `json:"session_ref,omitempty"`
	Session    sdksession.Session    `json:"session,omitempty"`
	Agent      string                `json:"agent,omitempty"`
	Source     string                `json:"source,omitempty"`
	Reason     string                `json:"reason,omitempty"`
}

// TurnRequest runs one turn through the active ACP controller.
type TurnRequest struct {
	SessionRef        sdksession.SessionRef  `json:"session_ref,omitempty"`
	Session           sdksession.Session     `json:"session,omitempty"`
	TurnID            string                 `json:"turn_id,omitempty"`
	Input             string                 `json:"input,omitempty"`
	ContentParts      []sdkmodel.ContentPart `json:"content_parts,omitempty"`
	Stream            bool                   `json:"stream,omitempty"`
	Mode              string                 `json:"mode,omitempty"`
	ApprovalRequester ApprovalRequester      `json:"-"`
}

// ParticipantPromptRequest sends one bounded prompt to an attached ACP
// participant without changing the main controller.
type ParticipantPromptRequest struct {
	SessionRef    sdksession.SessionRef  `json:"session_ref,omitempty"`
	Session       sdksession.Session     `json:"session,omitempty"`
	ParticipantID string                 `json:"participant_id,omitempty"`
	Input         string                 `json:"input,omitempty"`
	ContentParts  []sdkmodel.ContentPart `json:"content_parts,omitempty"`
}

type TurnHandle interface {
	Events() iter.Seq2[*sdksession.Event, error]
	Cancel() bool
	Close() error
}

// TurnResult is one normalized ACP-controller turn result.
type TurnResult struct {
	Handle    TurnHandle `json:"-"`
	UpdatedAt time.Time  `json:"updated_at,omitempty"`
}

// Backend is the runtime-facing control-plane contract for ACP-backed main
// controllers and sidecar participants.
type Backend interface {
	Activate(context.Context, HandoffRequest) (sdksession.ControllerBinding, error)
	Deactivate(context.Context, sdksession.SessionRef) error
	RunTurn(context.Context, TurnRequest) (TurnResult, error)
	Attach(context.Context, AttachRequest) (sdksession.ParticipantBinding, error)
	PromptParticipant(context.Context, ParticipantPromptRequest) (TurnResult, error)
	Detach(context.Context, DetachRequest) error
}

func NormalizeAttachRequest(in AttachRequest) AttachRequest {
	out := in
	out.SessionRef = sdksession.NormalizeSessionRef(in.SessionRef)
	out.Session = sdksession.CloneSession(in.Session)
	out.Agent = strings.TrimSpace(in.Agent)
	out.Source = strings.TrimSpace(in.Source)
	out.Label = strings.TrimSpace(in.Label)
	return out
}

func NormalizeDetachRequest(in DetachRequest) DetachRequest {
	out := in
	out.SessionRef = sdksession.NormalizeSessionRef(in.SessionRef)
	out.Session = sdksession.CloneSession(in.Session)
	out.ParticipantID = strings.TrimSpace(in.ParticipantID)
	out.Source = strings.TrimSpace(in.Source)
	return out
}

func NormalizeHandoffRequest(in HandoffRequest) HandoffRequest {
	out := in
	out.SessionRef = sdksession.NormalizeSessionRef(in.SessionRef)
	out.Session = sdksession.CloneSession(in.Session)
	out.Agent = strings.TrimSpace(in.Agent)
	out.Source = strings.TrimSpace(in.Source)
	out.Reason = strings.TrimSpace(in.Reason)
	return out
}

func NormalizeTurnRequest(in TurnRequest) TurnRequest {
	out := in
	out.SessionRef = sdksession.NormalizeSessionRef(in.SessionRef)
	out.Session = sdksession.CloneSession(in.Session)
	out.TurnID = strings.TrimSpace(in.TurnID)
	out.Input = strings.TrimSpace(in.Input)
	if len(in.ContentParts) > 0 {
		out.ContentParts = append([]sdkmodel.ContentPart(nil), in.ContentParts...)
	}
	out.Mode = strings.TrimSpace(in.Mode)
	return out
}

func NormalizeParticipantPromptRequest(in ParticipantPromptRequest) ParticipantPromptRequest {
	out := in
	out.SessionRef = sdksession.NormalizeSessionRef(in.SessionRef)
	out.Session = sdksession.CloneSession(in.Session)
	out.ParticipantID = strings.TrimSpace(in.ParticipantID)
	out.Input = strings.TrimSpace(in.Input)
	out.ContentParts = append([]sdkmodel.ContentPart(nil), in.ContentParts...)
	return out
}
