package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	headlessadapter "github.com/OnslaughtSnail/caelis/gateway/adapter/headless"
	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func TestResolveSingleShotInput_FromPromptFlag(t *testing.T) {
	got, singleShot, err := resolveSingleShotInput("hello", strings.NewReader(""), true, true)
	if err != nil {
		t.Fatalf("resolve input failed: %v", err)
	}
	if !singleShot {
		t.Fatal("expected single-shot mode for -p input")
	}
	if got != "hello" {
		t.Fatalf("expected prompt text, got %q", got)
	}
}

func TestResolveSingleShotInput_FromPipedStdin(t *testing.T) {
	got, singleShot, err := resolveSingleShotInput("", strings.NewReader("from pipe\n"), false, false)
	if err != nil {
		t.Fatalf("resolve input failed: %v", err)
	}
	if !singleShot {
		t.Fatal("expected single-shot mode for piped stdin")
	}
	if got != "from pipe" {
		t.Fatalf("unexpected piped prompt: %q", got)
	}
}

func TestResolveSingleShotInput_RejectsMissingPromptWhenStdoutNonTTY(t *testing.T) {
	_, _, err := resolveSingleShotInput("", strings.NewReader(""), true, false)
	if err == nil {
		t.Fatal("expected error for non-interactive output without input")
	}
}

func TestParseHeadlessOutputFormat(t *testing.T) {
	got, err := parseHeadlessOutputFormat("json")
	if err != nil {
		t.Fatalf("parse format failed: %v", err)
	}
	if got != headlessFormatJSON {
		t.Fatalf("expected json format, got %q", got)
	}
	if _, err := parseHeadlessOutputFormat("xml"); err == nil {
		t.Fatal("expected invalid format error")
	}
}

func TestWriteHeadlessResult_JSON(t *testing.T) {
	var buf bytes.Buffer
	err := writeHeadlessResult(&buf, headlessFormatJSON, headlessRunResult{
		SessionID:    "s-1",
		Output:       "ok",
		PromptTokens: 12,
	})
	if err != nil {
		t.Fatalf("write json failed: %v", err)
	}
	text := strings.TrimSpace(buf.String())
	if !strings.Contains(text, `"session_id":"s-1"`) || !strings.Contains(text, `"output":"ok"`) {
		t.Fatalf("unexpected json output: %q", text)
	}
}

func TestWriteHeadlessResult_Text(t *testing.T) {
	var buf bytes.Buffer
	err := writeHeadlessResult(&buf, headlessFormatText, headlessRunResult{
		Output: "hello",
	})
	if err != nil {
		t.Fatalf("write text failed: %v", err)
	}
	if got := buf.String(); got != "hello\n" {
		t.Fatalf("unexpected text output %q", got)
	}
}

func TestRunHeadlessOnce_UsesGatewayLifecycle(t *testing.T) {
	t.Parallel()

	session := sdksession.Session{
		SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s-1", WorkspaceKey: "ws",
		},
		CWD: "/tmp/ws",
	}
	handle := newCLIHeadlessHandle([]appgateway.EventEnvelope{
		{
			Cursor: "e1",
			Event: appgateway.Event{
				Kind: appgateway.EventKindSessionEvent,
				SessionEvent: &sdksession.Event{
					ID:   "e1",
					Type: sdksession.EventTypeAssistant,
					Text: "ok",
					Meta: map[string]any{
						"usage": map[string]any{"prompt_tokens": 12},
					},
				},
			},
		},
	})
	gw := &fakeHeadlessGateway{
		started: session,
		begin: appgateway.BeginTurnResult{
			Session: session,
			Handle:  handle,
		},
	}

	got, err := runHeadlessOnce(context.Background(), gw, headlessGatewayRunRequest{
		AppName:      "caelis",
		UserID:       "u",
		SessionID:    "s-1",
		Workspace:    sdksession.WorkspaceRef{Key: "ws", CWD: "/tmp/ws"},
		Input:        "hello",
		ContentParts: []sdkmodel.ContentPart{{Type: sdkmodel.ContentPartText, Text: "hello"}},
	})
	if err != nil {
		t.Fatalf("runHeadlessOnce() error = %v", err)
	}
	if got.SessionID != "s-1" || got.Output != "ok" || got.PromptTokens != 12 {
		t.Fatalf("runHeadlessOnce() = %+v", got)
	}
	if gw.startReq.PreferredSessionID != "s-1" || gw.beginReq.SessionRef.SessionID != "s-1" {
		t.Fatalf("gateway requests = start:%+v begin:%+v", gw.startReq, gw.beginReq)
	}
}

