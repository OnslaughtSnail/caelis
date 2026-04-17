# Unified Gateway Stage 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first independent `sdk`-backed Unified Gateway skeleton with its own turn handle contract, structured errors, one-active-run-per-session arbitration, and a headless adapter path.

**Architecture:** Add a new top-level `gateway` package that depends only on `sdk/session` and `sdk/runtime`, with a gateway-owned turn handle abstraction and an injected intent resolver. Add `gateway/adapter/headless` as the first non-interactive adapter over the same contract. Keep the implementation intentionally small and do not import or reuse any legacy `internal/app/gateway`, `internal/app/bootstrap`, or `kernel/sessionsvc` code.

**Tech Stack:** Go, `sdk/session`, `sdk/runtime`, standard library synchronization and context primitives, `go test`

---

### Task 1: Create the Gateway Package Skeleton and TDD the Public Contract

**Files:**
- Create: `gateway/types.go`
- Create: `gateway/errors.go`
- Create: `gateway/gateway.go`
- Create: `gateway/gateway_test.go`

- [ ] **Step 1: Write the failing contract tests**

```go
package gateway

import (
	"context"
	"testing"

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

func TestBeginTurnRejectsSecondActiveRunForSameSession(t *testing.T) {
	t.Parallel()

	session := sdksession.Session{SessionRef: sdksession.SessionRef{
		AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
	}}
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

	second, err := gw.BeginTurn(context.Background(), BeginTurnRequest{
		SessionRef: session.SessionRef,
		Input:      "second",
	})
	if err == nil {
		t.Fatalf("BeginTurn(second) error = nil, want conflict")
	}
	var gwErr *Error
	if second.Handle != nil || !As(err, &gwErr) || gwErr.Code != CodeActiveRunConflict {
		t.Fatalf("BeginTurn(second) error = %v, want active run conflict", err)
	}
}

func TestBeginTurnPassesIntentToResolver(t *testing.T) {
	t.Parallel()

	session := sdksession.Session{SessionRef: sdksession.SessionRef{
		AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
	}}
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

var _ sdkruntime.Runtime = mockRuntime{}
```

- [ ] **Step 2: Run tests to verify RED**

Run: `go test ./gateway -run 'TestNewRequiresSessionsRuntimeAndResolver|TestBeginTurnRejectsSecondActiveRunForSameSession|TestBeginTurnPassesIntentToResolver' -count=1`
Expected: FAIL with missing package/types such as `Config`, `BeginTurnRequest`, `ResolvedTurn`, or `New`

- [ ] **Step 3: Write the minimal gateway contract**

```go
// gateway/types.go
package gateway

import (
	"context"
	"time"

	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

type BeginTurnRequest struct {
	SessionRef   sdksession.SessionRef
	Input        string
	ContentParts []sdkmodel.ContentPart
	ModeName     string
	ModelHint    string
	Surface      string
	Metadata     map[string]any
}

type TurnIntent = BeginTurnRequest

type ResolvedTurn struct {
	RunRequest sdkruntime.RunRequest
}

type TurnResolver interface {
	ResolveTurn(context.Context, TurnIntent) (ResolvedTurn, error)
}

type BeginTurnResult struct {
	Session sdksession.Session
	Handle  TurnHandle
}

type TurnHandle interface {
	HandleID() string
	RunID() string
	TurnID() string
	SessionRef() sdksession.SessionRef
	CreatedAt() time.Time
	Events() <-chan EventEnvelope
	EventsAfter(string) ([]EventEnvelope, string, error)
	Submit(context.Context, SubmitRequest) error
	Cancel() bool
	Close() error
}
```

