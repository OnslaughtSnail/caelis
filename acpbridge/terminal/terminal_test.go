package terminal

import (
	"context"
	"iter"
	"testing"

	sdkterminal "github.com/OnslaughtSnail/caelis/sdk/terminal"
)

func TestLocalTerminalAdapterOutputUsesCumulativeRead(t *testing.T) {
	t.Parallel()

	service := &recordingTerminalService{
		snapshot: sdkterminal.Snapshot{
			Frames: []sdkterminal.Frame{{Stream: "stdout", Text: "one\ntwo\n"}},
			Cursor: sdkterminal.Cursor{Stdout: 8},
		},
	}
	adapter := LocalTerminalAdapter{Terminals: service}

	resp, err := adapter.Output(context.Background(), TerminalOutputRequest{
		SessionID:  "session-1",
		TerminalID: "terminal-1",
	})
	if err != nil {
		t.Fatalf("Output() error = %v", err)
	}
	if resp.Output != "one\ntwo\n" {
		t.Fatalf("Output() = %q, want cumulative terminal output", resp.Output)
	}
	if service.readReq.Cursor.Stdout != 0 || service.readReq.Cursor.Stderr != 0 {
		t.Fatalf("Read cursor = %+v, want zero cursor for ACP cumulative output", service.readReq.Cursor)
	}
}

type recordingTerminalService struct {
	readReq  sdkterminal.ReadRequest
	snapshot sdkterminal.Snapshot
}

func (s *recordingTerminalService) Read(_ context.Context, req sdkterminal.ReadRequest) (sdkterminal.Snapshot, error) {
	s.readReq = req
	return s.snapshot, nil
}

func (s *recordingTerminalService) Subscribe(context.Context, sdkterminal.SubscribeRequest) iter.Seq2[*sdkterminal.Frame, error] {
	return func(func(*sdkterminal.Frame, error) bool) {}
}
