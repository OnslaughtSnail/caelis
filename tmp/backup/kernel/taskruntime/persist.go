package taskruntime

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/task"
)

func (m *Manager) persistRecord(ctx context.Context, record *task.Record) error {
	if m == nil || m.store == nil || record == nil {
		return nil
	}
	var entry *task.Entry
	record.WithLock(func(one *task.Record) {
		entry = &task.Entry{
			TaskID:         one.ID,
			Kind:           one.Kind,
			Session:        one.Session,
			Title:          one.Title,
			State:          one.State,
			Running:        one.Running,
			SupportsInput:  one.SupportsInput,
			SupportsCancel: one.SupportsCancel,
			CreatedAt:      one.CreatedAt,
			UpdatedAt:      one.UpdatedAt,
			HeartbeatAt:    one.UpdatedAt,
			StdoutCursor:   one.StdoutCursor,
			StderrCursor:   one.StderrCursor,
			EventCursor:    one.EventCursor,
			Spec:           task.CloneEntry(&task.Entry{Spec: one.Spec}).Spec,
			Result:         task.CloneEntry(&task.Entry{Result: one.Result}).Result,
		}
	})
	if entry == nil {
		return nil
	}
	return m.store.Upsert(ctx, entry)
}

func (m *Manager) ensureRecord(ctx context.Context, taskID string) (*task.Record, error) {
	record, err := m.registry.Get(taskID)
	if err == nil {
		if !m.taskBelongsToCurrentSession(record) {
			return nil, task.ErrTaskNotFound
		}
		return record, nil
	}
	if m == nil || m.store == nil {
		return nil, err
	}
	entry, storeErr := m.store.Get(ctx, taskID)
	if storeErr != nil {
		if errors.Is(storeErr, task.ErrTaskNotFound) {
			return nil, task.ErrTaskNotFound
		}
		return nil, storeErr
	}
	record = entryToRecord(entry)
	if !m.taskBelongsToCurrentSession(record) {
		return nil, task.ErrTaskNotFound
	}
	record.Backend = m.rebuildController(entry)
	m.registry.Put(record)
	return record, nil
}

func (m *Manager) taskBelongsToCurrentSession(record *task.Record) bool {
	if m == nil || record == nil {
		return false
	}
	if m.session == nil {
		return true
	}
	return SameTaskSession(record.Session, *m.session)
}

func (m *Manager) rebuildController(entry *task.Entry) task.Controller {
	if entry == nil {
		return nil
	}
	switch entry.Kind {
	case task.KindBash:
		backendName := RecoverBashBackendName(entry.Spec, entry.Result, m.execenv)
		sessionRef, err := OpenBashSession(
			m.execenv,
			backendName,
			StringValue(entry.Spec, SpecExecSessionID),
		)
		if err != nil {
			return nil
		}
		return &BashTaskController{
			Session: sessionRef,
			Command: StringValue(entry.Spec, SpecCommand),
			Workdir: StringValue(entry.Spec, SpecWorkdir),
			TTY:     BoolValue(entry.Spec, SpecTTY),
			Route:   StringValue(entry.Spec, SpecRoute),
			Backend: cmp.Or(strings.TrimSpace(backendName), strings.TrimSpace(sessionRef.Ref().Backend)),
			Store:   m.store,
		}
	case task.KindSpawn:
		return &SubagentTaskController{
			SessionID:              StringValue(entry.Spec, SpecChildSession),
			DelegationID:           StringValue(entry.Spec, SpecDelegationID),
			Runner:                 m.subagents,
			ContinueRunner:         m.continueSubagentRunner,
			Store:                  m.store,
			Agent:                  StringValue(entry.Spec, SpecAgent),
			ChildCWD:               StringValue(entry.Spec, SpecChildCWD),
			IdleTimeout:            time.Duration(IntValue(entry.Spec, SpecIdleTimeout)) * time.Second,
			ContinuationAnchorTool: m.continuationAnchorTool,
		}
	default:
		return nil
	}
}

