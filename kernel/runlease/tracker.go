package runlease

import (
	"strings"
	"sync"
)

type Tracker struct {
	mu     sync.Mutex
	active map[string]struct{}
}

func New() *Tracker {
	return &Tracker{active: map[string]struct{}{}}
}

func Key(appName, userID, sessionID string) string {
	return strings.TrimSpace(appName) + "\x00" + strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(sessionID)
}

func (t *Tracker) Acquire(key string) bool {
	if t == nil || strings.TrimSpace(key) == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active == nil {
		t.active = map[string]struct{}{}
	}
	if _, exists := t.active[key]; exists {
		return false
	}
	t.active[key] = struct{}{}
	return true
}

func (t *Tracker) Release(key string) {
	if t == nil || strings.TrimSpace(key) == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.active, key)
}

func (t *Tracker) Has(key string) bool {
	if t == nil || strings.TrimSpace(key) == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.active[key]
	return ok
}
