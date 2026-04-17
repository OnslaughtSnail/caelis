package execenv

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const hostBackendName = "host"

type commandBackend struct {
	name   string
	kind   BackendKind
	runner CommandRunner
	async  AsyncCommandRunner
	health BackendHealth
}

func newCommandBackend(name string, kind BackendKind, runner CommandRunner) *commandBackend {
	name = strings.TrimSpace(name)
	if name == "" {
		name = string(kind)
	}
	async, _ := runner.(AsyncCommandRunner)
	return &commandBackend{
		name:   name,
		kind:   kind,
		runner: runner,
		async:  async,
		health: BackendHealth{Ready: runner != nil},
	}
}

func (b *commandBackend) Name() string { return b.name }

func (b *commandBackend) Kind() BackendKind { return b.kind }

func (b *commandBackend) Capabilities() BackendCapabilities {
	return BackendCapabilities{Async: b.async != nil}
}

func (b *commandBackend) Health(context.Context) BackendHealth {
	if b == nil {
		return BackendHealth{Message: "backend is unavailable"}
	}
	if b.health.Ready {
		return b.health
	}
	return BackendHealth{Message: cmp.Or(strings.TrimSpace(b.health.Message), "backend is unavailable")}
}

func (b *commandBackend) Execute(ctx context.Context, req CommandRequest) (CommandResult, error) {
	if b == nil || b.runner == nil {
		return CommandResult{}, fmt.Errorf("execenv: backend %q is unavailable", b.name)
	}
	return b.runner.Run(ctx, req)
}

func (b *commandBackend) Start(ctx context.Context, req CommandRequest) (Session, error) {
	if b == nil || b.async == nil {
		return nil, fmt.Errorf("execenv: backend %q does not support async execution", b.name)
	}
	sessionID, err := b.async.StartAsync(ctx, req)
	if err != nil {
		return nil, err
	}
	return &commandSession{
		ref:    CommandSessionRef{Backend: b.name, SessionID: sessionID},
		runner: b.async,
	}, nil
}

func (b *commandBackend) OpenSession(sessionID string) (Session, error) {
	if b == nil || b.async == nil {
		return nil, fmt.Errorf("execenv: backend %q does not support async execution", b.name)
	}
	return &commandSession{
		ref:    CommandSessionRef{Backend: b.name, SessionID: strings.TrimSpace(sessionID)},
		runner: b.async,
	}, nil
}

func (b *commandBackend) Close() error {
	if b == nil {
		return nil
	}
	closer, ok := b.runner.(runtimeCloser)
	if !ok {
		return nil
	}
	return closer.Close()
}

type commandSession struct {
	ref    CommandSessionRef
	runner AsyncCommandRunner
}

func (s *commandSession) Ref() CommandSessionRef {
	if s == nil {
		return CommandSessionRef{}
	}
	return s.ref
}

func (s *commandSession) WriteInput(input []byte) error {
	if s == nil || s.runner == nil {
		return fmt.Errorf("execenv: session is unavailable")
	}
	return s.runner.WriteInput(s.ref.SessionID, input)
}

func (s *commandSession) ReadOutput(stdoutMarker, stderrMarker int64) (stdout, stderr []byte, newStdoutMarker, newStderrMarker int64, err error) {
	if s == nil || s.runner == nil {
		return nil, nil, 0, 0, fmt.Errorf("execenv: session is unavailable")
	}
	return s.runner.ReadOutput(s.ref.SessionID, stdoutMarker, stderrMarker)
}

func (s *commandSession) Status() (SessionStatus, error) {
	if s == nil || s.runner == nil {
		return SessionStatus{}, fmt.Errorf("execenv: session is unavailable")
	}
	return s.runner.GetSessionStatus(s.ref.SessionID)
}

func (s *commandSession) Wait(ctx context.Context, timeout time.Duration) (CommandResult, error) {
	if s == nil || s.runner == nil {
		return CommandResult{}, fmt.Errorf("execenv: session is unavailable")
	}
	return s.runner.WaitSession(ctx, s.ref.SessionID, timeout)
}

func (s *commandSession) Terminate() error {
	if s == nil || s.runner == nil {
		return fmt.Errorf("execenv: session is unavailable")
	}
	return s.runner.TerminateSession(s.ref.SessionID)
}

type backendSet struct {
	host      Backend
	sandbox   Backend
	snapshots []BackendSnapshot
	closeOnce sync.Once
	closeErr  error
}

func newBackendSet(host Backend, sandbox Backend) *backendSet {
	snapshots := make([]BackendSnapshot, 0, 2)
	if host != nil {
		snapshots = append(snapshots, BackendSnapshot{
			Name:         host.Name(),
			Kind:         host.Kind(),
			Capabilities: host.Capabilities(),
			Health:       host.Health(context.Background()),
		})
	}
	if sandbox != nil {
		snapshots = append(snapshots, BackendSnapshot{
			Name:         sandbox.Name(),
			Kind:         sandbox.Kind(),
			Capabilities: sandbox.Capabilities(),
			Health:       sandbox.Health(context.Background()),
		})
	}
	return &backendSet{host: host, sandbox: sandbox, snapshots: snapshots}
}