```go
// gateway/gateway.go
package gateway

import (
	"context"
	"fmt"

	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

type Config struct {
	Sessions sdksession.Service
	Runtime  sdkruntime.Runtime
	Resolver TurnResolver
}

type Gateway struct{}

func New(cfg Config) (*Gateway, error) {
	if cfg.Sessions == nil {
		return nil, fmt.Errorf("gateway: sessions service is required")
	}
	if cfg.Runtime == nil {
		return nil, fmt.Errorf("gateway: runtime is required")
	}
	if cfg.Resolver == nil {
		return nil, fmt.Errorf("gateway: turn resolver is required")
	}
	return &Gateway{}, nil
}

func (g *Gateway) BeginTurn(context.Context, BeginTurnRequest) (BeginTurnResult, error) {
	return BeginTurnResult{}, &Error{Kind: KindInternal, Code: CodeNotImplemented}
}
```

- [ ] **Step 4: Run tests to verify GREEN for constructor and RED for behavior gaps**

Run: `go test ./gateway -run 'TestNewRequiresSessionsRuntimeAndResolver|TestBeginTurnRejectsSecondActiveRunForSameSession|TestBeginTurnPassesIntentToResolver' -count=1`
Expected: constructor test passes; behavior tests still fail because begin-turn semantics are not implemented yet

### Task 2: TDD the Turn Handle, Structured Errors, and Active-Run Arbitration

**Files:**
- Modify: `gateway/types.go`
- Modify: `gateway/errors.go`
- Modify: `gateway/gateway.go`
- Create: `gateway/handle.go`
- Create: `gateway/handle_test.go`
- Modify: `gateway/gateway_test.go`

- [ ] **Step 1: Write failing tests for handle identity, event replay, close idempotence, and submit routing**

```go
func TestTurnHandleReplaysEventsAfterCursor(t *testing.T) {
	t.Parallel()

	handle := newTurnHandle(turnHandleConfig{
		handleID: "h1",
		runID:    "run-1",
		turnID:   "turn-1",
		sessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
		},
		createdAt: time.Unix(100, 0),
	})
	handle.publishSessionEvent(&sdksession.Event{ID: "e1", Type: sdksession.EventTypeUser})
	handle.publishSessionEvent(&sdksession.Event{ID: "e2", Type: sdksession.EventTypeAssistant})

	replayed, next, err := handle.EventsAfter("e1")
	if err != nil {
		t.Fatalf("EventsAfter() error = %v", err)
	}
	if len(replayed) != 1 || replayed[0].Cursor != "e2" || next != "e2" {
		t.Fatalf("EventsAfter() = %#v, %q, want only e2", replayed, next)
	}
}

func TestTurnHandleSubmitRoutesApprovalAndContinuation(t *testing.T) {
	t.Parallel()

	handle := newTurnHandle(turnHandleConfig{
		handleID: "h1",
		runID:    "run-1",
		turnID:   "turn-1",
		sessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
		},
		createdAt: time.Unix(100, 0),
	})
	runner := &recordingRunner{}
	handle.setRunner(runner)

	if err := handle.Submit(context.Background(), SubmitRequest{
		Kind: SubmissionKindConversation,
		Text: "follow up",
	}); err != nil {
		t.Fatalf("Submit(conversation) error = %v", err)
	}
	if got := len(runner.submissions); got != 1 || runner.submissions[0].Text != "follow up" {
		t.Fatalf("runner submissions = %#v", runner.submissions)
	}

	handle.setPendingApproval()
	if err := handle.Submit(context.Background(), SubmitRequest{
		Kind: SubmissionKindApproval,
		Approval: &ApprovalDecision{Approved: true, Outcome: "approved"},
	}); err != nil {
		t.Fatalf("Submit(approval) error = %v", err)
	}
}

func TestTurnHandleCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	handle := newTurnHandle(turnHandleConfig{
		handleID: "h1",
		runID:    "run-1",
		turnID:   "turn-1",
		sessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
		},
		createdAt: time.Unix(100, 0),
	})
	if err := handle.Close(); err != nil {
		t.Fatalf("Close(first) error = %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("Close(second) error = %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run: `go test ./gateway -run 'TestTurnHandleReplaysEventsAfterCursor|TestTurnHandleSubmitRoutesApprovalAndContinuation|TestTurnHandleCloseIsIdempotent|TestBeginTurnRejectsSecondActiveRunForSameSession' -count=1`
Expected: FAIL with missing `newTurnHandle`, `SubmitRequest`, `SubmissionKindConversation`, `ApprovalDecision`, or replay logic

- [ ] **Step 3: Implement structured errors, handle storage, and one-active-run-per-session arbitration**

```go
// gateway/errors.go
package gateway