func TestRunHeadlessOnce_ConvertsApprovalIntoAdapterFlow(t *testing.T) {
	t.Parallel()

	session := sdksession.Session{
		SessionRef: sdksession.SessionRef{
			AppName: "caelis", UserID: "u", SessionID: "s-1", WorkspaceKey: "ws",
		},
	}
	handle := newCLIHeadlessHandle([]appgateway.EventEnvelope{
		{
			Cursor: "a1",
			Event: appgateway.Event{
				Kind: appgateway.EventKindApprovalRequested,
				Approval: &sdkruntime.ApprovalRequest{
					SessionRef: session.SessionRef,
				},
			},
		},
	})
	gw := &fakeHeadlessGateway{
		started: session,
		begin: appgateway.BeginTurnResult{
			Session: session,
			Handle:  handle,
		},
	}

	if _, err := runHeadlessOnce(context.Background(), gw, headlessGatewayRunRequest{
		AppName:   "caelis",
		UserID:    "u",
		SessionID: "s-1",
		Workspace: sdksession.WorkspaceRef{Key: "ws", CWD: "/tmp/ws"},
		Input:     "hello",
	}); err != nil {
		t.Fatalf("runHeadlessOnce() error = %v", err)
	}
	if len(handle.submits) != 1 || handle.submits[0].Kind != appgateway.SubmissionKindApproval || handle.submits[0].Approval == nil || handle.submits[0].Approval.Approved {
		t.Fatalf("approval submits = %#v", handle.submits)
	}
}

type fakeHeadlessGateway struct {
	startReq appgateway.StartSessionRequest
	beginReq appgateway.BeginTurnRequest
	started  sdksession.Session
	begin    appgateway.BeginTurnResult
}

func (f *fakeHeadlessGateway) StartSession(_ context.Context, req appgateway.StartSessionRequest) (sdksession.Session, error) {
	f.startReq = req
	return f.started, nil
}

func (f *fakeHeadlessGateway) BeginTurn(_ context.Context, req appgateway.BeginTurnRequest) (appgateway.BeginTurnResult, error) {
	f.beginReq = req
	return f.begin, nil
}

type cliHeadlessHandle struct {
	events  chan appgateway.EventEnvelope
	submits []appgateway.SubmitRequest
}

func newCLIHeadlessHandle(events []appgateway.EventEnvelope) *cliHeadlessHandle {
	ch := make(chan appgateway.EventEnvelope, len(events))
	for _, env := range events {
		ch <- env
	}
	close(ch)
	return &cliHeadlessHandle{events: ch}
}

func (h *cliHeadlessHandle) HandleID() string                  { return "h-1" }
func (h *cliHeadlessHandle) RunID() string                     { return "run-1" }
func (h *cliHeadlessHandle) TurnID() string                    { return "turn-1" }
func (h *cliHeadlessHandle) SessionRef() sdksession.SessionRef { return sdksession.SessionRef{} }
func (h *cliHeadlessHandle) CreatedAt() time.Time              { return time.Time{} }
func (h *cliHeadlessHandle) Events() <-chan appgateway.EventEnvelope {
	return h.events
}
func (h *cliHeadlessHandle) EventsAfter(string) ([]appgateway.EventEnvelope, string, error) {
	return nil, "", nil
}
func (h *cliHeadlessHandle) Submit(_ context.Context, req appgateway.SubmitRequest) error {
	h.submits = append(h.submits, req)
	return nil
}
func (h *cliHeadlessHandle) Cancel() bool { return true }
func (h *cliHeadlessHandle) Close() error { return nil }

var _ headlessadapter.Starter = (*fakeHeadlessGateway)(nil)
