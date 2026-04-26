package local

import (
	"context"
	"fmt"
	"strings"
	"time"

	sdkcontroller "github.com/OnslaughtSnail/caelis/sdk/controller"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sdktool "github.com/OnslaughtSnail/caelis/sdk/tool"
)

func (r *Runtime) Controllers() sdkcontroller.ACP {
	if r == nil {
		return nil
	}
	return r.controllers
}

func (r *Runtime) ensureSessionController(ctx context.Context, session sdksession.Session) (sdksession.Session, error) {
	if r == nil || r.sessions == nil {
		return sdksession.Session{}, fmt.Errorf("sdk/runtime/local: session service is unavailable")
	}
	if session.Controller.Kind != "" {
		return sdksession.CloneSession(session), nil
	}
	return r.sessions.BindController(ctx, sdksession.BindControllerRequest{
		SessionRef: session.SessionRef,
		Binding:    r.kernelControllerBinding("runtime"),
	})
}

func (r *Runtime) kernelControllerBinding(source string) sdksession.ControllerBinding {
	return sdksession.ControllerBinding{
		Kind:         sdksession.ControllerKindKernel,
		ControllerID: "sdk-kernel",
		Label:        "SDK Kernel",
		EpochID:      r.nextID("kernel", nil),
		AttachedAt:   r.now(),
		Source:       firstNonEmpty(strings.TrimSpace(source), "runtime"),
	}
}

func (r *Runtime) runACPControllerTurn(
	ctx context.Context,
	session sdksession.Session,
	ref sdksession.SessionRef,
	req sdkruntime.RunRequest,
) (sdkruntime.RunResult, error) {
	if r == nil || r.controllers == nil {
		return sdkruntime.RunResult{}, fmt.Errorf("sdk/runtime/local: ACP controller backend is not configured")
	}
	runID := r.nextID("run", r.runIDGenerator)
	turnID := r.nextID("turn", nil)
	r.setRunState(ref.SessionID, sdkruntime.RunState{
		Status:      sdkruntime.RunLifecycleStatusRunning,
		ActiveRunID: runID,
		UpdatedAt:   r.now(),
	})
	runCtx, cancel := context.WithCancel(ctx)
	handle := newRunner(runID, cancel)
	go r.executeACPControllerTurn(runCtx, session, ref, req, runID, turnID, handle)
	return sdkruntime.RunResult{
		Session: session,
		Handle:  handle,
	}, nil
}