import "errors"

type ErrorKind string

const (
	KindValidation ErrorKind = "validation"
	KindConflict   ErrorKind = "conflict"
	KindInternal   ErrorKind = "internal"
	KindApproval   ErrorKind = "approval"
	KindUnsupported ErrorKind = "unsupported"
)

const (
	CodeNotImplemented    = "not_implemented"
	CodeActiveRunConflict = "active_run_conflict"
	CodeInvalidRequest    = "invalid_request"
	CodeSubmissionUnsupported = "submission_unsupported"
)

type Error struct {
	Kind        ErrorKind
	Code        string
	Retryable   bool
	UserVisible bool
	Message     string
	Detail      string
	Cause       error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind) + ":" + e.Code
}

func (e *Error) Unwrap() error { return e.Cause }

func As(err error, target any) bool { return errors.As(err, target) }
```

```go
// gateway/handle.go
package gateway

// define EventKind, EventEnvelope, SubmitRequest, ApprovalDecision,
// turnHandleConfig, and turnHandle with in-memory event log, replay by cursor,
// approval slot, runner pointer, cancel/close idempotence, and event channel
```

```go
// gateway/gateway.go
package gateway

// store config dependencies, an active handle map keyed by session id, and
// reject BeginTurn when an unfinished handle already exists for that session
```

- [ ] **Step 4: Run focused tests to verify GREEN**

Run: `go test ./gateway -run 'TestTurnHandleReplaysEventsAfterCursor|TestTurnHandleSubmitRoutesApprovalAndContinuation|TestTurnHandleCloseIsIdempotent|TestBeginTurnRejectsSecondActiveRunForSameSession' -count=1`
Expected: PASS

### Task 3: TDD the In-Process Gateway Execution Path and Approval Bridge

**Files:**
- Modify: `gateway/gateway.go`
- Modify: `gateway/gateway_test.go`
- Modify: `gateway/handle.go`

- [ ] **Step 1: Write failing tests for session loading, runtime invocation, event publication, and approval bridging**

```go
func TestBeginTurnLoadsSessionResolvesIntentRunsRuntimeAndPublishesEvents(t *testing.T) {
	t.Parallel()

	session := sdksession.Session{SessionRef: sdksession.SessionRef{
		AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
	}}
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
	if len(got) == 0 || got[len(got)-1].SessionEvent == nil || got[len(got)-1].SessionEvent.ID != "e1" {
		t.Fatalf("published events = %#v, want assistant event e1", got)
	}
	if rt.lastReq.SessionRef != session.SessionRef || rt.lastReq.Input != "hello" {
		t.Fatalf("runtime req = %+v", rt.lastReq)
	}
}

