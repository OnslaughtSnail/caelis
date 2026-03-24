package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type SubmissionMode string

const (
	SubmissionConversation SubmissionMode = "conversation"
	SubmissionOverlay      SubmissionMode = "overlay"
)

type Submission struct {
	Text         string
	ContentParts []model.ContentPart
	Mode         SubmissionMode
}

func (h *runHandle) takeSubmission() *Submission {
	return h.submitSlot.Swap(nil)
}

func (h *runHandle) handleOverlaySubmission(baseInv *invocationContext, sub *Submission) bool {
	if h == nil || sub == nil {
		return true
	}
	inv, err := h.buildOverlayInvocation(baseInv, sub)
	if err != nil {
		if h.shouldPropagateOverlayError(baseInv, err) {
			h.emitTerminalError(err)
			return false
		}
		return h.emitOverlayError(err)
	}
	pump := startAgentRunPump(h.ctx, h.req.Agent, inv)
	for {
		item, open := pump.next()
		if !open {
			if err := h.ctx.Err(); err != nil {
				if h.shouldPropagateOverlayError(baseInv, err) {
					h.emitTerminalError(err)
					return false
				}
				return h.emitOverlayError(err)
			}
			return true
		}
		if item.err != nil {
			if h.shouldPropagateOverlayError(baseInv, item.err) {
				h.emitTerminalError(item.err)
				return false
			}
			return h.emitOverlayError(item.err)
		}
		ev := item.event
		if ev == nil {
			if !pump.respond(true) {
				return true
			}
			continue
		}
		ev = session.MarkOverlay(ev)
		if !h.appendOutput(ev, nil, shouldPersistEvent(ev)) {
			_ = pump.respond(false)
			return false
		}
		if !pump.respond(true) {
			return true
		}
	}
}

func (h *runHandle) shouldPropagateOverlayError(baseInv *invocationContext, err error) bool {
	if err == nil {
		return false
	}
	if baseInv == nil {
		return true
	}
	return errors.Is(err, context.Canceled)
}

func (h *runHandle) buildOverlayInvocation(baseInv *invocationContext, sub *Submission) (*invocationContext, error) {
	if h == nil || sub == nil {
		return nil, nil
	}
	baseEvents, err := h.overlayBaseEvents(baseInv)
	if err != nil {
		return nil, err
	}
	allEvents := append([]*session.Event(nil), baseEvents...)
	for _, recoveryEvent := range buildRecoveryEvents(baseEvents) {
		if recoveryEvent == nil {
			continue
		}
		allEvents = append(allEvents, session.MarkOverlay(recoveryEvent))
	}
	userMsg := model.MessageFromTextAndContentParts(model.RoleUser, sub.Text, prepareUserContentParts(sub.Text, sub.ContentParts))
	allEvents = append(allEvents, session.MarkOverlay(&session.Event{
		ID:      eventID(),
		Time:    now(),
		Message: userMsg,
	}))
	inv, err := h.runtime.buildInvocationContext(h.ctx, h.sess, h.req, allEvents)
	if err != nil {
		return nil, err
	}
	inv.overlay = true
	inv.policies = append(inv.policies, overlayToolDenyHook{})
	return inv, nil
}

func (h *runHandle) overlayBaseEvents(baseInv *invocationContext) ([]*session.Event, error) {
	if baseInv != nil && baseInv.events != nil {
		events := make([]*session.Event, 0, baseInv.events.Len())
		for ev := range baseInv.events.All() {
			if ev == nil {
				continue
			}
			events = append(events, ev)
		}
		return events, nil
	}
	return h.runtime.listContextWindowEvents(h.ctx, h.sess)
}

func (h *runHandle) emitOverlayError(err error) bool {
	if err == nil {
		return true
	}
	ev := session.MarkOverlay(&session.Event{
		ID:      eventID(),
		Time:    now(),
		Message: model.NewTextMessage(model.RoleAssistant, fmt.Sprintf("error: %v", err)),
	})
	return h.appendOutput(ev, nil, false)
}

type overlayToolDenyHook struct {
	policy.NoopHook
}

func (h overlayToolDenyHook) Name() string {
	return "overlay_tool_deny"
}

func (h overlayToolDenyHook) BeforeTool(ctx context.Context, in policy.ToolInput) (policy.ToolInput, error) {
	_ = ctx
	in.Decision = policy.NormalizeDecision(policy.Decision{
		Effect: policy.DecisionEffectDeny,
		Reason: "side question mode does not allow tool use; answer from existing context only",
	})
	return in, nil
}

func (h *runHandle) applySubmission(sub *Submission) ([]*session.Event, bool) {
	if sub == nil {
		allEvents, err := h.runtime.listContextWindowEvents(h.ctx, h.sess)
		if err != nil {
			h.emitTerminalError(err)
			return nil, false
		}
		return allEvents, true
	}
	existing, err := h.runtime.listContextWindowEvents(h.ctx, h.sess)
	if err != nil {
		h.emitTerminalError(err)
		return nil, false
	}
	recoveryEvents := buildRecoveryEvents(existing)
	for _, recoveryEvent := range recoveryEvents {
		if recoveryEvent == nil {
			continue
		}
		prepareEvent(h.ctx, h.sess, recoveryEvent)
		if err := h.runtime.store.AppendEvent(h.ctx, h.sess, recoveryEvent); err != nil {
			h.emitTerminalError(err)
			return nil, false
		}
		if !h.appendOutput(recoveryEvent, nil, false) {
			return nil, false
		}
	}
	userMsg := model.MessageFromTextAndContentParts(model.RoleUser, sub.Text, prepareUserContentParts(sub.Text, sub.ContentParts))
	userEvent := &session.Event{Message: userMsg}
	prepareEvent(h.ctx, h.sess, userEvent)
	if err := h.runtime.store.AppendEvent(h.ctx, h.sess, userEvent); err != nil {
		h.emitTerminalError(err)
		return nil, false
	}
	if !h.appendOutput(userEvent, nil, false) {
		return nil, false
	}
	allEvents, err := h.runtime.listContextWindowEvents(h.ctx, h.sess)
	if err != nil {
		h.emitTerminalError(err)
		return nil, false
	}
	compactionEvent, compactErr := h.runtime.compactIfNeededWithNotify(h.ctx, compactInput{
		Session:             h.sess,
		Model:               h.req.Model,
		Events:              allEvents,
		ContextWindowTokens: h.req.ContextWindowTokens,
		Trigger:             triggerAuto,
		Force:               false,
	}, func(ev *session.Event) bool {
		if ev == nil {
			return true
		}
		return h.appendOutput(ev, nil, shouldPersistEvent(ev))
	})
	if compactErr != nil {
		h.emitTerminalError(compactErr)
		return nil, false
	}
	if compactionEvent != nil {
		if !h.appendOutput(compactionEvent, nil, shouldPersistEvent(compactionEvent)) {
			return nil, false
		}
		allEvents, err = h.runtime.listContextWindowEvents(h.ctx, h.sess)
		if err != nil {
			h.emitTerminalError(err)
			return nil, false
		}
	}
	return allEvents, true
}
