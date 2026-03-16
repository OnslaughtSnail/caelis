package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/idutil"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	_ "modernc.org/sqlite"
)

const (
	sessionIndexDriver = "sqlite"
	sessionIndexDSNOpt = "?_pragma=busy_timeout(3000)&_pragma=journal_mode(WAL)"
)

type sessionIndex struct {
	db *sql.DB
	mu sync.Mutex
}

type sessionIndexRecord struct {
	SessionID       string
	AppName         string
	UserID          string
	WorkspaceCWD    string
	CreatedAt       time.Time
	LastEventAt     time.Time
	EventCount      int64
	LastUserMessage string
}

func newSessionIndex(path string) (*sessionIndex, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("session index: path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("session index: create dir: %w", err)
	}
	db, err := sql.Open(sessionIndexDriver, path+sessionIndexDSNOpt)
	if err != nil {
		return nil, fmt.Errorf("session index: open db: %w", err)
	}
	idx := &sessionIndex{db: db}
	if err := idx.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return idx, nil
}

func (s *sessionIndex) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *sessionIndex) UpsertSession(workspace workspaceContext, appName, userID, sessionID string, at time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	if strings.TrimSpace(workspace.Key) == "" || strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session index: workspace and session_id are required")
	}
	if at.IsZero() {
		at = time.Now()
	}
	ts := at.UnixMilli()
	const q = `
INSERT INTO session_index (
	workspace_key, session_id, workspace_cwd, app_name, user_id,
	created_at, last_event_at, event_count, last_user_message
) VALUES (?, ?, ?, ?, ?, ?, ?, 0, '')
ON CONFLICT(workspace_key, session_id) DO UPDATE SET
	workspace_cwd = excluded.workspace_cwd,
	app_name = excluded.app_name,
	user_id = excluded.user_id,
	last_event_at = CASE
		WHEN session_index.last_event_at > excluded.last_event_at THEN session_index.last_event_at
		ELSE excluded.last_event_at
	END`
	_, err := s.db.ExecContext(context.Background(), q,
		workspace.Key, sessionID, workspace.CWD, appName, userID,
		ts, ts,
	)
	return err
}

func (s *sessionIndex) TouchEvent(workspace workspaceContext, appName, userID, sessionID string, ev *session.Event, at time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	if strings.TrimSpace(workspace.Key) == "" || strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session index: workspace and session_id are required")
	}
	if at.IsZero() {
		at = time.Now()
	}
	lastUser := ""
	if ev != nil && ev.Message.Role == model.RoleUser && !isCompactionEventForIndex(ev) {
		lastUser = sessionIndexLastUserMessage(ev)
	}
	ts := at.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.UpsertSession(workspace, appName, userID, sessionID, at); err != nil {
		return err
	}
	const q = `
UPDATE session_index SET
	last_event_at = ?,
	event_count = event_count + 1,
	last_user_message = CASE
		WHEN ? <> '' THEN ?
		ELSE last_user_message
	END
WHERE workspace_key = ? AND session_id = ?`
	_, err := s.db.ExecContext(context.Background(), q, ts, lastUser, lastUser, workspace.Key, sessionID)
	return err
}

func isCompactionEventForIndex(ev *session.Event) bool {
	return session.EventTypeOf(ev) == session.EventTypeCompaction
}

func (s *sessionIndex) ListWorkspaceSessions(workspaceKey string, limit int) ([]sessionIndexRecord, error) {
	return s.ListWorkspaceSessionsPage(workspaceKey, 1, limit)
}

