package main

import (
	"context"
	"strings"

	appgateway "github.com/OnslaughtSnail/caelis/internal/app/gateway"
	"github.com/OnslaughtSnail/caelis/kernel/sessionsvc"
)

type cliSessionIndexAdapter struct {
	index *sessionIndex
}

func (a *cliSessionIndexAdapter) ResolveWorkspaceSessionID(ctx context.Context, workspaceKey, prefix string) (string, bool, error) {
	if a == nil || a.index == nil {
		return "", false, nil
	}
	return a.index.ResolveWorkspaceSessionIDContext(ctx, workspaceKey, prefix)
}

func (a *cliSessionIndexAdapter) MostRecentWorkspaceSessionID(ctx context.Context, workspaceKey, excludeSessionID string) (string, bool, error) {
	if a == nil || a.index == nil {
		return "", false, nil
	}
	rec, ok, err := a.index.MostRecentWorkspaceSessionContext(ctx, workspaceKey, excludeSessionID)
	if err != nil || !ok {
		return "", ok, err
	}
	return strings.TrimSpace(rec.SessionID), true, nil
}

func (a *cliSessionIndexAdapter) ListWorkspaceSessionsPage(ctx context.Context, workspaceKey string, page int, pageSize int) ([]sessionsvc.SessionSummary, error) {
	if a == nil || a.index == nil {
		return []sessionsvc.SessionSummary{}, nil
	}
	records, err := a.index.ListWorkspaceSessionsPageContext(ctx, workspaceKey, page, pageSize)
	if err != nil {
		return nil, err
	}
	out := make([]sessionsvc.SessionSummary, 0, len(records))
	for _, rec := range records {
		if rec.EventCount <= 0 {
			continue
		}
		out = append(out, sessionsvc.SessionSummary{
			SessionRef: sessionsvc.SessionRef{
				AppName:      rec.AppName,
				UserID:       rec.UserID,
				SessionID:    rec.SessionID,
				WorkspaceKey: strings.TrimSpace(workspaceKey),
			},
			CWD:       rec.WorkspaceCWD,
			Title:     acpSessionTitle(rec),
			UpdatedAt: rec.LastEventAt,
		})
	}
	return out, nil
}

func (c *cliConsole) gatewayChannel() appgateway.ChannelRef {
	if c == nil {
		return appgateway.ChannelRef{}
	}
	return appgateway.ChannelRef{
		ID:           strings.TrimSpace(c.workspace.Key) + "\x00" + strings.TrimSpace(c.appName) + "\x00" + strings.TrimSpace(c.userID) + "\x00cli",
		AppName:      c.appName,
		UserID:       c.userID,
		WorkspaceKey: c.workspace.Key,
		WorkspaceCWD: c.workspace.CWD,
	}
}
