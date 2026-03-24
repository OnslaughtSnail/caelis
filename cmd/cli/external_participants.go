package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const (
	externalParticipantsStateKey = "external_participants_v1"

	metaParticipantAlias       = "participant_alias"
	metaParticipantAgent       = "participant_agent"
	metaParticipantDisplay     = "display_label"
	metaMirrorSourceSessionID  = "mirror_source_session_id"
	metaMirrorSourceEventID    = "mirror_source_event_id"
	metaRouteKind              = "route_kind"
	metaParentSessionID        = "parent_session_id"
	metaChildSessionID         = "child_session_id"
	metaParticipantSessionKind = "participant_session_kind"
)

type externalParticipant struct {
	Alias          string
	AgentID        string
	ChildSessionID string
	DisplayLabel   string
	Status         string
	CreatedAt      time.Time
	LastActiveAt   time.Time
}

var participantAliasPool = []string{
	"john", "amy", "mike", "luna", "leo", "emma", "zoe", "liam",
	"maya", "nora", "jack", "iris", "kate", "alex", "ella", "owen",
	"ruby", "evan", "noah", "mia", "lucy", "jude", "cole", "claire",
}

func participantDisplayLabel(alias string, agentID string) string {
	alias = strings.TrimSpace(alias)
	agentID = strings.TrimSpace(agentID)
	if alias == "" {
		return agentID
	}
	if agentID == "" {
		return alias
	}
	return alias + "(" + agentID + ")"
}

func normalizeParticipantAlias(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func parseParticipantRouteInput(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "@") {
		return "", "", false
	}
	body := strings.TrimSpace(strings.TrimPrefix(line, "@"))
	if body == "" {
		return "", "", false
	}
	parts := strings.Fields(body)
	if len(parts) < 2 {
		return normalizeParticipantAlias(extractParticipantAlias(parts[0])), "", true
	}
	alias := normalizeParticipantAlias(extractParticipantAlias(parts[0]))
	if alias == "" {
		return "", "", false
	}
	return alias, strings.TrimSpace(strings.TrimPrefix(body, parts[0])), true
}

func extractParticipantAlias(label string) string {
	label = strings.TrimSpace(label)
	if idx := strings.Index(label, "("); idx > 0 {
		label = label[:idx]
	}
	return label
}

func participantStateMap(p externalParticipant) map[string]any {
	return map[string]any{
		"alias":            strings.TrimSpace(p.Alias),
		"agent_id":         strings.TrimSpace(p.AgentID),
		"child_session_id": strings.TrimSpace(p.ChildSessionID),
		"display_label":    strings.TrimSpace(p.DisplayLabel),
		"status":           strings.TrimSpace(p.Status),
		"created_at":       p.CreatedAt.Format(time.RFC3339Nano),
		"last_active_at":   p.LastActiveAt.Format(time.RFC3339Nano),
	}
}

func participantFromStateMap(values map[string]any) externalParticipant {
	p := externalParticipant{
		Alias:          strings.TrimSpace(asString(values["alias"])),
		AgentID:        strings.TrimSpace(asString(values["agent_id"])),
		ChildSessionID: strings.TrimSpace(asString(values["child_session_id"])),
		DisplayLabel:   strings.TrimSpace(asString(values["display_label"])),
		Status:         strings.TrimSpace(asString(values["status"])),
	}
	if p.DisplayLabel == "" {
		p.DisplayLabel = participantDisplayLabel(p.Alias, p.AgentID)
	}
	if createdAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(asString(values["created_at"]))); err == nil {
		p.CreatedAt = createdAt
	}
	if lastActiveAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(asString(values["last_active_at"]))); err == nil {
		p.LastActiveAt = lastActiveAt
	}
	return p
}

func participantIsUsable(p externalParticipant) bool {
	return normalizeParticipantAlias(p.Alias) != "" && strings.TrimSpace(p.ChildSessionID) != ""
}

func (c *cliConsole) ensureSessionRecord(ctx context.Context, sessionID string) (*session.Session, error) {
	if c == nil || c.sessionStore == nil {
		return nil, fmt.Errorf("session store unavailable")
	}
	sess := &session.Session{AppName: c.appName, UserID: c.userID, ID: strings.TrimSpace(sessionID)}
	if sess.ID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	return c.sessionStore.GetOrCreate(ctx, sess)
}