func (r *Runtime) executeACPControllerTurn(
	ctx context.Context,
	session sdksession.Session,
	ref sdksession.SessionRef,
	req sdkruntime.RunRequest,
	runID string,
	turnID string,
	handle *runner,
) {
	defer handle.finish()

	userEvent := buildUserEvent(session, turnID, req.Input, req.ContentParts)
	if userEvent != nil {
		persisted, err := r.sessions.AppendEvent(ctx, sdksession.AppendEventRequest{
			SessionRef: ref,
			Event:      userEvent,
		})
		if err != nil {
			r.setRunState(ref.SessionID, sdkruntime.RunState{
				Status:      interruptedOrFailedStatus(ctx, err),
				ActiveRunID: runID,
				LastError:   err.Error(),
				UpdatedAt:   r.now(),
			})
			handle.publishError(err)
			return
		}
		handle.publishEvent(persisted)
	}

	turnResult, err := r.controllers.RunTurn(ctx, sdkcontroller.TurnRequest{
		SessionRef:        ref,
		Session:           session,
		TurnID:            turnID,
		Input:             req.Input,
		ContentParts:      req.ContentParts,
		Stream:            req.Request.StreamEnabled(false),
		Mode:              r.policyMode(req.AgentSpec),
		ApprovalRequester: controllerApprovalRequester{requester: req.ApprovalRequester, sessionRef: ref, session: session, runID: runID, turnID: turnID},
	})
	if err != nil {
		r.setRunState(ref.SessionID, sdkruntime.RunState{
			Status:      interruptedOrFailedStatus(ctx, err),
			ActiveRunID: runID,
			LastError:   err.Error(),
			UpdatedAt:   r.now(),
		})
		handle.publishError(err)
		return
	}
	if turnResult.Handle != nil {
		for event, seqErr := range turnResult.Handle.Events() {
			if seqErr != nil {
				r.setRunState(ref.SessionID, sdkruntime.RunState{
					Status:      interruptedOrFailedStatus(ctx, seqErr),
					ActiveRunID: runID,
					LastError:   seqErr.Error(),
					UpdatedAt:   r.now(),
				})
				handle.publishError(seqErr)
				return
			}
			normalized := normalizeEvent(session, turnID, event)
			if normalized == nil {
				continue
			}
			if sdksession.IsCanonicalHistoryEvent(normalized) {
				persisted, appendErr := r.sessions.AppendEvent(ctx, sdksession.AppendEventRequest{
					SessionRef: ref,
					Event:      normalized,
				})
				if appendErr != nil {
					r.setRunState(ref.SessionID, sdkruntime.RunState{
						Status:      interruptedOrFailedStatus(ctx, appendErr),
						ActiveRunID: runID,
						LastError:   appendErr.Error(),
						UpdatedAt:   r.now(),
					})
					handle.publishError(appendErr)
					return
				}
				normalized = persisted
			}
			if err := r.handleControllerPlanEvent(ctx, ref, normalized); err != nil {
				r.setRunState(ref.SessionID, sdkruntime.RunState{
					Status:      interruptedOrFailedStatus(ctx, err),
					ActiveRunID: runID,
					LastError:   err.Error(),
					UpdatedAt:   r.now(),
				})
				handle.publishError(err)
				return
			}
			handle.publishEvent(normalized)
		}
	}
	r.setRunState(ref.SessionID, sdkruntime.RunState{
		Status:      sdkruntime.RunLifecycleStatusCompleted,
		ActiveRunID: runID,
		UpdatedAt:   r.now(),
	})
}

func (r *Runtime) handleControllerPlanEvent(ctx context.Context, ref sdksession.SessionRef, event *sdksession.Event) error {
	if r == nil || r.sessions == nil || event == nil || event.Protocol == nil || event.Protocol.Plan == nil {
		return nil
	}
	entries := event.Protocol.Plan.Entries
	if len(entries) == 0 {
		return nil
	}
	return r.sessions.UpdateState(ctx, ref, func(state map[string]any) (map[string]any, error) {
		if state == nil {
			state = map[string]any{}
		}
		out := make([]map[string]any, 0, len(entries))
		for _, item := range entries {
			out = append(out, map[string]any{
				"content": strings.TrimSpace(item.Content),
				"status":  strings.TrimSpace(item.Status),
			})
		}
		state["plan"] = map[string]any{
			"version":     1,
			"entries":     out,
			"explanation": strings.TrimSpace(event.Text),
		}
		return state, nil
	})
}

func (r *Runtime) AttachACPParticipant(ctx context.Context, req sdkruntime.AttachACPParticipantRequest) (sdksession.Session, error) {
	if r == nil || r.controllers == nil {
		return sdksession.Session{}, fmt.Errorf("sdk/runtime/local: ACP controller backend is not configured")
	}
	ref := sdksession.NormalizeSessionRef(req.SessionRef)
	session, err := r.sessions.Session(ctx, ref)
	if err != nil {
		return sdksession.Session{}, err
	}
	session, err = r.ensureSessionController(ctx, session)
	if err != nil {
		return sdksession.Session{}, err
	}
	binding, err := r.controllers.Attach(ctx, sdkcontroller.AttachRequest{
		SessionRef: ref,
		Session:    session,
		Agent:      strings.TrimSpace(req.Agent),
		Role:       req.Role,
		Source:     strings.TrimSpace(req.Source),
		Label:      strings.TrimSpace(req.Label),
	})
	if err != nil {
		return sdksession.Session{}, err
	}
	session, err = r.sessions.PutParticipant(ctx, sdksession.PutParticipantRequest{
		SessionRef: ref,
		Binding:    binding,
	})
	if err != nil {
		return sdksession.Session{}, err
	}
	if _, err := r.sessions.AppendEvent(ctx, sdksession.AppendEventRequest{
		SessionRef: ref,
		Event:      participantLifecycleEvent(session, binding, "attached", r.now()),
	}); err != nil {
		return sdksession.Session{}, err
	}
	return r.sessions.Session(ctx, ref)
}

