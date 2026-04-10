package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	if h == nil {
		return nil, ErrRunnerClosed
	}
	if sub == nil {
		return nil, fmt.Errorf("runtime: overlay submission is required")
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
		annotateControllerMeta(recoveryEvent, h.req.ControllerKind, h.req.ControllerID, h.req.EpochID)
		if err := h.runtime.logStore.AppendEvent(h.ctx, h.sess, recoveryEvent); err != nil {
			h.emitTerminalError(err)
			return nil, false
		}
		if !h.appendOutput(recoveryEvent, nil, false) {
			return nil, false
		}
	}
	allEvents := append([]*session.Event(nil), existing...)
	for _, recoveryEvent := range recoveryEvents {
		if recoveryEvent == nil {
			continue
		}
		allEvents = append(allEvents, recoveryEvent)
	}
	allEvents = append(allEvents, buildInvocationPreludeEvents(h.req.InvocationPrelude)...)
	userMsg := model.MessageFromTextAndContentParts(model.RoleUser, sub.Text, prepareUserContentParts(sub.Text, sub.ContentParts))
	userEvent := &session.Event{Message: userMsg}
	prepareEvent(h.ctx, h.sess, userEvent)
	annotateControllerMeta(userEvent, h.req.ControllerKind, h.req.ControllerID, h.req.EpochID)
	if err := h.runtime.logStore.AppendEvent(h.ctx, h.sess, userEvent); err != nil {
		h.emitTerminalError(err)
		return nil, false
	}
	if !h.appendOutput(userEvent, nil, false) {
		return nil, false
	}
	allEvents = append(allEvents, userEvent)
	updatedEvents, err := h.runtime.listContextWindowEvents(h.ctx, h.sess)
	if err != nil {
		h.emitTerminalError(err)
		return nil, false
	}
	compactionEvent, compactErr := h.runtime.compactIfNeededWithNotify(h.ctx, compactInput{
		Session:             h.sess,
		Model:               h.req.Model,
		Events:              updatedEvents,
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
		allEvents = insertPreludeBeforeTrailingUser(allEvents, buildInvocationPreludeEvents(h.req.InvocationPrelude))
	}
	return allEvents, true
}

func buildInvocationPreludeEvents(messages []model.Message) []*session.Event {
	if len(messages) == 0 {
		return nil
	}
	events := make([]*session.Event, 0, len(messages))
	for _, msg := range messages {
		if strings.TrimSpace(string(msg.Role)) == "" {
			continue
		}
		if strings.TrimSpace(msg.TextContent()) == "" && len(msg.Parts) == 0 {
			continue
		}
		events = append(events, session.MarkOverlay(&session.Event{
			ID:      eventID(),
			Time:    now(),
			Message: msg,
		}))
	}
	return events
}

func annotateControllerMeta(ev *session.Event, kind string, controllerID string, epochID string) {
	if ev == nil {
		return
	}
	kind = strings.TrimSpace(kind)
	controllerID = strings.TrimSpace(controllerID)
	epochID = strings.TrimSpace(epochID)
	if kind == "" && controllerID == "" && epochID == "" {
		return
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	if kind != "" {
		ev.Meta["controller_kind"] = kind
	}
	if controllerID != "" {
		ev.Meta["controller_id"] = controllerID
	}
	if epochID != "" {
		ev.Meta["epoch_id"] = epochID
	}
}

func insertPreludeBeforeTrailingUser(events []*session.Event, prelude []*session.Event) []*session.Event {
	if len(prelude) == 0 {
		return events
	}
	if len(events) == 0 {
		return append([]*session.Event(nil), prelude...)
	}
	last := events[len(events)-1]
	if last == nil || last.Message.Role != model.RoleUser {
		return append(events, prelude...)
	}
	out := make([]*session.Event, 0, len(events)+len(prelude))
	out = append(out, events[:len(events)-1]...)
	out = append(out, prelude...)
	out = append(out, last)
	return out
}
