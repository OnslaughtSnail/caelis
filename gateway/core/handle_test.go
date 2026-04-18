package core

import (
	"context"
	"testing"
	"time"

	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

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

func TestTurnHandleCanonicalizesAssistantEventAndUsage(t *testing.T) {
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
	handle.publishSessionEvent(&sdksession.Event{
		ID:   "e1",
		Type: sdksession.EventTypeAssistant,
		Text: "done",
		Meta: map[string]any{
			"usage": map[string]any{
				"prompt_tokens":     12,
				"completion_tokens": 5,
				"total_tokens":      17,
			},
		},
	})

	replayed, next, err := handle.EventsAfter("")
	if err != nil {
		t.Fatalf("EventsAfter() error = %v", err)
	}
	if len(replayed) != 1 || next != "e1" {
		t.Fatalf("EventsAfter() = %#v, %q", replayed, next)
	}
	if replayed[0].Event.Kind != EventKindAssistantMessage {
		t.Fatalf("event kind = %q, want %q", replayed[0].Event.Kind, EventKindAssistantMessage)
	}
	if got := AssistantText(replayed[0].Event); got != "done" {
		t.Fatalf("AssistantText() = %q, want %q", got, "done")
	}
	if replayed[0].Event.Usage == nil || replayed[0].Event.Usage.PromptTokens != 12 || replayed[0].Event.Usage.CompletionTokens != 5 || replayed[0].Event.Usage.TotalTokens != 17 {
		t.Fatalf("usage = %+v", replayed[0].Event.Usage)
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

	wait := handle.setPendingApproval()
	if err := handle.Submit(context.Background(), SubmitRequest{
		Kind:     SubmissionKindApproval,
		Approval: &ApprovalDecision{Approved: true, Outcome: "approved"},
	}); err != nil {
		t.Fatalf("Submit(approval) error = %v", err)
	}
	resp := <-wait
	if !resp.Approved || resp.Outcome != "approved" {
		t.Fatalf("approval response = %+v", resp)
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

func TestTurnHandleCloseAfterFinishDoesNotDoubleClose(t *testing.T) {
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

	handle.finish()
	if err := handle.Close(); err != nil {
		t.Fatalf("Close(after finish) error = %v", err)
	}
}

func TestTurnHandleSubmitRejectsUnsupportedWithoutRunner(t *testing.T) {
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
	err := handle.Submit(context.Background(), SubmitRequest{
		Kind: SubmissionKindConversation,
		Text: "follow up",
	})
	if err == nil {
		t.Fatal("Submit() error = nil, want unsupported")
	}
	var gwErr *Error
	if !As(err, &gwErr) || gwErr.Code != CodeSubmissionUnsupported {
		t.Fatalf("Submit() error = %v, want submission unsupported", err)
	}
}

func TestTurnHandleApprovalSubmitRejectsWithoutPendingRequest(t *testing.T) {
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

	err := handle.Submit(context.Background(), SubmitRequest{
		Kind:     SubmissionKindApproval,
		Approval: &ApprovalDecision{Approved: true, Outcome: "approved"},
	})
	if err == nil {
		t.Fatal("Submit(approval) error = nil, want approval-not-pending")
	}
	var gwErr *Error
	if !As(err, &gwErr) || gwErr.Code != CodeApprovalNotPending {
		t.Fatalf("Submit(approval) error = %v, want approval_not_pending", err)
	}
}

var _ sdkruntime.Runner = (*recordingRunner)(nil)
