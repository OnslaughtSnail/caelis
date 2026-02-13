package main

import (
	"strings"
	"testing"
)

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
