package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
)

type testSender struct {
	msgs []any
}

func (s *testSender) Send(msg any) {
	s.msgs = append(s.msgs, msg)
}

func TestHandleNew_StartsFreshSession(t *testing.T) {
	console := &cliConsole{sessionID: "s-old"}
	_, err := handleNew(console, nil)
	if err != nil {
		t.Fatal(err)
	}
	if console.sessionID == "s-old" {
		t.Fatal("expected session id to change")
	}
	if !strings.HasPrefix(console.sessionID, "s-") {
		t.Fatalf("expected new session prefix s-, got %q", console.sessionID)
	}
}

func TestHandleNew_RejectsArgs(t *testing.T) {
	console := &cliConsole{sessionID: "s-old"}
	_, err := handleNew(console, []string{"extra"})
	if err == nil {
		t.Fatal("expected usage error")
	}
}

func TestHandleNew_ClearsTUIHistoryInsteadOfPrintingBranchMessage(t *testing.T) {
	var out bytes.Buffer
	sender := &testSender{}
	console := &cliConsole{
		sessionID:  "s-old",
		out:        &out,
		tuiSender:  sender,
		imageCache: nil,
	}
	_, err := handleNew(console, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no direct output in TUI mode, got %q", out.String())
	}
	if len(sender.msgs) < 2 {
		t.Fatalf("expected clear/history hint messages, got %d", len(sender.msgs))
	}
	if _, ok := sender.msgs[0].(tuievents.ClearHistoryMsg); !ok {
		t.Fatalf("expected first TUI message ClearHistoryMsg, got %T", sender.msgs[0])
	}
	var hint tuievents.SetHintMsg
	var foundHint bool
	for _, raw := range sender.msgs {
		msg, ok := raw.(tuievents.SetHintMsg)
		if !ok {
			continue
		}
		hint = msg
		foundHint = true
	}
	if !foundHint {
		t.Fatal("expected transient new-session hint")
	}
	if hint.Hint != "started new session" {
		t.Fatalf("unexpected hint text %q", hint.Hint)
	}
	if hint.ClearAfter <= 0 {
		t.Fatalf("expected new-session hint to auto-clear, got %s", hint.ClearAfter)
	}
}

func TestHandleFork_StartsNewSessionFromCurrent(t *testing.T) {
	console := &cliConsole{sessionID: "s-old"}
	_, err := handleFork(console, nil)
	if err != nil {
		t.Fatal(err)
	}
	if console.sessionID == "s-old" {
		t.Fatal("expected fork to update session id")
	}
}

func TestHandleFork_KeepTokenUsageAndNoSessionIDInHint(t *testing.T) {
	sender := &testSender{}
	console := &cliConsole{
		sessionID:        "s-old",
		lastPromptTokens: 5200,
		tuiSender:        sender,
	}
	_, err := handleFork(console, nil)
	if err != nil {
		t.Fatal(err)
	}
	if console.lastPromptTokens != 5200 {
		t.Fatalf("expected fork to keep token usage, got %d", console.lastPromptTokens)
	}
	var hint string
	for _, raw := range sender.msgs {
		switch msg := raw.(type) {
		case tuievents.SetHintMsg:
			hint = msg.Hint
			if msg.ClearAfter <= 0 {
				t.Fatalf("expected fork hint to auto-clear, got %s", msg.ClearAfter)
			}
		case tuievents.ClearHistoryMsg:
			t.Fatal("did not expect /fork to clear history")
		}
	}
	if hint != "fork succeeded" {
		t.Fatalf("expected concise fork hint, got %q", hint)
	}
}