func (s *backendSet) Backend(name string) (Backend, bool) {
	if s == nil {
		return nil, false
	}
	name = strings.TrimSpace(name)
	switch {
	case s.host != nil && strings.EqualFold(s.host.Name(), name):
		return s.host, true
	case s.sandbox != nil && strings.EqualFold(s.sandbox.Name(), name):
		return s.sandbox, true
	default:
		return nil, false
	}
}

func (s *backendSet) DefaultHost() Backend {
	if s == nil {
		return nil
	}
	return s.host
}

func (s *backendSet) DefaultSandbox() (Backend, bool) {
	if s == nil || s.sandbox == nil {
		return nil, false
	}
	return s.sandbox, true
}

func (s *backendSet) Snapshot() []BackendSnapshot {
	if s == nil {
		return nil
	}
	return append([]BackendSnapshot(nil), s.snapshots...)
}

func (s *backendSet) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		var errs []error
		for _, backend := range []Backend{s.sandbox, s.host} {
			closer, ok := backend.(runtimeCloser)
			if !ok || closer == nil {
				continue
			}
			if err := closer.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		s.closeErr = errors.Join(errs...)
	})
	return s.closeErr
}

type runtimeView struct {
	mu                sync.RWMutex
	permissionMode    PermissionMode
	requestedSandbox  string
	baseSandboxPolicy SandboxPolicy
	diagnostics       SandboxDiagnostics
	fs                FileSystem
	backends          *backendSet
}

func newRuntimeView(cfg Config) (*runtimeView, error) {
	mode, err := normalizePermissionMode(cfg.PermissionMode)
	if err != nil {
		return nil, err
	}

	filesystem := cfg.FileSystem
	if filesystem == nil {
		filesystem = newHostFileSystem()
	}
	hostRunner := cfg.HostRunner
	if hostRunner == nil {
		hostRunner = newHostRunner()
	}
	hostBackend := newCommandBackend(hostBackendName, BackendKindHost, hostRunner)
	var (
		sandboxBackend Backend
		diagnostics    SandboxDiagnostics
	)
	selectCfg := cfg
	selectCfg.PermissionMode = PermissionModeDefault
	sandboxRunner, diagnostics, err := SelectSandbox(selectCfg)
	if err != nil {
		if mode != PermissionModeFullControl || !IsErrorCode(err, ErrorCodeSandboxUnsupported) {
			return nil, err
		}
		diagnostics = SandboxDiagnostics{
			RequestedType:  strings.TrimSpace(strings.ToLower(cfg.SandboxType)),
			Candidates:     sandboxTypeCandidates(strings.TrimSpace(strings.ToLower(cfg.SandboxType))),
			FallbackToHost: true,
			FallbackReason: err.Error(),
			Failures:       []string{err.Error()},
		}
	}
	if sandboxRunner != nil {
		name := cmp.Or(strings.TrimSpace(diagnostics.ResolvedType), strings.TrimSpace(cfg.SandboxType), "sandbox")
		sandboxBackend = newCommandBackend(name, BackendKindSandbox, sandboxRunner)
	}
	rt := &runtimeView{
		permissionMode:    mode,
		requestedSandbox:  strings.TrimSpace(strings.ToLower(cfg.SandboxType)),
		baseSandboxPolicy: cloneSandboxPolicy(cfg.SandboxPolicy),
		diagnostics:       diagnostics,
		fs:                filesystem,
		backends:          newBackendSet(hostBackend, sandboxBackend),
	}
	rt.fs = newPolicyFileSystem(filesystem, rt.SandboxPolicy)
	return rt, nil
}

