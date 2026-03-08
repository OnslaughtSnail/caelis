package task

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/idutil"
)

type Kind string

const (
	KindBash     Kind = "bash"
	KindDelegate Kind = "delegate"
)

type State string

const (
	StateRunning         State = "running"
	StateWaitingInput    State = "waiting_input"
	StateCompleted       State = "completed"
	StateFailed          State = "failed"
	StateCancelled       State = "cancelled"
	StateInterrupted     State = "interrupted"
	StateTerminated      State = "terminated"
	StateWaitingApproval State = "waiting_approval"
)

var ErrTaskNotFound = errors.New("task: not found")

type Output struct {
	Stdout string
	Stderr string
	Log    string
}

type Snapshot struct {
	TaskID         string
	Kind           Kind
	Title          string
	State          State
	Running        bool
	Yielded        bool
	SupportsInput  bool
	SupportsCancel bool
	Output         Output
	Result         map[string]any
}

type BashStartRequest struct {
	Command     string
	Workdir     string
	Yield       time.Duration
	Timeout     time.Duration
	IdleTimeout time.Duration
	TTY         bool
	Route       string
}

type DelegateStartRequest struct {
	Task  string
	Yield time.Duration
}

type ControlRequest struct {
	TaskID string
	Yield  time.Duration
	Input  string
}

type Manager interface {
	StartBash(context.Context, BashStartRequest) (Snapshot, error)
	StartDelegate(context.Context, DelegateStartRequest) (Snapshot, error)
	Wait(context.Context, ControlRequest) (Snapshot, error)
	Status(context.Context, ControlRequest) (Snapshot, error)
	Write(context.Context, ControlRequest) (Snapshot, error)
	Cancel(context.Context, ControlRequest) (Snapshot, error)
	List(context.Context) ([]Snapshot, error)
}

type SessionRef struct {
	AppName   string
	UserID    string
	SessionID string
}

type Entry struct {
	TaskID         string
	Kind           Kind
	Session        SessionRef
	Title          string
	State          State
	Running        bool
	SupportsInput  bool
	SupportsCancel bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	HeartbeatAt    time.Time
	Spec           map[string]any
	Result         map[string]any
}

type Store interface {
	Upsert(context.Context, *Entry) error
	Get(context.Context, string) (*Entry, error)
	ListSession(context.Context, SessionRef) ([]*Entry, error)
}

type Controller interface {
	Wait(context.Context, *Record, time.Duration) (Snapshot, error)
	Status(context.Context, *Record) (Snapshot, error)
	Write(context.Context, *Record, string, time.Duration) (Snapshot, error)
	Cancel(context.Context, *Record) (Snapshot, error)
}

type Record struct {
	mu sync.Mutex

	ID             string
	Kind           Kind
	Title          string
	State          State
	Running        bool
	SupportsInput  bool
	SupportsCancel bool
	Result         map[string]any
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Session        SessionRef
	Spec           map[string]any

	StdoutCursor int64
	StderrCursor int64
	EventCursor  int

	Backend Controller
}

func (r *Record) WithLock(fn func(*Record)) {
	if r == nil || fn == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(r)
}

func (r *Record) snapshotLocked(output Output) Snapshot {
	return Snapshot{
		TaskID:         r.ID,
		Kind:           r.Kind,
		Title:          r.Title,
		State:          r.State,
		Running:        r.Running,
		SupportsInput:  r.SupportsInput,
		SupportsCancel: r.SupportsCancel,
		Output:         output,
		Result:         cloneMap(r.Result),
	}
}

func (r *Record) LockedSnapshot(output Output) Snapshot {
	if r == nil {
		return Snapshot{}
	}
	return r.snapshotLocked(output)
}

func (r *Record) Snapshot(output Output) Snapshot {
	var snapshot Snapshot
	r.WithLock(func(one *Record) {
		snapshot = one.snapshotLocked(output)
	})
	return snapshot
}

type RegistryConfig struct {
	MaxTasks int
}

type Registry struct {
	mu       sync.RWMutex
	tasks    map[string]*Record
	maxTasks int
}

const defaultMaxTasks = 64

func NewRegistry(cfg RegistryConfig) *Registry {
	maxTasks := cfg.MaxTasks
	if maxTasks <= 0 {
		maxTasks = defaultMaxTasks
	}
	return &Registry{
		tasks:    map[string]*Record{},
		maxTasks: maxTasks,
	}
}

func (r *Registry) Create(kind Kind, title string, backend Controller, supportsInput, supportsCancel bool) *Record {
	record := &Record{
		ID:             idutil.NewTaskID(),
		Kind:           kind,
		Title:          strings.TrimSpace(title),
		State:          StateRunning,
		Running:        true,
		SupportsInput:  supportsInput,
		SupportsCancel: supportsCancel,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Backend:        backend,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[record.ID] = record
	r.compactLocked()
	return record
}

func (r *Registry) Put(record *Record) {
	if r == nil || record == nil || strings.TrimSpace(record.ID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[strings.TrimSpace(record.ID)] = record
	r.compactLocked()
}

func (r *Registry) Get(taskID string) (*Record, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.tasks[strings.TrimSpace(taskID)]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrTaskNotFound, strings.TrimSpace(taskID))
	}
	return record, nil
}

func (r *Registry) Delete(taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tasks, strings.TrimSpace(taskID))
}

func (r *Registry) List() []*Record {
	r.mu.RLock()
	defer r.mu.RUnlock()
	type sortKey struct {
		record    *Record
		updatedAt time.Time
	}
	keys := make([]sortKey, 0, len(r.tasks))
	for _, record := range r.tasks {
		if record == nil {
			continue
		}
		var t time.Time
		record.WithLock(func(one *Record) { t = one.UpdatedAt })
		keys = append(keys, sortKey{record: record, updatedAt: t})
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].updatedAt.After(keys[j].updatedAt)
	})
	out := make([]*Record, len(keys))
	for i, k := range keys {
		out[i] = k.record
	}
	return out
}

func (r *Registry) compactLocked() {
	if len(r.tasks) <= r.maxTasks {
		return
	}
	type compactKey struct {
		id        string
		running   bool
		updatedAt time.Time
	}
	keys := make([]compactKey, 0, len(r.tasks))
	for _, record := range r.tasks {
		if record == nil {
			continue
		}
		var running bool
		var updatedAt time.Time
		record.WithLock(func(one *Record) {
			running = one.Running
			updatedAt = one.UpdatedAt
		})
		keys = append(keys, compactKey{id: record.ID, running: running, updatedAt: updatedAt})
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].running != keys[j].running {
			return !keys[i].running && keys[j].running
		}
		return keys[i].updatedAt.Before(keys[j].updatedAt)
	})
	for len(r.tasks) > r.maxTasks && len(keys) > 0 {
		victim := keys[0]
		keys = keys[1:]
		delete(r.tasks, victim.id)
	}
}

type managerContextKey struct{}

func WithManager(ctx context.Context, manager Manager) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if manager == nil {
		return ctx
	}
	return context.WithValue(ctx, managerContextKey{}, manager)
}

func ManagerFromContext(ctx context.Context) (Manager, bool) {
	if ctx == nil {
		return nil, false
	}
	manager, ok := ctx.Value(managerContextKey{}).(Manager)
	return manager, ok
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func CloneEntry(in *Entry) *Entry {
	if in == nil {
		return nil
	}
	out := *in
	out.Spec = cloneMap(in.Spec)
	out.Result = cloneMap(in.Result)
	return &out
}
