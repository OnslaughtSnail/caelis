package main

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
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
	if updater, ok := c.sessionStore.(session.StateUpdateStore); ok {
		return updater.UpdateState(c.baseCtx, c.currentSessionRef(), func(values map[string]any) (map[string]any, error) {
			if values == nil {
				values = map[string]any{}
			}
			return sessionmode.StoreSnapshot(values, c.sessionMode), nil
		})
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
	if err := c.restoreSessionMode(c.loadSessionMode()); err != nil {
		c.printf("warn: sync session mode failed: %v\n", err)
	}
}

func (c *cliConsole) setSessionMode(mode string) error {
	if c == nil {
		return nil
	}
	return c.applySessionMode(mode, sessionModeApplyOptions{
		persistSession: true,
		syncRuntime:    true,
	})
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

type sessionModeApplyOptions struct {
	persistSession        bool
	syncRuntime           bool
	persistRuntimeDefault bool
}

func (c *cliConsole) restoreSessionMode(mode string) error {
	if c == nil {
		return nil
	}
	c.sessionMode = c.sanitizedRestoredSessionMode(mode)
	return nil
}

func (c *cliConsole) sanitizedRestoredSessionMode(mode string) string {
	nextMode := sessionmode.Normalize(mode)
	if nextMode == sessionmode.FullMode && !c.permissionAllowsFullAccess() {
		return sessionmode.DefaultMode
	}
	return nextMode
}

func (c *cliConsole) permissionAllowsFullAccess() bool {
	return c != nil && c.execRuntime != nil && c.execRuntime.PermissionMode() == toolexec.PermissionModeFullControl
}

func (c *cliConsole) setPermissionMode(mode toolexec.PermissionMode) error {
	if c == nil {
		return nil
	}
	targetMode := sessionmode.ModeForPermission(mode, c.sessionMode)
	return c.applySessionMode(targetMode, sessionModeApplyOptions{
		persistSession:        true,
		syncRuntime:           true,
		persistRuntimeDefault: true,
	})
}

func (c *cliConsole) applySessionMode(mode string, opts sessionModeApplyOptions) error {
	if c == nil {
		return nil
	}
	nextMode := sessionmode.Normalize(mode)
	prevMode := c.sessionMode
	prevPermission := toolexec.PermissionModeDefault
	if c.execRuntime != nil {
		prevPermission = c.execRuntime.PermissionMode()
	}
	nextPermission := sessionmode.PermissionMode(nextMode)
	if opts.syncRuntime && prevPermission != nextPermission {
		if err := c.updateExecutionRuntime(nextPermission, c.sandboxType); err != nil {
			return err
		}
	}
	c.sessionMode = nextMode
	if opts.persistSession {
		if err := c.persistSessionMode(); err != nil {
			c.sessionMode = prevMode
			if opts.syncRuntime && prevPermission != nextPermission {
				_ = c.updateExecutionRuntime(prevPermission, c.sandboxType)
			}
			return err
		}
	}
	if opts.persistRuntimeDefault {
		c.persistRuntimeSettings()
	}
	return nil
}