func (r *Runtime) DetachACPParticipant(ctx context.Context, req sdkruntime.DetachACPParticipantRequest) (sdksession.Session, error) {
	if r == nil || r.controllers == nil {
		return sdksession.Session{}, fmt.Errorf("sdk/runtime/local: ACP controller backend is not configured")
	}
	ref := sdksession.NormalizeSessionRef(req.SessionRef)
	session, err := r.sessions.Session(ctx, ref)
	if err != nil {
		return sdksession.Session{}, err
	}
	session, err = r.ensureSessionController(ctx, session)
	if err != nil {
		return sdksession.Session{}, err
	}
	binding, _ := participantBinding(session, req.ParticipantID)
	if err := r.controllers.Detach(ctx, sdkcontroller.DetachRequest{
		SessionRef:    ref,
		Session:       session,
		ParticipantID: strings.TrimSpace(req.ParticipantID),
		Source:        strings.TrimSpace(req.Source),
	}); err != nil {
		return sdksession.Session{}, err
	}
	session, err = r.sessions.RemoveParticipant(ctx, sdksession.RemoveParticipantRequest{
		SessionRef:    ref,
		ParticipantID: strings.TrimSpace(req.ParticipantID),
	})
	if err != nil {
		return sdksession.Session{}, err
	}
	if binding.ID != "" {
		if _, err := r.sessions.AppendEvent(ctx, sdksession.AppendEventRequest{
			SessionRef: ref,
			Event:      participantLifecycleEvent(session, binding, "detached", r.now()),
		}); err != nil {
			return sdksession.Session{}, err
		}
	}
	return r.sessions.Session(ctx, ref)
}

func (r *Runtime) PromptACPParticipant(ctx context.Context, req sdkruntime.PromptACPParticipantRequest) (sdksession.Session, error) {
	if r == nil || r.controllers == nil {
		return sdksession.Session{}, fmt.Errorf("sdk/runtime/local: ACP controller backend is not configured")
	}
	ref := sdksession.NormalizeSessionRef(req.SessionRef)
	session, err := r.sessions.Session(ctx, ref)
	if err != nil {
		return sdksession.Session{}, err
	}
	session, err = r.ensureSessionController(ctx, session)
	if err != nil {
		return sdksession.Session{}, err
	}
	turnResult, err := r.controllers.PromptParticipant(ctx, sdkcontroller.ParticipantPromptRequest{
		SessionRef:    ref,
		Session:       session,
		ParticipantID: strings.TrimSpace(req.ParticipantID),
		Input:         strings.TrimSpace(req.Input),
		ContentParts:  req.ContentParts,
	})
	if err != nil {
		return sdksession.Session{}, err
	}
	if turnResult.Handle != nil {
		for event, seqErr := range turnResult.Handle.Events() {
			if seqErr != nil {
				return sdksession.Session{}, seqErr
			}
			normalized := normalizeEvent(session, strings.TrimSpace(req.ParticipantID), event)
			if normalized == nil {
				continue
			}
			if sdksession.IsCanonicalHistoryEvent(normalized) {
				if _, err := r.sessions.AppendEvent(ctx, sdksession.AppendEventRequest{
					SessionRef: ref,
					Event:      normalized,
				}); err != nil {
					return sdksession.Session{}, err
				}
			}
		}
	}
	return r.sessions.Session(ctx, ref)
}