func RecoverBashBackendName(spec map[string]any, result map[string]any, execRuntime toolexec.Runtime) string {
	if backend := strings.TrimSpace(StringValue(spec, SpecBackend)); backend != "" {
		return backend
	}
	route := cmp.Or(
		strings.TrimSpace(StringValue(spec, SpecRoute)),
		strings.TrimSpace(StringValue(result, "route")),
	)
	sessionID := cmp.Or(
		strings.TrimSpace(StringValue(result, "session_id")),
		strings.TrimSpace(StringValue(spec, SpecExecSessionID)),
	)
	switch route {
	case string(toolexec.ExecutionRouteHost):
		return "host"
	case "", string(toolexec.ExecutionRouteSandbox):
		if UsesLegacyACPTerminalBackend(route, sessionID) {
			return "acp_terminal"
		}
		if execRuntime != nil {
			if resolved := strings.TrimSpace(execRuntime.State().ResolvedSandbox); resolved != "" {
				return resolved
			}
		}
		return "sandbox"
	default:
		return ""
	}
}

func UsesLegacyACPTerminalBackend(route string, sessionID string) bool {
	switch strings.TrimSpace(route) {
	case "", string(toolexec.ExecutionRouteSandbox):
	default:
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(sessionID)), "term-")
}

func SameTaskSession(a task.SessionRef, b task.SessionRef) bool {
	return strings.TrimSpace(a.AppName) == strings.TrimSpace(b.AppName) &&
		strings.TrimSpace(a.UserID) == strings.TrimSpace(b.UserID) &&
		strings.TrimSpace(a.SessionID) == strings.TrimSpace(b.SessionID)
}

func entryToRecord(entry *task.Entry) *task.Record {
	if entry == nil {
		return nil
	}
	return &task.Record{
		ID:             entry.TaskID,
		Kind:           entry.Kind,
		Title:          entry.Title,
		State:          entry.State,
		Running:        entry.Running,
		SupportsInput:  entry.SupportsInput,
		SupportsCancel: entry.SupportsCancel,
		CreatedAt:      entry.CreatedAt,
		UpdatedAt:      entry.UpdatedAt,
		StdoutCursor:   entry.StdoutCursor,
		StderrCursor:   entry.StderrCursor,
		EventCursor:    entry.EventCursor,
		Session:        entry.Session,
		Spec:           task.CloneEntry(&task.Entry{Spec: entry.Spec}).Spec,
		Result:         task.CloneEntry(&task.Entry{Result: entry.Result}).Result,
	}
}

func persistControllerRecord(ctx context.Context, store task.Store, record *task.Record) error {
	if store == nil || record == nil {
		return nil
	}
	var entry *task.Entry
	record.WithLock(func(one *task.Record) {
		entry = &task.Entry{
			TaskID:         one.ID,
			Kind:           one.Kind,
			Session:        one.Session,
			Title:          one.Title,
			State:          one.State,
			Running:        one.Running,
			SupportsInput:  one.SupportsInput,
			SupportsCancel: one.SupportsCancel,
			CreatedAt:      one.CreatedAt,
			UpdatedAt:      one.UpdatedAt,
			HeartbeatAt:    one.UpdatedAt,
			StdoutCursor:   one.StdoutCursor,
			StderrCursor:   one.StderrCursor,
			EventCursor:    one.EventCursor,
			Spec:           task.CloneEntry(&task.Entry{Spec: one.Spec}).Spec,
			Result:         task.CloneEntry(&task.Entry{Result: one.Result}).Result,
		}
	})
	return store.Upsert(ctx, entry)
}

func persistedFinalTaskSnapshot(record *task.Record) (task.Snapshot, bool) {
	if record == nil {
		return task.Snapshot{}, false
	}
	var (
		snapshot task.Snapshot
		ok       bool
	)
	record.WithLock(func(one *task.Record) {
		if one == nil || one.Running {
			return
		}
		switch one.State {
		case task.StateCompleted, task.StateFailed, task.StateCancelled, task.StateInterrupted, task.StateTerminated:
			snapshot = one.LockedSnapshot(task.Output{})
			ok = true
		}
	})
	return snapshot, ok
}

func StringValue(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

func BoolValue(values map[string]any, key string) bool {
	if len(values) == 0 {
		return false
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return false
	}
	value, _ := raw.(bool)
	return value
}

func IntValue(values map[string]any, key string) int {
	if len(values) == 0 {
		return 0
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return 0
	}
	switch value := raw.(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}
