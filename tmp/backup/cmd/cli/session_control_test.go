package main

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
)

func TestTogglePlanMode_UsesACPMainModes(t *testing.T) {
	client := &stubMainACPClient{
		modes: &acpclient.SessionModeState{
			CurrentModeID: "default",
			AvailableModes: []acpclient.SessionMode{
				{ID: "default", Name: "Default"},
				{ID: "plan", Name: "Plan"},
				{ID: "full", Name: "Full Access"},
			},
		},
		configOptions: []acpclient.SessionConfigOption{
			{
				ID:           acpConfigMode,
				Category:     "mode",
				CurrentValue: "default",
				Options: []acpclient.SessionConfigSelectOption{
					{Value: "default", Name: "Default"},
					{Value: "plan", Name: "Plan"},
					{Value: "full", Name: "Full Access"},
				},
			},
		},
	}
	console := &cliConsole{
		baseCtx: context.Background(),
		configStore: &appConfigStore{data: appConfig{
			MainAgent: "copilot",
			Agents: map[string]agentRecord{
				"copilot": {Command: "copilot", Args: []string{"--acp", "--stdio"}},
			},
		}},
		persistentMainACP: &persistentMainACPState{
			client:          client,
			agentID:         "copilot",
			remoteSessionID: "remote-1",
			modes:           cloneACPModeState(client.modes),
			configOptions:   cloneACPConfigOptions(client.configOptions),
		},
	}

	hint, err := console.togglePlanMode()
	if err != nil {
		t.Fatalf("toggle ACP mode: %v", err)
	}
	if len(client.setModeCalls) != 1 || client.setModeCalls[0] != "plan" {
		t.Fatalf("expected ACP set_mode call, got %v", client.setModeCalls)
	}
	if len(client.setConfigCalls) != 1 || client.setConfigCalls[0] != "mode=plan" {
		t.Fatalf("expected ACP mode config update, got %v", client.setConfigCalls)
	}
	if hint != "plan mode enabled" {
		t.Fatalf("unexpected hint %q", hint)
	}
	if got := console.sessionModeLabel(); got != "plan" {
		t.Fatalf("expected ACP mode label plan, got %q", got)
	}
}
