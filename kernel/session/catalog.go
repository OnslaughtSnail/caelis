package session

import (
	"context"
	"time"
)

type CatalogPage struct {
	Number int
	Size   int
}

type SessionSummary struct {
	AppName         string
	UserID          string
	SessionID       string
	WorkspaceKey    string
	WorkspaceCWD    string
	Scope           string
	RolloutPath     string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastEventAt     time.Time
	EventCount      int64
	LastUserMessage string
	Hidden          bool
}

// SessionCatalogStore provides session discovery and summary queries for one
// storage backend. This stays separate from LogStore because listing and prefix
// resolution are indexing concerns rather than canonical event-log concerns.
type SessionCatalogStore interface {
	UpsertSession(context.Context, SessionSummary) error
	GetSession(context.Context, *Session) (SessionSummary, error)
	ListSessions(context.Context, string, CatalogPage) ([]SessionSummary, error)
	ResolveSessionPrefix(context.Context, string, string) (string, bool, error)
	MarkHidden(context.Context, *Session, bool) error
}
