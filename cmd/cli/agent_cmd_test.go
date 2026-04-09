package main

import (
	"bytes"
	"path/filepath"
	"testing"

	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
)

func TestHandleAgentAddAndRemove(t *testing.T) {
	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	uiOut := &bytes.Buffer{}
	console := &cliConsole{
		configStore:   store,
		agentRegistry: appagents.NewRegistry(),
		out:           uiOut,
		ui:            newUI(uiOut, true, false),
	}

	if _, err := handleAgent(console, []string{"add", "codex"}); err != nil {
		t.Fatalf("add preset: %v", err)
	}
	if _, ok := console.configStore.data.Agents["codex"]; !ok {
		t.Fatal("expected codex in config after add")
	}
	if _, ok := console.agentRegistry.Lookup("codex"); !ok {
		t.Fatal("expected codex in runtime registry after add")
	}

	if _, err := handleAgent(console, []string{"rm", "codex"}); err != nil {
		t.Fatalf("remove preset: %v", err)
	}
	if _, ok := console.configStore.data.Agents["codex"]; ok {
		t.Fatal("expected codex removed from config")
	}
}

func TestHandleAgentUseBuiltinAddsAndSwitchesMainAgent(t *testing.T) {
	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	uiOut := &bytes.Buffer{}
	console := &cliConsole{
		configStore:   store,
		agentRegistry: appagents.NewRegistry(),
		out:           uiOut,
		ui:            newUI(uiOut, true, false),
	}

	if _, err := handleAgent(console, []string{"use", "codex"}); err != nil {
		t.Fatalf("switch builtin main agent: %v", err)
	}
	if got := console.configStore.MainAgent(); got != "codex" {
		t.Fatalf("expected main agent codex, got %q", got)
	}
	if _, ok := console.configStore.data.Agents["codex"]; !ok {
		t.Fatal("expected codex to be added to config when switching")
	}
	if _, ok := console.agentRegistry.Lookup("codex"); !ok {
		t.Fatal("expected codex to be present in runtime registry after switch")
	}

	if _, err := handleAgent(console, []string{"use", "self"}); err != nil {
		t.Fatalf("switch back to self: %v", err)
	}
	if got := console.configStore.MainAgent(); got != "self" {
		t.Fatalf("expected main agent self, got %q", got)
	}
}

func TestHandleAgentUseRejectsWhileRunActive(t *testing.T) {
	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	uiOut := &bytes.Buffer{}
	console := &cliConsole{
		configStore:   store,
		agentRegistry: appagents.NewRegistry(),
		out:           uiOut,
		ui:            newUI(uiOut, true, false),
		activeRunCancel: func() {
		},
		activeRunKind: runOccupancyMainSession,
	}

	if _, err := handleAgent(console, []string{"use", "self"}); err == nil {
		t.Fatal("expected switching main agent while a run is active to fail")
	}
}

func TestHandleAgentRemoveCurrentMainAgentFallsBackToSelf(t *testing.T) {
	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	uiOut := &bytes.Buffer{}
	console := &cliConsole{
		configStore:   store,
		agentRegistry: appagents.NewRegistry(),
		out:           uiOut,
		ui:            newUI(uiOut, true, false),
	}

	if _, err := handleAgent(console, []string{"use", "codex"}); err != nil {
		t.Fatalf("switch builtin main agent: %v", err)
	}
	if _, err := handleAgent(console, []string{"rm", "codex"}); err != nil {
		t.Fatalf("remove codex: %v", err)
	}
	if got := console.configStore.MainAgent(); got != "self" {
		t.Fatalf("expected main agent to fall back to self after removal, got %q", got)
	}
}
