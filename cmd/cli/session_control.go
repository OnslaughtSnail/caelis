package main

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func (c *cliConsole) currentSessionRef() *session.Session {
	if c == nil {
		return nil
	}
	return &session.Session{
		AppName: c.appName,
		UserID:  c.userID,
		ID:      strings.TrimSpace(c.sessionID),
	}
}

func (c *cliConsole) loadSessionMode() string {
	if c == nil || c.sessionStore == nil {
		return sessionmode.DefaultMode
	}
	values, err := c.sessionStore.SnapshotState(c.baseCtx, c.currentSessionRef())
	if err != nil {
		return sessionmode.DefaultMode
	}
	return sessionmode.LoadSnapshot(values)
}

func (c *cliConsole) persistSessionMode() error {
	if c == nil || c.sessionStore == nil {
		return nil
	}
	values, err := c.sessionStore.SnapshotState(c.baseCtx, c.currentSessionRef())
	if err != nil {
		return err
	}
	return c.sessionStore.ReplaceState(c.baseCtx, c.currentSessionRef(), sessionmode.StoreSnapshot(values, c.sessionMode))
}

func (c *cliConsole) syncSessionModeFromStore() {
	if c == nil {
		return
	}
	c.sessionMode = c.loadSessionMode()
}

func (c *cliConsole) setSessionMode(mode string) error {
	if c == nil {
		return nil
	}
	c.sessionMode = sessionmode.Normalize(mode)
	if err := c.persistSessionMode(); err != nil {
		return err
	}
	return nil
}

func (c *cliConsole) togglePlanMode() (string, error) {
	if c == nil {
		return "", nil
	}
	next := sessionmode.Next(c.sessionMode)
	if err := c.setSessionMode(next); err != nil {
		return "", err
	}
	switch next {
	case sessionmode.PlanMode:
		return "plan mode enabled", nil
	case sessionmode.FullMode:
		return "full access mode enabled", nil
	default:
		return "default mode enabled", nil
	}
}

func (c *cliConsole) refreshSessionModeStatus() {
	if c == nil || c.tuiSender == nil {
		return
	}
	modelText, contextText := c.readTUIStatus()
	c.tuiSender.Send(tuievents.SetStatusMsg{Model: modelText, Context: contextText})
}

func (c *cliConsole) sessionModeLabel() string {
	if c == nil {
		return ""
	}
	return sessionmode.DisplayLabel(c.sessionMode)
}

func (c *cliConsole) injectedPrompt(input string) string {
	if c == nil {
		return input
	}
	return sessionmode.Inject(input, c.sessionMode)
}
