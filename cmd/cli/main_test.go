package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func TestResolveInputFromPrompt(t *testing.T) {
	got, single, err := resolveInput("hello", strings.NewReader(""), true)
	if err != nil {
		t.Fatalf("resolveInput() error = %v", err)
	}
	if !single || got != "hello" {
		t.Fatalf("resolveInput() = %q, %v", got, single)
	}
}

func TestResolveTurnInputForceInteractiveDoesNotConsumePipe(t *testing.T) {
	stdin := strings.NewReader("piped prompt")
	got, single, err := resolveTurnInput("", stdin, false, true)
	if err != nil {
		t.Fatalf("resolveTurnInput() error = %v", err)
	}
	if single || got != "" {
		t.Fatalf("resolveTurnInput() = %q, %v", got, single)
	}

	remaining, err := io.ReadAll(stdin)
	if err != nil {
		t.Fatalf("ReadAll(stdin) error = %v", err)
	}
	if string(remaining) != "piped prompt" {
		t.Fatalf("remaining stdin = %q", remaining)
	}
}

func TestParseOutputFormat(t *testing.T) {
	if got, err := parseOutputFormat("json"); err != nil || got != outputJSON {
		t.Fatalf("parseOutputFormat() = %q, %v", got, err)
	}
	if _, err := parseOutputFormat("xml"); err == nil {
		t.Fatal("parseOutputFormat(xml) error = nil")
	}
}

func TestDefaultStoreDirUsesHomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("home directory unavailable")
	}
	want := filepath.Join(home, ".caelis")
	if got := defaultStoreDir(t.TempDir()); got != want {
		t.Fatalf("defaultStoreDir() = %q, want %q", got, want)
	}
}

func TestPreferredSessionIDDefaultsDifferBetweenInteractiveAndHeadless(t *testing.T) {
	if got := preferredInteractiveSessionID(""); got != "" {
		t.Fatalf("preferredInteractiveSessionID(\"\") = %q, want empty for fresh TUI session", got)
	}
	if got := preferredHeadlessSessionID(""); got != "" {
		t.Fatalf("preferredHeadlessSessionID(\"\") = %q, want empty for fresh headless session", got)
	}
	if got := preferredInteractiveSessionID("sticky"); got != "sticky" {
		t.Fatalf("preferredInteractiveSessionID(\"sticky\") = %q, want sticky", got)
	}
	if got := preferredHeadlessSessionID("sticky"); got != "sticky" {
		t.Fatalf("preferredHeadlessSessionID(\"sticky\") = %q, want sticky", got)
	}
}

func TestStreamHandleWritesAssistantTextAndDeniesApproval(t *testing.T) {
	handle := newFakeHandle([]appgateway.EventEnvelope{
		{
			Event: appgateway.Event{
				Kind: appgateway.EventKindApprovalRequested,
				Approval: &sdkruntime.ApprovalRequest{
					SessionRef: sdksession.SessionRef{SessionID: "s1"},
				},
			},
		},
		{
			Event: appgateway.Event{
				Kind: appgateway.EventKindAssistantMessage,
				SessionEvent: &sdksession.Event{
					Type: sdksession.EventTypeAssistant,
					Text: "interactive ok",
				},
			},
		},
	})
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := streamHandle(context.Background(), handle, &out, &errBuf); err != nil {
		t.Fatalf("streamHandle() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "interactive ok") {
		t.Fatalf("stdout = %q", got)
	}
	if got := errBuf.String(); !strings.Contains(got, "denied by default") {
		t.Fatalf("stderr = %q", got)
	}
	if len(handle.submits) != 1 || handle.submits[0].Approval == nil || handle.submits[0].Approval.Approved {
		t.Fatalf("submits = %#v", handle.submits)
	}
}

type fakeHandle struct {
	events    chan appgateway.EventEnvelope
	submits   []appgateway.SubmitRequest
	closed    bool
	cancelled bool
}

func newFakeHandle(events []appgateway.EventEnvelope) *fakeHandle {
	ch := make(chan appgateway.EventEnvelope, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return &fakeHandle{events: ch}
}

func (h *fakeHandle) HandleID() string { return "h1" }
func (h *fakeHandle) RunID() string    { return "r1" }
func (h *fakeHandle) TurnID() string   { return "t1" }
func (h *fakeHandle) SessionRef() sdksession.SessionRef {
	return sdksession.SessionRef{SessionID: "s1"}
}
func (h *fakeHandle) CreatedAt() time.Time { return time.Time{} }
func (h *fakeHandle) Events() <-chan appgateway.EventEnvelope {
	return h.events
}
func (h *fakeHandle) EventsAfter(string) ([]appgateway.EventEnvelope, string, error) {
	return nil, "", nil
}
func (h *fakeHandle) Submit(_ context.Context, req appgateway.SubmitRequest) error {
	h.submits = append(h.submits, req)
	return nil
}
func (h *fakeHandle) Cancel() bool {
	h.cancelled = true
	return true
}
func (h *fakeHandle) Close() error {
	h.closed = true
	return nil
}
