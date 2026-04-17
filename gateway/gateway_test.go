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
