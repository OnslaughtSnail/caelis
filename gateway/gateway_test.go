package gateway

import (
	"context"
	"iter"
	"testing"
	"time"

	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func TestNewRequiresSessionsRuntimeAndResolver(t *testing.T) {
	t.Parallel()

	base := Config{
		Sessions: mockSessionService{},
		Runtime:  mockRuntime{},
		Resolver: staticResolver{},
	}
	cases := []struct {
		name string
		cfg  Config
	}{
		{name: "missing sessions", cfg: Config{Runtime: base.Runtime, Resolver: base.Resolver}},
		{name: "missing runtime", cfg: Config{Sessions: base.Sessions, Resolver: base.Resolver}},
		{name: "missing resolver", cfg: Config{Sessions: base.Sessions, Runtime: base.Runtime}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(tc.cfg); err == nil {
				t.Fatalf("New(%s) error = nil, want non-nil", tc.name)
			}
		})
	}
}

func TestStartSessionDelegatesToSDKSessions(t *testing.T) {
	t.Parallel()

	session := sdksession.Session{
		SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
		},
		CWD: "/tmp/ws",
	}
	svc := staticSessionService{session: session}
	gw, err := New(Config{
		Sessions: svc,
		Runtime:  mockRuntime{},
		Resolver: staticResolver{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	started, err := gw.StartSession(context.Background(), StartSessionRequest{
		AppName: "caelis",
		UserID:  "u",
		Workspace: sdksession.WorkspaceRef{
			Key: "ws",
			CWD: "/tmp/ws",
		},
		PreferredSessionID: "s1",
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if started.SessionID != "s1" || started.CWD != "/tmp/ws" {
		t.Fatalf("StartSession() = %+v", started)
	}
}

func TestLoadSessionDelegatesToSDKSessionsAndBinds(t *testing.T) {
	t.Parallel()

	loaded := sdksession.LoadedSession{
		Session: sdksession.Session{
			SessionRef: sdksession.SessionRef{
				AppName: "caelis", UserID: "u", SessionID: "s2", WorkspaceKey: "ws",
			},
			CWD: "/tmp/ws",
		},
	}
	svc := &recordingSessionService{loadSessionResult: loaded}
	gw, err := New(Config{
		Sessions: svc,
		Runtime:  mockRuntime{},
		Resolver: staticResolver{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got, err := gw.LoadSession(context.Background(), LoadSessionRequest{
		SessionRef: loaded.Session.SessionRef,
		Limit:      32,
		BindingKey: "surface-headless",
	})
	if err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}
	if got.Session.SessionID != "s2" || svc.loadReq.Limit != 32 {
		t.Fatalf("LoadSession() = %+v, loadReq = %+v", got, svc.loadReq)
	}
	current, ok := gw.CurrentSession("surface-headless")
	if !ok || current.SessionID != "s2" {
		t.Fatalf("CurrentSession() = %+v, %v", current, ok)
	}
}

func TestListSessionsDelegatesToSDKSessions(t *testing.T) {
	t.Parallel()

	want := sdksession.SessionList{
		Sessions: []sdksession.SessionSummary{{SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s2", WorkspaceKey: "ws",
		}}},
	}
	svc := &recordingSessionService{listSessionsResult: want}
	gw, err := New(Config{
		Sessions: svc,
		Runtime:  mockRuntime{},
		Resolver: staticResolver{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got, err := gw.ListSessions(context.Background(), ListSessionsRequest{
		AppName:      "caelis",
		UserID:       "u",
		WorkspaceKey: "ws",
		Limit:        5,
	})
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].SessionID != "s2" {
		t.Fatalf("ListSessions() = %+v", got)
	}
	if svc.listReq.Limit != 5 || svc.listReq.WorkspaceKey != "ws" {
		t.Fatalf("listReq = %+v", svc.listReq)
	}
}

func TestResumeSessionUsesMostRecentExcludingCurrentBinding(t *testing.T) {
	t.Parallel()

	current := sdksession.Session{
		SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
		},
		CWD: "/tmp/ws",
	}
	next := sdksession.LoadedSession{
		Session: sdksession.Session{
			SessionRef: sdksession.SessionRef{
				AppName: "caelis", UserID: "u", SessionID: "s2", WorkspaceKey: "ws",
			},
			CWD: "/tmp/ws",
		},
	}
	svc := &recordingSessionService{
		startSessionResult: current,
		loadSessionResult:  next,
		listSessionsResult: sdksession.SessionList{
			Sessions: []sdksession.SessionSummary{
				{SessionRef: current.SessionRef, UpdatedAt: time.Unix(200, 0)},
				{SessionRef: next.Session.SessionRef, UpdatedAt: time.Unix(100, 0)},
			},
		},
	}
	gw, err := New(Config{
		Sessions: svc,
		Runtime:  mockRuntime{},
		Resolver: staticResolver{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := gw.StartSession(context.Background(), StartSessionRequest{
		AppName:    "caelis",
		UserID:     "u",
		Workspace:  sdksession.WorkspaceRef{Key: "ws", CWD: "/tmp/ws"},
		BindingKey: "surface-1",
	}); err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}

	loaded, err := gw.ResumeSession(context.Background(), ResumeSessionRequest{
		AppName:    "caelis",
		UserID:     "u",
		Workspace:  sdksession.WorkspaceRef{Key: "ws"},
		BindingKey: "surface-1",
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if loaded.Session.SessionID != "s2" || svc.loadReq.SessionRef.SessionID != "s2" {
		t.Fatalf("ResumeSession() = %+v, loadReq = %+v", loaded, svc.loadReq)
	}
	currentRef, ok := gw.CurrentSession("surface-1")
	if !ok || currentRef.SessionID != "s2" {
		t.Fatalf("CurrentSession() = %+v, %v", currentRef, ok)
	}
}

func TestResumeSessionResolvesUniquePrefix(t *testing.T) {
	t.Parallel()

	target := sdksession.LoadedSession{
		Session: sdksession.Session{
			SessionRef: sdksession.SessionRef{
				AppName: "caelis", UserID: "u", SessionID: "s-12345678", WorkspaceKey: "ws",
			},
		},
	}
	svc := &recordingSessionService{
		loadSessionResult: target,
		listSessionsResult: sdksession.SessionList{
			Sessions: []sdksession.SessionSummary{
				{SessionRef: target.Session.SessionRef},
				{SessionRef: sdksession.SessionRef{AppName: "caelis", UserID: "u", SessionID: "s-87654321", WorkspaceKey: "ws"}},
			},
		},
	}
	gw, err := New(Config{
		Sessions: svc,
		Runtime:  mockRuntime{},
		Resolver: staticResolver{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := gw.ResumeSession(context.Background(), ResumeSessionRequest{
		AppName:   "caelis",
		UserID:    "u",
		Workspace: sdksession.WorkspaceRef{Key: "ws"},
		SessionID: "s-1234",
	}); err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if svc.loadReq.SessionRef.SessionID != "s-12345678" {
		t.Fatalf("loadReq = %+v", svc.loadReq)
	}
}

func TestForkSessionCopiesSourceMetadataAndBinds(t *testing.T) {
	t.Parallel()

	source := sdksession.Session{
		SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
		},
		CWD:      "/tmp/ws",
		Title:    "Original",
		Metadata: map[string]any{"mode": "main"},
	}
	forked := sdksession.Session{
		SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s2", WorkspaceKey: "ws",
		},
		CWD: "/tmp/ws",
	}
	svc := &recordingSessionService{
		sessionResult:      source,
		startSessionResult: forked,
	}
	gw, err := New(Config{
		Sessions: svc,
		Runtime:  mockRuntime{},
		Resolver: staticResolver{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	started, err := gw.ForkSession(context.Background(), ForkSessionRequest{
		SourceSessionRef: source.SessionRef,
		BindingKey:       "surface-fork",
		Metadata:         map[string]any{"mode": "fork"},
	})
	if err != nil {
		t.Fatalf("ForkSession() error = %v", err)
	}
	if started.SessionID != "s2" || svc.startReq.AppName != "caelis" || svc.startReq.Title != "Original" {
		t.Fatalf("ForkSession() started=%+v startReq=%+v", started, svc.startReq)
	}
	if got := svc.startReq.Metadata["forked_from_session_id"]; got != "s1" {
		t.Fatalf("fork metadata = %+v", svc.startReq.Metadata)
	}
	current, ok := gw.CurrentSession("surface-fork")
	if !ok || current.SessionID != "s2" {
		t.Fatalf("CurrentSession() = %+v, %v", current, ok)
	}
}

func TestInterruptCancelsActiveRunByBinding(t *testing.T) {
	t.Parallel()

	session := sdksession.Session{
		SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
		},
		CWD: "/tmp/ws",
	}
	rt := &cancellableRuntime{session: session, cancelled: make(chan struct{})}
	svc := &recordingSessionService{startSessionResult: session, sessionResult: session}
	gw, err := New(Config{
		Sessions: svc,
		Runtime:  rt,
		Resolver: staticResolver{resolved: ResolvedTurn{}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := gw.StartSession(context.Background(), StartSessionRequest{
		AppName:    "caelis",
		UserID:     "u",
		Workspace:  sdksession.WorkspaceRef{Key: "ws", CWD: "/tmp/ws"},
		BindingKey: "surface-1",
	}); err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if _, err := gw.BeginTurn(context.Background(), BeginTurnRequest{
		SessionRef: session.SessionRef,
		Input:      "hello",
	}); err != nil {
		t.Fatalf("BeginTurn() error = %v", err)
	}

	if err := gw.Interrupt(context.Background(), InterruptRequest{BindingKey: "surface-1"}); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	select {
	case <-rt.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("Interrupt() did not cancel runtime context")
	}
}

func TestBeginTurnRejectsSecondActiveRunForSameSession(t *testing.T) {
	t.Parallel()

	session := sdksession.Session{
		SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
		},
	}
	rt := &blockingRuntime{session: session}
	gw, err := New(Config{
		Sessions: staticSessionService{session: session},
		Runtime:  rt,
		Resolver: staticResolver{resolved: ResolvedTurn{}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, err := gw.BeginTurn(context.Background(), BeginTurnRequest{
		SessionRef: session.SessionRef,
		Input:      "first",
	})
	if err != nil {
		t.Fatalf("BeginTurn(first) error = %v", err)
	}
	defer first.Handle.Close()

	_, err = gw.BeginTurn(context.Background(), BeginTurnRequest{
		SessionRef: session.SessionRef,
		Input:      "second",
	})
	if err == nil {
		t.Fatal("BeginTurn(second) error = nil, want conflict")
	}
	var gwErr *Error
	if !As(err, &gwErr) || gwErr.Code != CodeActiveRunConflict {
		t.Fatalf("BeginTurn(second) error = %v, want active run conflict", err)
	}
}

func TestBeginTurnPassesIntentToResolver(t *testing.T) {
	t.Parallel()

	session := sdksession.Session{
		SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
		},
	}
	resolver := &recordingResolver{resolved: ResolvedTurn{}}
	gw, err := New(Config{
		Sessions: staticSessionService{session: session},
		Runtime:  mockRuntime{},
		Resolver: resolver,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := gw.BeginTurn(context.Background(), BeginTurnRequest{
		SessionRef: session.SessionRef,
		Input:      "hello",
		ModeName:   "main",
		ModelHint:  "mini",
		Surface:    "headless",
	}); err != nil {
		t.Fatalf("BeginTurn() error = %v", err)
	}

	if resolver.lastIntent.ModeName != "main" || resolver.lastIntent.ModelHint != "mini" || resolver.lastIntent.Surface != "headless" {
		t.Fatalf("resolver intent = %+v, want propagated fields", resolver.lastIntent)
	}
}

func TestBeginTurnLoadsSessionResolvesIntentRunsRuntimeAndPublishesEvents(t *testing.T) {
	t.Parallel()

	session := sdksession.Session{
		SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
		},
	}
	runner := &recordingRunner{
		events: []*sdksession.Event{{ID: "e1", Type: sdksession.EventTypeAssistant}},
	}
	rt := &recordingRuntime{
		session: session,
		result:  sdkruntime.RunResult{Session: session, Handle: runner},
	}
	resolver := &recordingResolver{resolved: ResolvedTurn{
		RunRequest: sdkruntime.RunRequest{Input: "hello"},
	}}
	gw, err := New(Config{
		Sessions: staticSessionService{session: session},
		Runtime:  rt,
		Resolver: resolver,
		Clock: func() time.Time {
			return time.Unix(100, 0)
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := gw.BeginTurn(context.Background(), BeginTurnRequest{
		SessionRef: session.SessionRef,
		Input:      "hello",
	})
	if err != nil {
		t.Fatalf("BeginTurn() error = %v", err)
	}
	got := collectHandleEvents(t, result.Handle)
	if len(got) == 0 || got[len(got)-1].Event.SessionEvent == nil || got[len(got)-1].Event.SessionEvent.ID != "e1" {
		t.Fatalf("published events = %#v, want assistant event e1", got)
	}
	if rt.lastReq.SessionRef != session.SessionRef || rt.lastReq.Input != "hello" {
		t.Fatalf("runtime req = %+v", rt.lastReq)
	}
}

func TestBeginTurnBridgesApprovalRequestsIntoHandleEvents(t *testing.T) {
	t.Parallel()

	session := sdksession.Session{
		SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
		},
	}
	rt := &approvalRuntime{session: session}
	gw, err := New(Config{
		Sessions: staticSessionService{session: session},
		Runtime:  rt,
		Resolver: staticResolver{resolved: ResolvedTurn{RunRequest: sdkruntime.RunRequest{}}},
		Clock: func() time.Time {
			return time.Unix(100, 0)
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := gw.BeginTurn(context.Background(), BeginTurnRequest{
		SessionRef: session.SessionRef,
		Input:      "hello",
	})
	if err != nil {
		t.Fatalf("BeginTurn() error = %v", err)
	}

	first := <-result.Handle.Events()
	if first.Event.Kind != EventKindApprovalRequested {
		t.Fatalf("first event kind = %q, want approval_requested", first.Event.Kind)
	}
	if err := result.Handle.Submit(context.Background(), SubmitRequest{
		Kind: SubmissionKindApproval,
		Approval: &ApprovalDecision{
			Approved: true,
			Outcome:  "approved",
		},
	}); err != nil {
		t.Fatalf("Submit(approval) error = %v", err)
	}
	got := collectHandleEvents(t, result.Handle)
	if len(got) == 0 {
		t.Fatal("collectHandleEvents() = empty, want completion event stream")
	}
}

type mockRuntime struct{}

func (mockRuntime) Run(context.Context, sdkruntime.RunRequest) (sdkruntime.RunResult, error) {
	return sdkruntime.RunResult{}, nil
}

func (mockRuntime) RunState(context.Context, sdksession.SessionRef) (sdkruntime.RunState, error) {
	return sdkruntime.RunState{}, nil
}

type recordingRuntime struct {
	session sdksession.Session
	result  sdkruntime.RunResult
	lastReq sdkruntime.RunRequest
}

func (r *recordingRuntime) Run(_ context.Context, req sdkruntime.RunRequest) (sdkruntime.RunResult, error) {
	r.lastReq = req
	return r.result, nil
}

func (r *recordingRuntime) RunState(context.Context, sdksession.SessionRef) (sdkruntime.RunState, error) {
	return sdkruntime.RunState{}, nil
}

type approvalRuntime struct {
	session sdksession.Session
}

func (r *approvalRuntime) Run(ctx context.Context, req sdkruntime.RunRequest) (sdkruntime.RunResult, error) {
	if req.ApprovalRequester == nil {
		return sdkruntime.RunResult{}, nil
	}
	_, err := req.ApprovalRequester.RequestApproval(ctx, sdkruntime.ApprovalRequest{
		SessionRef: r.session.SessionRef,
		Session:    r.session,
		RunID:      "run-1",
		TurnID:     "turn-1",
	})
	if err != nil {
		return sdkruntime.RunResult{}, err
	}
	return sdkruntime.RunResult{
		Session: r.session,
		Handle: &recordingRunner{
			events: []*sdksession.Event{{ID: "approved", Type: sdksession.EventTypeNotice}},
		},
	}, nil
}

func (r *approvalRuntime) RunState(context.Context, sdksession.SessionRef) (sdkruntime.RunState, error) {
	return sdkruntime.RunState{}, nil
}

type blockingRuntime struct {
	session sdksession.Session
	wait    chan struct{}
}

func (r *blockingRuntime) Run(context.Context, sdkruntime.RunRequest) (sdkruntime.RunResult, error) {
	if r.wait == nil {
		r.wait = make(chan struct{})
	}
	<-r.wait
	return sdkruntime.RunResult{
		Session: r.session,
		Handle:  &recordingRunner{},
	}, nil
}

func (r *blockingRuntime) RunState(context.Context, sdksession.SessionRef) (sdkruntime.RunState, error) {
	return sdkruntime.RunState{}, nil
}

type cancellableRuntime struct {
	session   sdksession.Session
	cancelled chan struct{}
}

func (r *cancellableRuntime) Run(ctx context.Context, req sdkruntime.RunRequest) (sdkruntime.RunResult, error) {
	_ = req
	<-ctx.Done()
	close(r.cancelled)
	return sdkruntime.RunResult{}, ctx.Err()
}

func (r *cancellableRuntime) RunState(context.Context, sdksession.SessionRef) (sdkruntime.RunState, error) {
	return sdkruntime.RunState{}, nil
}

type staticSessionService struct {
	session sdksession.Session
}

func (s staticSessionService) StartSession(context.Context, sdksession.StartSessionRequest) (sdksession.Session, error) {
	return s.session, nil
}

func (s staticSessionService) LoadSession(context.Context, sdksession.LoadSessionRequest) (sdksession.LoadedSession, error) {
	return sdksession.LoadedSession{Session: s.session}, nil
}

func (s staticSessionService) Session(context.Context, sdksession.SessionRef) (sdksession.Session, error) {
	return s.session, nil
}

func (s staticSessionService) AppendEvent(_ context.Context, req sdksession.AppendEventRequest) (*sdksession.Event, error) {
	return req.Event, nil
}
func (s staticSessionService) Events(context.Context, sdksession.EventsRequest) ([]*sdksession.Event, error) {
	return nil, nil
}
func (s staticSessionService) ListSessions(context.Context, sdksession.ListSessionsRequest) (sdksession.SessionList, error) {
	return sdksession.SessionList{}, nil
}
func (s staticSessionService) BindController(context.Context, sdksession.BindControllerRequest) (sdksession.Session, error) {
	return s.session, nil
}
func (s staticSessionService) PutParticipant(context.Context, sdksession.PutParticipantRequest) (sdksession.Session, error) {
	return s.session, nil
}
func (s staticSessionService) RemoveParticipant(context.Context, sdksession.RemoveParticipantRequest) (sdksession.Session, error) {
	return s.session, nil
}
func (s staticSessionService) SnapshotState(context.Context, sdksession.SessionRef) (map[string]any, error) {
	return nil, nil
}
func (s staticSessionService) ReplaceState(context.Context, sdksession.SessionRef, map[string]any) error {
	return nil
}
func (s staticSessionService) UpdateState(context.Context, sdksession.SessionRef, func(map[string]any) (map[string]any, error)) error {
	return nil
}

type mockSessionService struct{ staticSessionService }

type recordingSessionService struct {
	startReq           sdksession.StartSessionRequest
	loadReq            sdksession.LoadSessionRequest
	listReq            sdksession.ListSessionsRequest
	sessionReq         sdksession.SessionRef
	startSessionResult sdksession.Session
	loadSessionResult  sdksession.LoadedSession
	listSessionsResult sdksession.SessionList
	sessionResult      sdksession.Session
	startErr           error
	loadErr            error
	listErr            error
	sessionErr         error
}

func (s *recordingSessionService) StartSession(_ context.Context, req sdksession.StartSessionRequest) (sdksession.Session, error) {
	s.startReq = req
	if s.startErr != nil {
		return sdksession.Session{}, s.startErr
	}
	return s.startSessionResult, nil
}

func (s *recordingSessionService) LoadSession(_ context.Context, req sdksession.LoadSessionRequest) (sdksession.LoadedSession, error) {
	s.loadReq = req
	if s.loadErr != nil {
		return sdksession.LoadedSession{}, s.loadErr
	}
	return s.loadSessionResult, nil
}

func (s *recordingSessionService) Session(_ context.Context, ref sdksession.SessionRef) (sdksession.Session, error) {
	s.sessionReq = ref
	if s.sessionErr != nil {
		return sdksession.Session{}, s.sessionErr
	}
	return s.sessionResult, nil
}

func (s *recordingSessionService) AppendEvent(_ context.Context, req sdksession.AppendEventRequest) (*sdksession.Event, error) {
	return req.Event, nil
}

func (s *recordingSessionService) Events(context.Context, sdksession.EventsRequest) ([]*sdksession.Event, error) {
	return nil, nil
}

func (s *recordingSessionService) ListSessions(_ context.Context, req sdksession.ListSessionsRequest) (sdksession.SessionList, error) {
	s.listReq = req
	if s.listErr != nil {
		return sdksession.SessionList{}, s.listErr
	}
	return s.listSessionsResult, nil
}

func (s *recordingSessionService) BindController(context.Context, sdksession.BindControllerRequest) (sdksession.Session, error) {
	return s.sessionResult, nil
}

func (s *recordingSessionService) PutParticipant(context.Context, sdksession.PutParticipantRequest) (sdksession.Session, error) {
	return s.sessionResult, nil
}

func (s *recordingSessionService) RemoveParticipant(context.Context, sdksession.RemoveParticipantRequest) (sdksession.Session, error) {
	return s.sessionResult, nil
}

func (s *recordingSessionService) SnapshotState(context.Context, sdksession.SessionRef) (map[string]any, error) {
	return nil, nil
}

func (s *recordingSessionService) ReplaceState(context.Context, sdksession.SessionRef, map[string]any) error {
	return nil
}

func (s *recordingSessionService) UpdateState(context.Context, sdksession.SessionRef, func(map[string]any) (map[string]any, error)) error {
	return nil
}

type staticResolver struct {
	resolved ResolvedTurn
}

func (r staticResolver) ResolveTurn(context.Context, TurnIntent) (ResolvedTurn, error) {
	return r.resolved, nil
}

type recordingResolver struct {
	resolved   ResolvedTurn
	lastIntent TurnIntent
}

func (r *recordingResolver) ResolveTurn(_ context.Context, intent TurnIntent) (ResolvedTurn, error) {
	r.lastIntent = intent
	return r.resolved, nil
}

type recordingRunner struct {
	submissions []sdkruntime.Submission
	events      []*sdksession.Event
	cancelled   bool
}

func (r *recordingRunner) RunID() string { return "run-1" }

func (r *recordingRunner) Events() iter.Seq2[*sdksession.Event, error] {
	events := append([]*sdksession.Event(nil), r.events...)
	return func(yield func(*sdksession.Event, error) bool) {
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}
}

func (r *recordingRunner) Submit(sub sdkruntime.Submission) error {
	r.submissions = append(r.submissions, sub)
	return nil
}

func (r *recordingRunner) Cancel() bool {
	if r.cancelled {
		return false
	}
	r.cancelled = true
	return true
}

func (r *recordingRunner) Close() error { return nil }

func TestSanityTestClock(t *testing.T) {
	t.Parallel()
	if time.Unix(100, 0).IsZero() {
		t.Fatal("unexpected zero time")
	}
}

func collectHandleEvents(t *testing.T, handle TurnHandle) []EventEnvelope {
	t.Helper()

	var out []EventEnvelope
	timeout := time.After(2 * time.Second)
	for {
		select {
		case env, ok := <-handle.Events():
			if !ok {
				return out
			}
			out = append(out, env)
		case <-timeout:
			t.Fatalf("timed out waiting for handle events: %#v", out)
		}
	}
}
