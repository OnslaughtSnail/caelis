package acp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/slashcmd"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

var defaultACPCommands = slashcmd.New(
	slashcmd.Definition{
		Name:        "help",
		Description: "Show the slash commands available in this ACP session.",
		InputHint:   "/help",
	},
	slashcmd.Definition{
		Name:        "status",
		Description: "Summarize the current ACP session state, model, and mode.",
		InputHint:   "/status",
	},
	slashcmd.Definition{
		Name:        "compact",
		Description: "Manually compact session history. Optionally include a short note.",
		InputHint:   "/compact [note]",
	},
)

func (s *Server) advertisedSlashCommands(sess *serverSession) slashcmd.Registry {
	cmds := s.availableCommands(sess)
	defs := make([]slashcmd.Definition, 0, len(cmds))
	for _, item := range cmds {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		if name == "" {
			continue
		}
		hint := strings.TrimSpace(item.Input.Hint)
		if hint == "" {
			hint = "/" + name
		}
		defs = append(defs, slashcmd.Definition{
			Name:        name,
			Description: strings.TrimSpace(item.Description),
			InputHint:   hint,
		})
	}
	if len(defs) == 0 {
		return defaultACPCommands
	}
	return slashcmd.New(defs...)
}

func (s *Server) handleSlashCommand(ctx context.Context, sessionID string, sess *serverSession, input promptInputResult) (bool, string, error) {
	if input.hasImages || len(input.contentParts) > 0 {
		return false, "", nil
	}
	inv, ok := slashcmd.Parse(input.text)
	registry := s.advertisedSlashCommands(sess)
	if !ok || !registry.Has(inv.Name) {
		return false, "", nil
	}
	sess.resetPartialStreams()
	switch inv.Name {
	case "help":
		lines := make([]string, 0, 1+len(s.availableCommands(sess)))
		lines = append(lines, "Available commands:")
		for _, item := range s.availableCommands(sess) {
			line := "/" + item.Name
			if hint := strings.TrimSpace(item.Input.Hint); hint != "" {
				line = hint
			}
			if desc := strings.TrimSpace(item.Description); desc != "" {
				line += " - " + desc
			}
			lines = append(lines, line)
		}
		return true, StopReasonEndTurn, s.appendAssistantText(ctx, sessionID, strings.Join(lines, "\n"))
	case "status":
		return true, StopReasonEndTurn, s.appendAssistantText(ctx, sessionID, s.formatSlashStatus(sess))
	case "compact":
		return true, StopReasonEndTurn, s.handleSlashCompact(ctx, sessionID, sess, strings.TrimSpace(strings.Join(inv.Args, " ")))
	default:
		return true, StopReasonEndTurn, s.appendAssistantText(ctx, sessionID, fmt.Sprintf("Command /%s is not supported in this session.", inv.Name))
	}
}

func (s *Server) appendAssistantText(ctx context.Context, sessionID string, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	sessRef := s.sessionRef(sessionID)
	ev := &session.Event{
		ID:        fmt.Sprintf("ev_%d", time.Now().UnixNano()),
		SessionID: sessionID,
		Time:      time.Now(),
		Message: model.Message{
			Role: model.RoleAssistant,
			Text: text,
		},
	}
	if err := s.cfg.Store.AppendEvent(ctx, sessRef, ev); err != nil {
		return err
	}
	return s.notifyEvent(sessionID, ev, nil)
}

func (s *Server) formatSlashStatus(sess *serverSession) string {
	mode := ""
	if sess != nil {
		mode = strings.TrimSpace(sess.mode())
	}
	if mode == "" {
		mode = "default"
	}
	lines := []string{
		"Session status:",
		"mode: " + mode,
	}
	if sess != nil && strings.TrimSpace(sess.cwd) != "" {
		lines = append(lines, "cwd: "+sess.cwd)
	}
	if modelID := s.currentModelID(sess); modelID != "" {
		lines = append(lines, "model: "+modelID)
	}
	if entries := sess.planSnapshot(); len(entries) > 0 {
		lines = append(lines, fmt.Sprintf("plan items: %d", len(entries)))
	}
	return strings.Join(lines, "\n")
}

func (s *Server) currentModelID(sess *serverSession) string {
	if sess == nil {
		return ""
	}
	for _, item := range s.sessionConfigOptions(sess) {
		if strings.TrimSpace(item.Category) != "model" {
			continue
		}
		return strings.TrimSpace(item.CurrentValue)
	}
	return ""
}

func (s *Server) handleSlashCompact(ctx context.Context, sessionID string, sess *serverSession, note string) error {
	if s.cfg.Runtime == nil {
		return fmt.Errorf("runtime compaction is unavailable")
	}
	modelValue, err := s.cfg.NewModel(sess.agentConfig())
	if err != nil {
		return err
	}
	ev, err := s.cfg.Runtime.Compact(ctx, runtime.CompactRequest{
		AppName:   s.cfg.AppName,
		UserID:    s.cfg.UserID,
		SessionID: sessionID,
		Model:     modelValue,
		Note:      note,
	})
	if err != nil {
		return err
	}
	if ev == nil {
		return s.appendAssistantText(ctx, sessionID, "Compact skipped.")
	}
	return s.appendAssistantText(ctx, sessionID, "Compact completed.")
}