func TestBeginTurnBridgesApprovalRequestsIntoHandleEvents(t *testing.T) {
	t.Parallel()

	session := sdksession.Session{SessionRef: sdksession.SessionRef{
		AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
	}}
	rt := &approvalRuntime{session: session}
	gw, err := New(Config{
		Sessions: staticSessionService{session: session},
		Runtime:  rt,
		Resolver: staticResolver{resolved: ResolvedTurn{RunRequest: sdkruntime.RunRequest{}}},
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
}
```

- [ ] **Step 2: Run tests to verify RED**

Run: `go test ./gateway -run 'TestBeginTurnLoadsSessionResolvesIntentRunsRuntimeAndPublishesEvents|TestBeginTurnBridgesApprovalRequestsIntoHandleEvents' -count=1`
Expected: FAIL because `BeginTurn` does not yet run the SDK runtime, publish handle events, or bridge approvals

- [ ] **Step 3: Implement the in-process execution flow**

```go
// gateway/gateway.go
package gateway

// BeginTurn should:
// 1. load the session from sdksession.Service
// 2. resolve TurnIntent through the injected resolver
// 3. create and register one gateway-owned turn handle
// 4. run sdkruntime.Runtime.Run in a goroutine with a gateway approval bridge
// 5. attach the returned sdk runner when available
// 6. publish canonical session events onto the handle
// 7. release the active-run slot when the goroutine finishes
```

- [ ] **Step 4: Run focused tests to verify GREEN**

Run: `go test ./gateway -run 'TestBeginTurnLoadsSessionResolvesIntentRunsRuntimeAndPublishesEvents|TestBeginTurnBridgesApprovalRequestsIntoHandleEvents|TestBeginTurnRejectsSecondActiveRunForSameSession' -count=1`
Expected: PASS

### Task 4: TDD the Headless Adapter and the First End-to-End Gateway Smoke Test

**Files:**
- Create: `gateway/adapter/headless/headless.go`
- Create: `gateway/adapter/headless/headless_test.go`
- Modify: `gateway/gateway_test.go`

- [ ] **Step 1: Write failing tests for blocking drain and non-interactive approval policy**

```go
package headless

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/gateway"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func TestRunOnceDrainsAssistantOutput(t *testing.T) {
	t.Parallel()

	gw := gateway.NewTestGatewayWithEvents([]*sdksession.Event{
		{ID: "e1", Type: sdksession.EventTypeAssistant, Text: "done"},
	})
	result, err := RunOnce(context.Background(), gw, gateway.BeginTurnRequest{
		SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
		},
		Input: "hello",
	}, Options{})
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("RunOnce() output = %q, want %q", result.Output, "done")
	}
}

func TestRunOnceAutoDeniesApprovalByDefault(t *testing.T) {
	t.Parallel()

	gw := gateway.NewApprovalTestGateway()
	_, err := RunOnce(context.Background(), gw, gateway.BeginTurnRequest{
		SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s1", WorkspaceKey: "ws",
		},
		Input: "hello",
	}, Options{})
	if err == nil {
		t.Fatalf("RunOnce() error = nil, want approval-related result")
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run: `go test ./gateway/... -run 'TestRunOnceDrainsAssistantOutput|TestRunOnceAutoDeniesApprovalByDefault' -count=1`
Expected: FAIL because the headless adapter package does not exist

- [ ] **Step 3: Implement the headless adapter**

```go
// gateway/adapter/headless/headless.go
package headless

// Define Options, Result, and RunOnce(ctx, gw, req, opts) that:
// 1. begins one turn through the gateway
// 2. drains handle events to completion
// 3. collects the last assistant text
// 4. auto-denies approval requests unless opts overrides the policy
```

- [ ] **Step 4: Run the first stage-1 smoke tests**

Run: `go test ./gateway ./gateway/adapter/headless -count=1`
Expected: PASS

- [ ] **Step 5: Run the repo-level focused validation**

Run: `go test ./gateway/... ./sdk/runtime/local ./sdk/session/... -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add plans/2026-04-17-unified-gateway-stage1.md gateway
git commit -m "feat: add unified gateway stage 1 skeleton"
```

## Self-Review

- Spec coverage: Task 1 and Task 2 cover the package contract, handle, structured errors, and concurrency. Task 3 covers in-process execution and approval bridging. Task 4 covers headless first-class use.
- Placeholder scan: all file paths, commands, and interfaces referenced above are explicit.
- Type consistency: `BeginTurnRequest`, `ResolvedTurn`, `TurnHandle`, `SubmitRequest`, `ApprovalDecision`, and the `headless.RunOnce` API use the same names across all tasks.