func (r *runtimeView) PermissionMode() PermissionMode {
	if r == nil {
		return PermissionModeDefault
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cmp.Or(r.permissionMode, PermissionModeDefault)
}

func (r *runtimeView) SetPermissionMode(mode PermissionMode) error {
	normalized, err := normalizePermissionMode(mode)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.permissionMode = normalized
	r.mu.Unlock()
	return nil
}

func (r *runtimeView) SandboxType() string {
	state := r.State()
	return cmp.Or(state.ResolvedSandbox, state.RequestedSandbox)
}

func (r *runtimeView) SandboxPolicy() SandboxPolicy {
	base := SandboxPolicy{}
	if r != nil {
		r.mu.RLock()
		base = cloneSandboxPolicy(r.baseSandboxPolicy)
		r.mu.RUnlock()
	}
	return deriveSandboxPolicy(r.PermissionMode(), base)
}

func (r *runtimeView) FallbackToHost() bool {
	return r.State().SandboxStatus == SandboxStatusFallback
}

func (r *runtimeView) FallbackReason() string {
	return r.State().FallbackReason
}

func (r *runtimeView) Diagnostics() SandboxDiagnostics {
	if r == nil {
		return SandboxDiagnostics{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.permissionMode == PermissionModeFullControl {
		return SandboxDiagnostics{}
	}
	return cloneSandboxDiagnostics(r.diagnostics)
}

func (r *runtimeView) FileSystem() FileSystem {
	if r == nil {
		return nil
	}
	return r.fs
}

func (r *runtimeView) Decide(ctx context.Context, req RouteRequest) (CommandDecision, error) {
	_ = ctx
	if r == nil || r.backends == nil {
		return CommandDecision{}, fmt.Errorf("execenv: runtime is unavailable")
	}
	var sandboxName string
	if sandboxBackend, ok := r.backends.DefaultSandbox(); ok {
		sandboxName = sandboxBackend.Name()
	}
	hostName := hostBackendName
	if hostBackend := r.backends.DefaultHost(); hostBackend != nil {
		hostName = hostBackend.Name()
	}
	return decideRoute(r.PermissionMode(), r.Diagnostics(), hostName, sandboxName, req.Command, req.SandboxPermission), nil
}

func (r *runtimeView) DecideRoute(command string, sandboxPermission SandboxPermission) CommandDecision {
	decision, _ := r.Decide(context.Background(), RouteRequest{
		Command:           command,
		SandboxPermission: sandboxPermission,
	})
	return decision
}

func (r *runtimeView) Execute(ctx context.Context, req CommandRequest) (CommandResult, error) {
	backend, _, err := r.resolveBackend(ctx, req)
	if err != nil {
		return CommandResult{}, err
	}
	return backend.Execute(ctx, req)
}

func (r *runtimeView) Start(ctx context.Context, req CommandRequest) (Session, error) {
	backend, resolvedReq, err := r.resolveBackend(ctx, req)
	if err != nil {
		return nil, err
	}
	return backend.Start(ctx, resolvedReq)
}

func (r *runtimeView) OpenSession(ref CommandSessionRef) (Session, error) {
	if r == nil || r.backends == nil {
		return nil, fmt.Errorf("execenv: runtime is unavailable")
	}
	sessionID := strings.TrimSpace(ref.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("execenv: session id is required")
	}
	backendName := strings.TrimSpace(ref.Backend)
	if backendName == "" {
		backendName = hostBackendName
	}
	backend, ok := r.backends.Backend(backendName)
	if !ok {
		return nil, fmt.Errorf("execenv: backend %q is unavailable", backendName)
	}
	return backend.OpenSession(sessionID)
}

func (r *runtimeView) State() RuntimeState {
	if r == nil {
		return RuntimeState{}
	}
	r.mu.RLock()
	mode := cmp.Or(r.permissionMode, PermissionModeDefault)
	requested := r.requestedSandbox
	diagnostics := cloneSandboxDiagnostics(r.diagnostics)
	r.mu.RUnlock()
	state := RuntimeState{
		Mode:             mode,
		RequestedSandbox: requested,
		ResolvedSandbox:  diagnostics.ResolvedType,
		Backends:         r.backends.Snapshot(),
		RouterState:      RouterState{Diagnostics: diagnostics},
	}
	switch {
	case mode == PermissionModeFullControl:
		state.SandboxStatus = SandboxStatusReady
	case diagnostics.FallbackToHost:
		state.SandboxStatus = SandboxStatusFallback
		state.FallbackReason = diagnostics.FallbackReason
	case diagnostics.ResolvedType != "":
		state.SandboxStatus = SandboxStatusReady
	default:
		state.SandboxStatus = SandboxStatusUnavailable
	}
	return state
}

func (r *runtimeView) Close() error {
	if r == nil || r.backends == nil {
		return nil
	}
	return r.backends.Close()
}

func (r *runtimeView) resolveBackend(ctx context.Context, req CommandRequest) (Backend, CommandRequest, error) {
	if r == nil || r.backends == nil {
		return nil, CommandRequest{}, fmt.Errorf("execenv: runtime is unavailable")
	}
	backendName := strings.TrimSpace(req.BackendName)
	if backendName == "" {
		decision, err := r.Decide(ctx, RouteRequest{
			Command:           req.Command,
			SandboxPermission: req.SandboxPermission,
		})
		if err != nil {
			return nil, CommandRequest{}, err
		}
		backendName = decision.Backend
		if req.RouteHint == "" {
			req.RouteHint = decision.Route
		}
	}
	if backendName == "" {
		switch req.RouteHint {
		case ExecutionRouteHost:
			backendName = hostBackendName
		case ExecutionRouteSandbox:
			if sandbox, ok := r.backends.DefaultSandbox(); ok {
				backendName = sandbox.Name()
			} else {
				backendName = "sandbox"
			}
		}
	}
	backend, ok := r.backends.Backend(backendName)
	if !ok {
		return nil, CommandRequest{}, fmt.Errorf("execenv: backend %q is unavailable", backendName)
	}
	req.BackendName = backendName
	if req.RouteHint == "" {
		switch backend.Kind() {
		case BackendKindHost:
			req.RouteHint = ExecutionRouteHost
		case BackendKindSandbox:
			req.RouteHint = ExecutionRouteSandbox
		}
	}
	if backend.Kind() == BackendKindSandbox && req.SandboxPolicyOverride == nil {
		policy := r.SandboxPolicy()
		req.SandboxPolicyOverride = &policy
	}
	return backend, req, nil
}