func (c *cliConsole) childSessionRecord(childSessionID string) *session.Session {
	if c == nil {
		return nil
	}
	return &session.Session{AppName: c.appName, UserID: c.userID, ID: strings.TrimSpace(childSessionID)}
}

func (c *cliConsole) loadSessionParticipants(ctx context.Context) ([]externalParticipant, error) {
	if c == nil || c.sessionStore == nil || strings.TrimSpace(c.sessionID) == "" {
		return nil, nil
	}
	state, err := c.sessionStore.SnapshotState(ctx, c.currentSessionRef())
	if err != nil && err != session.ErrSessionNotFound {
		return nil, err
	}
	raw, _ := state[externalParticipantsStateKey].([]any)
	out := make([]externalParticipant, 0, len(raw))
	for _, item := range raw {
		values, ok := item.(map[string]any)
		if !ok {
			continue
		}
		p := participantFromStateMap(values)
		if !participantIsUsable(p) {
			continue
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b externalParticipant) int {
		return strings.Compare(normalizeParticipantAlias(a.Alias), normalizeParticipantAlias(b.Alias))
	})
	return out, nil
}

func (c *cliConsole) updateSessionParticipants(ctx context.Context, fn func([]externalParticipant) ([]externalParticipant, error)) error {
	if c == nil || c.sessionStore == nil {
		return fmt.Errorf("session store unavailable")
	}
	if _, err := c.ensureSessionRecord(ctx, c.sessionID); err != nil {
		return err
	}
	updater, ok := c.sessionStore.(session.StateUpdateStore)
	if !ok {
		current, err := c.loadSessionParticipants(ctx)
		if err != nil {
			return err
		}
		next, err := fn(current)
		if err != nil {
			return err
		}
		state, err := c.sessionStore.SnapshotState(ctx, c.currentSessionRef())
		if err != nil && err != session.ErrSessionNotFound {
			return err
		}
		if state == nil {
			state = map[string]any{}
		}
		state[externalParticipantsStateKey] = marshalParticipantsState(next)
		return c.sessionStore.ReplaceState(ctx, c.currentSessionRef(), state)
	}
	return updater.UpdateState(ctx, c.currentSessionRef(), func(values map[string]any) (map[string]any, error) {
		current := unmarshalParticipantsState(values)
		next, err := fn(current)
		if err != nil {
			return nil, err
		}
		if values == nil {
			values = map[string]any{}
		}
		values[externalParticipantsStateKey] = marshalParticipantsState(next)
		return values, nil
	})
}

func marshalParticipantsState(items []externalParticipant) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		if !participantIsUsable(item) {
			continue
		}
		if item.DisplayLabel == "" {
			item.DisplayLabel = participantDisplayLabel(item.Alias, item.AgentID)
		}
		out = append(out, participantStateMap(item))
	}
	return out
}

func unmarshalParticipantsState(values map[string]any) []externalParticipant {
	if len(values) == 0 {
		return nil
	}
	raw, _ := values[externalParticipantsStateKey].([]any)
	out := make([]externalParticipant, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		p := participantFromStateMap(entry)
		if participantIsUsable(p) {
			out = append(out, p)
		}
	}
	return out
}

func (c *cliConsole) registerExternalParticipant(ctx context.Context, p externalParticipant) error {
	p.Alias = normalizeParticipantAlias(p.Alias)
	p.AgentID = strings.TrimSpace(p.AgentID)
	p.ChildSessionID = strings.TrimSpace(p.ChildSessionID)
	p.DisplayLabel = participantDisplayLabel(p.Alias, p.AgentID)
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	if p.LastActiveAt.IsZero() {
		p.LastActiveAt = p.CreatedAt
	}
	return c.updateSessionParticipants(ctx, func(items []externalParticipant) ([]externalParticipant, error) {
		out := make([]externalParticipant, 0, len(items)+1)
		replaced := false
		for _, item := range items {
			if strings.EqualFold(item.ChildSessionID, p.ChildSessionID) {
				out = append(out, p)
				replaced = true
				continue
			}
			out = append(out, item)
		}
		if !replaced {
			out = append(out, p)
		}
		return out, nil
	})
}

func (c *cliConsole) updateParticipantStatus(ctx context.Context, childSessionID string, status string) error {
	childSessionID = strings.TrimSpace(childSessionID)
	if childSessionID == "" {
		return nil
	}
	return c.updateSessionParticipants(ctx, func(items []externalParticipant) ([]externalParticipant, error) {
		out := append([]externalParticipant(nil), items...)
		for i := range out {
			if !strings.EqualFold(out[i].ChildSessionID, childSessionID) {
				continue
			}
			out[i].Status = strings.TrimSpace(status)
			out[i].LastActiveAt = time.Now()
		}
		return out, nil
	})
}

