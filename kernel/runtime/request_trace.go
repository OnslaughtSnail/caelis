package runtime

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type requestTraceSessionDirStore interface {
	SessionDir(*session.Session) (string, error)
}

func withRequestTraceContext(ctx context.Context, store session.LogStore, sess *session.Session, runID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if sess == nil {
		return ctx
	}
	info := model.RequestTraceContext{
		SessionID: strings.TrimSpace(sess.ID),
		RunID:     strings.TrimSpace(runID),
	}
	if withDir, ok := store.(requestTraceSessionDirStore); ok {
		if dir, err := withDir.SessionDir(sess); err == nil && strings.TrimSpace(dir) != "" {
			info.Path = filepath.Join(dir, model.RequestTraceFileName)
		}
	}
	return model.WithRequestTraceContext(ctx, info)
}
