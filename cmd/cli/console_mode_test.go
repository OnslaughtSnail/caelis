package main

import (
	"context"
	"testing"
)

func TestNewCLIConsole_TUIModeSkipsLineEditor(t *testing.T) {
	console := newCLIConsole(cliConsoleConfig{
		BaseContext: context.Background(),
		UIMode:      string(uiModeTUI),
		NoColor:     true,
	})
	if console.uiMode != uiModeTUI {
		t.Fatalf("expected ui mode %q, got %q", uiModeTUI, console.uiMode)
	}
	if console.editor != nil {
		t.Fatal("expected no line editor when ui mode is tui")
	}
	if console.prompter != nil {
		t.Fatal("expected no prompter bound before tui loop starts")
	}
}

func TestNewCLIConsole_DefaultsToTUIMode(t *testing.T) {
	console := newCLIConsole(cliConsoleConfig{
		BaseContext: context.Background(),
		NoColor:     true,
	})
	if console.uiMode != uiModeTUI {
		t.Fatalf("expected ui mode %q, got %q", uiModeTUI, console.uiMode)
	}
	if console.editor != nil {
		t.Fatal("expected no line editor in default mode")
	}
	if console.prompter != nil {
		t.Fatal("expected no prompter in default mode before tui loop")
	}
}

func TestNewCLIConsole_CommandSetRemovesLegacyUIAndModels(t *testing.T) {
	console := newCLIConsole(cliConsoleConfig{
		BaseContext: context.Background(),
		NoColor:     true,
	})
	if _, ok := console.commands["ui"]; ok {
		t.Fatal("expected /ui command to be removed")
	}
	if _, ok := console.commands["models"]; ok {
		t.Fatal("expected /models command to be removed")
	}
	if _, ok := console.commands["model"]; !ok {
		t.Fatal("expected /model command to exist")
	}
	if _, ok := console.commands["connect"]; !ok {
		t.Fatal("expected /connect command to exist")
	}
	if _, ok := console.commands["fork"]; !ok {
		t.Fatal("expected /fork command to exist")
	}
	if _, ok := console.commands["attach"]; ok {
		t.Fatal("expected /attach command to be removed")
	}
	if _, ok := console.commands["back"]; ok {
		t.Fatal("expected /back command to be removed")
	}
	if _, ok := console.commands["quit"]; !ok {
		t.Fatal("expected /quit command alias to exist")
	}
	if _, ok := console.commands["sandbox"]; !ok {
		t.Fatal("expected /sandbox command to exist")
	}
	if _, ok := console.commands["permission"]; ok {
		t.Fatal("expected /permission command to be removed")
	}
	if _, ok := console.commands["skills"]; ok {
		t.Fatal("expected /skills command to be removed")
	}
	if _, ok := console.commands["tools"]; ok {
		t.Fatal("expected /tools command to be removed")
	}
	if _, ok := console.commands["mouse"]; ok {
		t.Fatal("expected /mouse command to be removed")
	}
	if console.commands["quit"].Handle == nil || console.commands["exit"].Handle == nil {
		t.Fatal("expected /quit and /exit handlers")
	}
}
