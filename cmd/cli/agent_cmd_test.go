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

	if _, err := handleAgent(console, []string{"add", "codex-acp"}); err != nil {
		t.Fatalf("add preset: %v", err)
	}
	if _, ok := console.configStore.data.AgentServers["codex-acp"]; !ok {
		t.Fatal("expected codex-acp in config after add")
	}
	if _, ok := console.agentRegistry.Lookup("codex-acp"); !ok {
		t.Fatal("expected codex-acp in runtime registry after add")
	}

	if _, err := handleAgent(console, []string{"rm", "codex-acp"}); err != nil {
		t.Fatalf("remove preset: %v", err)
	}
	if _, ok := console.configStore.data.AgentServers["codex-acp"]; ok {
		t.Fatal("expected codex-acp removed from config")
	}
}
