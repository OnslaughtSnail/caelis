package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/epochhandoff"
	compact "github.com/OnslaughtSnail/caelis/kernel/compaction"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	coreacpmeta "github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

const (
	handoffSoftTailTokens = 2400
	handoffHardTailTokens = 3600
	handoffMinTailEvents  = 6
)

func (c *cliConsole) handoffCoordinator() *epochhandoff.HandoffCoordinator {
	if c == nil || c.sessionStore == nil {
		return nil
	}
	return epochhandoff.NewHandoffCoordinator(c.sessionStore)
}

func (c *cliConsole) closeCurrentEpochCheckpoint(ctx context.Context) error {
	coordinator := c.handoffCoordinator()
	rootSession := c.currentSessionRef()
	if coordinator == nil || rootSession == nil {
		return nil
	}
	_, err := coordinator.CloseEpochAndCheckpoint(cliContext(ctx), rootSession)
	return err
}

func buildHandoffTranscriptTail(events []*session.Event) string {
	filtered := make([]*session.Event, 0, len(events))
	for _, ev := range events {
		if session.IsCanonicalHistoryEvent(ev) {
			filtered = append(filtered, ev)
		}
	}
	if len(filtered) == 0 {
		return ""
	}
	_, tail := compact.SplitTargetWithOptions(filtered, compact.SplitOptions{
		SoftTailTokens: handoffSoftTailTokens,
		HardTailTokens: handoffHardTailTokens,
		MinTailEvents:  handoffMinTailEvents,
	})
	if len(tail) == 0 {
		tail = filtered
	}
	return strings.TrimSpace(formatHandoffTranscriptTail(tail))
}

func formatHandoffTranscriptTail(events []*session.Event) string {
	var b strings.Builder
	for _, ev := range events {
		if ev == nil {
			continue
		}
		role := strings.TrimSpace(string(ev.Message.Role))
		if role == "" {
			continue
		}
		text := strings.TrimSpace(handoffTranscriptEventText(ev))
		if text == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", role, text)
	}
	return b.String()
}

func handoffTranscriptEventText(ev *session.Event) string {
	if ev == nil {
		return ""
	}
	msg := ev.Message
	if msg.Role == model.RoleUser {
		return visibleUserText(msg)
	}
	if text := strings.TrimSpace(msg.TextContent()); text != "" {
		return text
	}
	if reasoning := strings.TrimSpace(msg.ReasoningText()); reasoning != "" {
		return "reasoning: " + reasoning
	}
	return strings.TrimSpace(compact.EventToText(ev))
}

func (c *cliConsole) buildPendingSelfHandoff(ctx context.Context) ([]model.Message, error) {
	coordinator := c.handoffCoordinator()
	rootSession := c.currentSessionRef()
	if coordinator == nil || rootSession == nil || c.sessionStore == nil {
		return nil, nil
	}
	history, err := c.sessionStore.ListEvents(ctx, rootSession)
	if err != nil && !errors.Is(err, session.ErrSessionNotFound) {
		return nil, err
	}
	bundle, err := coordinator.BuildHandoffBundle(ctx, rootSession, "self", coreacpmeta.RemoteSyncState{}, buildHandoffTranscriptTail(history))
	if err != nil {
		return nil, err
	}
	msg := epochhandoff.SyntheticHandoffMessage(bundle)
	if strings.TrimSpace(string(msg.Role)) == "" {
		return nil, nil
	}
	return []model.Message{msg}, nil
}

func (c *cliConsole) prepareMainControllerTurn(ctx context.Context, kind string, controllerID string) (string, []model.Message, bool, error) {
	rootSession := c.currentSessionRef()
	if c == nil || c.sessionStore == nil || rootSession == nil {
		return "", nil, false, nil
	}
	current, err := coreacpmeta.ControllerEpochFromStore(ctx, c.sessionStore, rootSession)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			return "", nil, false, nil
		}
		return "", nil, false, err
	}
	kind = strings.TrimSpace(kind)
	controllerID = strings.TrimSpace(controllerID)
	if current.EpochID != "" && current.ControllerKind == kind && current.ControllerID == controllerID {
		return current.EpochID, nil, false, nil
	}
	if current.EpochID != "" {
		if err := c.closeCurrentEpochCheckpoint(ctx); err != nil {
			return "", nil, false, err
		}
	}
	var invocationPrelude []model.Message
	if kind == coreacpmeta.ControllerKindSelf && current.EpochID != "" {
		invocationPrelude, err = c.buildPendingSelfHandoff(ctx)
		if err != nil {
			return "", nil, false, err
		}
	}
	epochID, err := c.advanceControllerEpoch(ctx, kind, controllerID)
	if err != nil {
		return "", nil, false, err
	}
	return epochID, invocationPrelude, true, nil
}
