package runtime

import (
	"context"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/task"
)

func (m *runtimeTaskManager) persistRecord(ctx context.Context, record *task.Record) error {
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

func (m *runtimeTaskManager) ensureRecord(ctx context.Context, taskID string) (*task.Record, error) {
	record, err := m.registry.Get(taskID)
	if err == nil {
		return record, nil
	}
	if m == nil || m.store == nil {
		return nil, err
	}
	entry, storeErr := m.store.Get(ctx, taskID)
	if storeErr != nil {
		return nil, storeErr
	}
	record = entryToRecord(entry)
	record.Backend = m.rebuildController(entry)
	m.registry.Put(record)
	return record, nil
}

func (m *runtimeTaskManager) rebuildController(entry *task.Entry) task.Controller {
	if entry == nil {
		return nil
	}
	switch entry.Kind {
	case task.KindBash:
		runner, ok := asyncBashRunnerForRoute(m.execenv, stringValue(entry.Spec, taskSpecRoute))
		if !ok || runner == nil {
			return nil
		}
		return &bashTaskController{
			runner:    runner,
			sessionID: stringValue(entry.Spec, taskSpecExecSessionID),
			command:   stringValue(entry.Spec, taskSpecCommand),
			workdir:   stringValue(entry.Spec, taskSpecWorkdir),
			tty:       boolValue(entry.Spec, taskSpecTTY),
			route:     stringValue(entry.Spec, taskSpecRoute),
			store:     m.store,
		}
	case task.KindSpawn:
		return &subagentTaskController{
			runtime:      m.runtime,
			appName:      entry.Session.AppName,
			userID:       entry.Session.UserID,
			sessionID:    stringValue(entry.Spec, taskSpecChildSession),
			delegationID: stringValue(entry.Spec, taskSpecDelegationID),
			runner:       m.subagents,
			store:        m.store,
			agent:        stringValueFallback(entry.Spec, taskSpecAgent, entry.Result),
			childCWD:     stringValueFallback(entry.Spec, taskSpecChildCWD, entry.Result),
			timeout:      time.Duration(intValue(entry.Spec, taskSpecTimeout)) * time.Second,
			idleTimeout:  time.Duration(intValue(entry.Spec, taskSpecIdleTimeout)) * time.Second,
		}
	default:
		return nil
	}
}

func entryToRecord(entry *task.Entry) *task.Record {
	if entry == nil {
		return nil
	}
	record := &task.Record{
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
	return record
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

func intValue(values map[string]any, key string) int {
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