func (c *cliConsole) lookupParticipantByAlias(ctx context.Context, alias string) (externalParticipant, bool, error) {
	alias = normalizeParticipantAlias(alias)
	if alias == "" {
		return externalParticipant{}, false, nil
	}
	items, err := c.loadSessionParticipants(ctx)
	if err != nil {
		return externalParticipant{}, false, err
	}
	for _, item := range items {
		if normalizeParticipantAlias(item.Alias) == alias {
			return item, true, nil
		}
	}
	return externalParticipant{}, false, nil
}

func (c *cliConsole) participantAliases(query string, limit int) ([]string, error) {
	items, err := c.loadSessionParticipants(context.Background())
	if err != nil {
		return nil, err
	}
	query = normalizeParticipantAlias(query)
	out := make([]string, 0, minInt(limit, len(items)))
	for _, item := range items {
		label := participantDisplayLabel(item.Alias, item.AgentID)
		if query != "" && !strings.Contains(strings.ToLower(label), query) && !strings.Contains(normalizeParticipantAlias(item.Alias), query) {
			continue
		}
		out = append(out, label)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func nextExternalParticipantAlias(rootSessionID string, agentID string, existing []externalParticipant) string {
	used := map[string]struct{}{}
	for _, item := range existing {
		used[normalizeParticipantAlias(item.Alias)] = struct{}{}
	}
	offset := 0
	if seed := strings.TrimSpace(rootSessionID + ":" + agentID); seed != "" {
		for _, r := range seed {
			offset += int(r)
		}
	}
	if len(participantAliasPool) > 0 {
		offset %= len(participantAliasPool)
		for i := 0; i < len(participantAliasPool); i++ {
			alias := normalizeParticipantAlias(participantAliasPool[(offset+i)%len(participantAliasPool)])
			if _, exists := used[alias]; !exists {
				return alias
			}
		}
	}
	base := normalizeParticipantAlias(agentID)
	if base == "" {
		base = "agent"
	}
	for i := 1; ; i++ {
		alias := fmt.Sprintf("%s%d", base, i)
		if _, exists := used[alias]; !exists {
			return alias
		}
	}
}

func annotateParticipantEvent(ev *session.Event, p externalParticipant) *session.Event {
	if ev == nil {
		return nil
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	ev.Meta[metaParticipantAlias] = normalizeParticipantAlias(p.Alias)
	ev.Meta[metaParticipantAgent] = strings.TrimSpace(p.AgentID)
	ev.Meta[metaParticipantDisplay] = participantDisplayLabel(p.Alias, p.AgentID)
	ev.Meta[metaChildSessionID] = strings.TrimSpace(p.ChildSessionID)
	return session.EnsureEventType(ev)
}

func annotateChildParticipantEvent(ev *session.Event, rootSessionID string, p externalParticipant) *session.Event {
	ev = annotateParticipantEvent(ev, p)
	if ev == nil {
		return nil
	}
	ev.Meta[metaParentSessionID] = strings.TrimSpace(rootSessionID)
	ev.Meta[metaChildSessionID] = strings.TrimSpace(p.ChildSessionID)
	ev.Meta[metaParticipantSessionKind] = "external_agent"
	return ev
}

func mirrorParticipantEvent(ev *session.Event, rootSessionID string, p externalParticipant, sourceEventID string) *session.Event {
	ev = annotateParticipantEvent(ev, p)
	if ev == nil {
		return nil
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	ev.Meta[metaMirrorSourceSessionID] = strings.TrimSpace(p.ChildSessionID)
	ev.Meta[metaMirrorSourceEventID] = strings.TrimSpace(sourceEventID)
	ev.Meta[metaParentSessionID] = strings.TrimSpace(rootSessionID)
	return session.MarkMirror(ev)
}

func routeMirrorUserEvent(routeText string, p externalParticipant, routeKind string) *session.Event {
	ev := &session.Event{
		Message: model.NewTextMessage(model.RoleUser, strings.TrimSpace(routeText)),
		Meta: map[string]any{
			metaRouteKind: strings.TrimSpace(routeKind),
		},
	}
	return session.MarkMirror(annotateParticipantEvent(ev, p))
}

func finalizeParticipantSessionRef(root string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return root
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return value
}