func (r *Runtime) HandoffController(ctx context.Context, req sdkruntime.HandoffControllerRequest) (sdksession.Session, error) {
	ref := sdksession.NormalizeSessionRef(req.SessionRef)
	session, err := r.sessions.Session(ctx, ref)
	if err != nil {
		return sdksession.Session{}, err
	}
	session, err = r.ensureSessionController(ctx, session)
	if err != nil {
		return sdksession.Session{}, err
	}
	from := sdksession.CloneControllerBinding(session.Controller)
	kind := req.Kind
	if kind == "" {
		kind = sdksession.ControllerKindKernel
	}
	var to sdksession.ControllerBinding
	switch kind {
	case sdksession.ControllerKindACP:
		if r.controllers == nil {
			return sdksession.Session{}, fmt.Errorf("sdk/runtime/local: ACP controller backend is not configured")
		}
		to, err = r.controllers.Activate(ctx, sdkcontroller.HandoffRequest{
			SessionRef: ref,
			Session:    session,
			Agent:      strings.TrimSpace(req.Agent),
			Source:     strings.TrimSpace(req.Source),
			Reason:     strings.TrimSpace(req.Reason),
		})
		if err != nil {
			return sdksession.Session{}, err
		}
	default:
		if r.controllers != nil && from.Kind == sdksession.ControllerKindACP {
			if err := r.controllers.Deactivate(ctx, ref); err != nil {
				return sdksession.Session{}, err
			}
		}
		to = r.kernelControllerBinding(firstNonEmpty(strings.TrimSpace(req.Source), "handoff"))
	}

	session, err = r.sessions.BindController(ctx, sdksession.BindControllerRequest{
		SessionRef: ref,
		Binding:    to,
	})
	if err != nil {
		return sdksession.Session{}, err
	}
	if _, err := r.sessions.AppendEvent(ctx, sdksession.AppendEventRequest{
		SessionRef: ref,
		Event:      handoffEvent(from, to, strings.TrimSpace(req.Reason), r.now()),
	}); err != nil {
		return sdksession.Session{}, err
	}
	return r.sessions.Session(ctx, ref)
}

func participantBinding(session sdksession.Session, participantID string) (sdksession.ParticipantBinding, bool) {
	participantID = strings.TrimSpace(participantID)
	for _, item := range session.Participants {
		if strings.TrimSpace(item.ID) == participantID {
			return sdksession.CloneParticipantBinding(item), true
		}
	}
	return sdksession.ParticipantBinding{}, false
}

func participantLifecycleEvent(session sdksession.Session, binding sdksession.ParticipantBinding, action string, now time.Time) *sdksession.Event {
	text := strings.TrimSpace(action + " participant " + firstNonEmpty(binding.Label, binding.ID))
	return &sdksession.Event{
		Type:       sdksession.EventTypeParticipant,
		Visibility: sdksession.VisibilityCanonical,
		Time:       now,
		Actor:      sdksession.ActorRef{Kind: sdksession.ActorKindSystem, Name: "runtime"},
		Text:       text,
		Protocol: &sdksession.EventProtocol{
			Participant: &sdksession.ProtocolParticipant{Action: strings.TrimSpace(action)},
		},
		Scope: &sdksession.EventScope{
			Source: "control_plane",
			Controller: sdksession.ControllerRef{
				Kind:    session.Controller.Kind,
				ID:      session.Controller.ControllerID,
				EpochID: session.Controller.EpochID,
			},
			Participant: sdksession.ParticipantRef{
				ID:           binding.ID,
				Kind:         binding.Kind,
				Role:         binding.Role,
				DelegationID: binding.DelegationID,
			},
			ACP: sdksession.ACPRef{
				SessionID: strings.TrimSpace(binding.SessionID),
			},
		},
		Meta: map[string]any{
			"participant_id": binding.ID,
			"label":          binding.Label,
			"session_id":     binding.SessionID,
			"controller_ref": binding.ControllerRef,
		},
	}
}