func (s *sessionIndex) ListWorkspaceSessionsPage(workspaceKey string, page int, pageSize int) ([]sessionIndexRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	if workspaceKey == "" {
		return nil, fmt.Errorf("session index: workspace_key is required")
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	const q = `
	SELECT session_id, app_name, user_id, workspace_cwd, created_at, last_event_at, event_count, last_user_message
	FROM session_index
	WHERE workspace_key = ?
	ORDER BY last_event_at DESC, created_at DESC
	LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(context.Background(), q, workspaceKey, pageSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]sessionIndexRecord, 0, pageSize)
	for rows.Next() {
		var rec sessionIndexRecord
		var createdAt, lastEventAt int64
		if err := rows.Scan(&rec.SessionID, &rec.AppName, &rec.UserID, &rec.WorkspaceCWD, &createdAt, &lastEventAt, &rec.EventCount, &rec.LastUserMessage); err != nil {
			return nil, err
		}
		rec.CreatedAt = time.UnixMilli(createdAt)
		rec.LastEventAt = time.UnixMilli(lastEventAt)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *sessionIndex) CountWorkspaceSessions(workspaceKey string) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	if workspaceKey == "" {
		return 0, fmt.Errorf("session index: workspace_key is required")
	}
	const q = `SELECT COUNT(*) FROM session_index WHERE workspace_key = ?`
	var count int
	if err := s.db.QueryRowContext(context.Background(), q, workspaceKey).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *sessionIndex) MostRecentWorkspaceSession(workspaceKey string, excludeSessionID string) (sessionIndexRecord, bool, error) {
	if s == nil || s.db == nil {
		return sessionIndexRecord{}, false, nil
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	if workspaceKey == "" {
		return sessionIndexRecord{}, false, fmt.Errorf("session index: workspace_key is required")
	}
	excludeSessionID = strings.TrimSpace(excludeSessionID)
	const q = `
	SELECT session_id, app_name, user_id, workspace_cwd, created_at, last_event_at, event_count, last_user_message
	FROM session_index
	WHERE workspace_key = ? AND (? = '' OR session_id <> ?)
	ORDER BY last_event_at DESC, created_at DESC
	LIMIT 1`
	var rec sessionIndexRecord
	var createdAt, lastEventAt int64
	if err := s.db.QueryRowContext(context.Background(), q, workspaceKey, excludeSessionID, excludeSessionID).Scan(
		&rec.SessionID, &rec.AppName, &rec.UserID, &rec.WorkspaceCWD, &createdAt, &lastEventAt, &rec.EventCount, &rec.LastUserMessage,
	); err != nil {
		if err == sql.ErrNoRows {
			return sessionIndexRecord{}, false, nil
		}
		return sessionIndexRecord{}, false, err
	}
	rec.CreatedAt = time.UnixMilli(createdAt)
	rec.LastEventAt = time.UnixMilli(lastEventAt)
	return rec, true, nil
}

func (s *sessionIndex) HasWorkspaceSession(workspaceKey, sessionID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, nil
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	sessionID = strings.TrimSpace(sessionID)
	if workspaceKey == "" || sessionID == "" {
		return false, fmt.Errorf("session index: workspace_key and session_id are required")
	}
	const q = `SELECT 1 FROM session_index WHERE workspace_key = ? AND session_id = ? LIMIT 1`
	var one int
	if err := s.db.QueryRowContext(context.Background(), q, workspaceKey, sessionID).Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *sessionIndex) ResolveWorkspaceSessionID(workspaceKey, prefix string) (string, bool, error) {
	if s == nil || s.db == nil {
		return "", false, nil
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	prefix = strings.TrimSpace(prefix)
	if workspaceKey == "" || prefix == "" {
		return "", false, fmt.Errorf("session index: workspace_key and session_id are required")
	}
	const q = `
	SELECT session_id
	FROM session_index
	WHERE workspace_key = ? AND session_id LIKE ?
	ORDER BY last_event_at DESC, created_at DESC
	LIMIT 3`
	rows, err := s.db.QueryContext(context.Background(), q, workspaceKey, prefix+"%")
	if err != nil {
		return "", false, err
	}
	defer rows.Close()

	matches := make([]string, 0, 3)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return "", false, err
		}
		matches = append(matches, sessionID)
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	for _, match := range matches {
		if match == prefix {
			return match, true, nil
		}
	}
	switch len(matches) {
	case 0:
		return "", false, nil
	case 1:
		return matches[0], true, nil
	default:
		return "", false, fmt.Errorf("session prefix %q is ambiguous in current workspace", prefix)
	}
}

func (s *sessionIndex) SyncWorkspaceFromStoreDir(workspace workspaceContext, appName, userID, storeDir string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if strings.TrimSpace(workspace.Key) == "" {
		return fmt.Errorf("session index: workspace_key is required")
	}
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := strings.TrimSpace(entry.Name())
		if sessionID == "" {
			continue
		}
		eventsPath := filepath.Join(storeDir, sessionID, "events.jsonl")
		info, statErr := os.Stat(eventsPath)
		if statErr != nil {
			continue
		}
		snapshot, snapErr := readSessionIndexSnapshot(eventsPath)
		timestamp := info.ModTime()
		if snapErr == nil && !snapshot.LastEventAt.IsZero() {
			timestamp = snapshot.LastEventAt
		}
		if err := s.UpsertSession(workspace, appName, userID, sessionID, timestamp); err != nil {
			return err
		}
		if snapErr == nil {
			if err := s.applySessionSnapshot(workspace, sessionID, snapshot); err != nil {
				return err
			}
		}
	}
	return nil
}

type sessionIndexSnapshot struct {
	LastEventAt     time.Time
	EventCount      int64
	LastUserMessage string
}

func readSessionIndexSnapshot(eventsPath string) (sessionIndexSnapshot, error) {
	f, err := os.Open(eventsPath)
	if err != nil {
		return sessionIndexSnapshot{}, err
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	var snapshot sessionIndexSnapshot
	for {
		var ev session.Event
		if err := dec.Decode(&ev); err != nil {
			if err == io.EOF {
				break
			}
			return sessionIndexSnapshot{}, err
		}
		snapshot.EventCount++
		if !ev.Time.IsZero() && ev.Time.After(snapshot.LastEventAt) {
			snapshot.LastEventAt = ev.Time
		}
		if ev.Message.Role == model.RoleUser && !isCompactionEventForIndex(&ev) {
			if text := sessionIndexLastUserMessage(&ev); text != "" {
				snapshot.LastUserMessage = text
			}
		}
	}
	return snapshot, nil
}

func sessionIndexLastUserMessage(ev *session.Event) string {
	if ev == nil {
		return ""
	}
	lastUser := sessionmode.VisibleText(strings.TrimSpace(ev.Message.Text))
	if lastUser == "" && len(ev.Message.ContentParts) > 0 {
		parts := make([]string, 0, len(ev.Message.ContentParts))
		for _, part := range ev.Message.ContentParts {
			if part.Type != model.ContentPartText {
				continue
			}
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			parts = append(parts, text)
		}
		lastUser = sessionmode.VisibleText(strings.Join(parts, "\n"))
	}
	return lastUser
}

func (s *sessionIndex) applySessionSnapshot(workspace workspaceContext, sessionID string, snapshot sessionIndexSnapshot) error {
	if s == nil || s.db == nil {
		return nil
	}
	ts := snapshot.LastEventAt.UnixMilli()
	if snapshot.LastEventAt.IsZero() {
		ts = time.Now().UnixMilli()
	}
	const q = `
UPDATE session_index SET
	last_event_at = ?,
	event_count = CASE WHEN ? > 0 THEN ? ELSE event_count END,
	last_user_message = CASE WHEN ? <> '' THEN ? ELSE last_user_message END
WHERE workspace_key = ? AND session_id = ?`
	_, err := s.db.ExecContext(context.Background(), q, ts, snapshot.EventCount, snapshot.EventCount, snapshot.LastUserMessage, snapshot.LastUserMessage, workspace.Key, sessionID)
	return err
}

func sessionIndexPreview(rec sessionIndexRecord, limit int) string {
	prompt := strings.TrimSpace(rec.LastUserMessage)
	if prompt == "" {
		return idutil.ShortDisplay(strings.TrimSpace(rec.SessionID))
	}
	prompt = strings.ReplaceAll(prompt, "\n", " ")
	return truncateInline(prompt, limit)
}

func (s *sessionIndex) migrate(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS session_index (
	workspace_key TEXT NOT NULL,
	session_id TEXT NOT NULL,
	workspace_cwd TEXT NOT NULL DEFAULT '',
	app_name TEXT NOT NULL DEFAULT '',
	user_id TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	last_event_at INTEGER NOT NULL,
	event_count INTEGER NOT NULL DEFAULT 0,
	last_user_message TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (workspace_key, session_id)
);
CREATE INDEX IF NOT EXISTS idx_session_index_workspace_last_event
ON session_index(workspace_key, last_event_at DESC);`
	_, err := s.db.ExecContext(ctx, ddl)
	if err != nil {
		return fmt.Errorf("session index: migrate: %w", err)
	}
	return nil
}