func handoffEvent(from sdksession.ControllerBinding, to sdksession.ControllerBinding, reason string, now time.Time) *sdksession.Event {
	text := "handoff to " + firstNonEmpty(to.Label, to.ControllerID)
	meta := map[string]any{
		"from": map[string]any{
			"kind": from.Kind,
			"id":   strings.TrimSpace(from.ControllerID),
		},
		"to": map[string]any{
			"kind": to.Kind,
			"id":   strings.TrimSpace(to.ControllerID),
		},
	}
	if strings.TrimSpace(reason) != "" {
		meta["reason"] = strings.TrimSpace(reason)
	}
	return &sdksession.Event{
		Type:       sdksession.EventTypeHandoff,
		Visibility: sdksession.VisibilityCanonical,
		Time:       now,
		Actor:      sdksession.ActorRef{Kind: sdksession.ActorKindSystem, Name: "runtime"},
		Text:       text,
		Protocol: &sdksession.EventProtocol{
			Handoff: &sdksession.ProtocolHandoff{Phase: "activation"},
		},
		Scope: &sdksession.EventScope{
			Source: "handoff",
			Controller: sdksession.ControllerRef{
				Kind:    to.Kind,
				ID:      strings.TrimSpace(to.ControllerID),
				EpochID: strings.TrimSpace(to.EpochID),
			},
		},
		Meta: meta,
	}
}

type controllerApprovalRequester struct {
	requester  sdkruntime.ApprovalRequester
	sessionRef sdksession.SessionRef
	session    sdksession.Session
	runID      string
	turnID     string
}

func (r controllerApprovalRequester) RequestControllerApproval(ctx context.Context, req sdkcontroller.ApprovalRequest) (sdkcontroller.ApprovalResponse, error) {
	if r.requester == nil {
		return sdkcontroller.ApprovalResponse{}, nil
	}
	options := make([]sdksession.ProtocolApprovalOption, 0, len(req.Options))
	for _, item := range req.Options {
		options = append(options, sdksession.ProtocolApprovalOption{
			ID:   strings.TrimSpace(item.ID),
			Name: strings.TrimSpace(item.Name),
			Kind: strings.TrimSpace(item.Kind),
		})
	}
	resp, err := r.requester.RequestApproval(ctx, sdkruntime.ApprovalRequest{
		SessionRef: sdksession.NormalizeSessionRef(r.sessionRef),
		Session:    sdksession.CloneSession(r.session),
		RunID:      strings.TrimSpace(r.runID),
		TurnID:     strings.TrimSpace(r.turnID),
		Mode:       strings.TrimSpace(req.Mode),
		Tool: sdktool.Definition{
			Name:        firstNonEmpty(req.ToolCall.Name, req.ToolCall.Title, "ACP_TOOL"),
			Description: firstNonEmpty(req.ToolCall.Title, req.ToolCall.Kind, "ACP controller requested permission"),
		},
		Call: sdktool.Call{
			ID:   strings.TrimSpace(req.ToolCall.ID),
			Name: firstNonEmpty(req.ToolCall.Name, req.ToolCall.Title, "ACP_TOOL"),
		},
		Approval: &sdksession.ProtocolApproval{
			ToolCall: sdksession.ProtocolToolCall{
				ID:     strings.TrimSpace(req.ToolCall.ID),
				Kind:   strings.TrimSpace(req.ToolCall.Kind),
				Title:  strings.TrimSpace(req.ToolCall.Title),
				Status: strings.TrimSpace(req.ToolCall.Status),
			},
			Options: options,
		},
		Metadata: map[string]any{
			"agent": strings.TrimSpace(req.Agent),
		},
	})
	if err != nil {
		return sdkcontroller.ApprovalResponse{}, err
	}
	return sdkcontroller.ApprovalResponse{
		Outcome:  strings.TrimSpace(resp.Outcome),
		OptionID: strings.TrimSpace(resp.OptionID),
		Approved: resp.Approved,
	}, nil
}
