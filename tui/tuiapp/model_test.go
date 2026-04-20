package tuiapp

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func TestModelViewShowsWelcomeCard(t *testing.T) {
	model := NewModel(Config{
		AppName:         "CAELIS",
		Version:         "dev",
		Workspace:       "/tmp/workspace",
		ModelAlias:      "minimax/MiniMax-M1",
		ShowWelcomeCard: true,
		Commands:        DefaultCommands(),
		Wizards:         DefaultWizards(),
	})

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	view := updated.View().Content
	if !strings.Contains(view, "CAELIS") {
		t.Fatalf("expected CAELIS in welcome card, got %q", view)
	}
	// The transplanted legacy TUI should show model info and the workspace
	if !strings.Contains(view, "/tmp/workspace") {
		t.Fatalf("expected workspace in view, got %q", view)
	}
}

func TestWelcomeCardPrefersDynamicStatusModel(t *testing.T) {
	model := NewModel(Config{
		AppName:         "CAELIS",
		Version:         "dev",
		Workspace:       "/tmp/workspace",
		ModelAlias:      "",
		ShowWelcomeCard: true,
		Commands:        DefaultCommands(),
		Wizards:         DefaultWizards(),
		RefreshStatus: func() (string, string) {
			return "deepseek/deepseek-chat", ""
		},
	})

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	view := updated.View().Content
	if !strings.Contains(view, "deepseek/deepseek-chat") {
		t.Fatalf("expected dynamic model in welcome card, got %q", view)
	}
	if strings.Contains(view, "not configured (/connect)") {
		t.Fatalf("welcome card still shows not configured: %q", view)
	}
}

func TestWelcomeCardUpdatesWhenStatusChanges(t *testing.T) {
	model := NewModel(Config{
		AppName:         "CAELIS",
		Version:         "dev",
		Workspace:       "/tmp/workspace",
		ModelAlias:      "",
		ShowWelcomeCard: true,
		Commands:        DefaultCommands(),
		Wizards:         DefaultWizards(),
	})

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m := updated.(*Model)
	m.handleSetStatusMsg(SetStatusMsg{
		Model:     "deepseek/deepseek-chat",
		Workspace: "/tmp/workspace",
	})
	view := m.View().Content
	if !strings.Contains(view, "deepseek/deepseek-chat") {
		t.Fatalf("expected updated model in welcome card, got %q", view)
	}
	if strings.Contains(view, "not configured (/connect)") {
		t.Fatalf("welcome card still shows not configured after status update: %q", view)
	}
}

func TestReasoningAndAnswerBlocksRemainAdjacentAndIndependent(t *testing.T) {
	model := NewModel(Config{})

	if _, cmd := model.handleStreamBlock("reasoning", "assistant", "thinking...", false); cmd != nil {
		_ = cmd
	}
	if _, cmd := model.handleStreamBlock("reasoning", "assistant", "thinking...", true); cmd != nil {
		_ = cmd
	}
	if model.activeReasoningID != "" {
		t.Fatalf("activeReasoningID = %q, want finalized reasoning block", model.activeReasoningID)
	}
	if got := model.doc.Len(); got != 1 {
		t.Fatalf("doc.Len() after reasoning = %d, want 1", got)
	}
	reasoning, ok := model.doc.Blocks()[0].(*ReasoningBlock)
	if !ok {
		t.Fatalf("first block = %#v, want ReasoningBlock", model.doc.Blocks()[0])
	}
	if reasoning.Streaming {
		t.Fatal("reasoning block should stay in transcript but stop streaming after final")
	}
	if strings.TrimSpace(reasoning.Raw) != "thinking..." {
		t.Fatalf("reasoning raw = %q, want thinking...", reasoning.Raw)
	}

	if _, cmd := model.handleStreamBlock("answer", "assistant", "final answer", true); cmd != nil {
		_ = cmd
	}
	if got := model.doc.Len(); got != 2 {
		t.Fatalf("doc.Len() after answer = %d, want 2", got)
	}
	if _, ok := model.doc.Blocks()[0].(*ReasoningBlock); !ok {
		t.Fatalf("first block = %#v, want ReasoningBlock", model.doc.Blocks()[0])
	}
	answer, ok := model.doc.Blocks()[1].(*AssistantBlock)
	if !ok {
		t.Fatalf("second block = %#v, want AssistantBlock", model.doc.Blocks()[1])
	}
	if answer.Streaming {
		t.Fatal("assistant block should be finalized")
	}
	if strings.TrimSpace(answer.Raw) != "final answer" {
		t.Fatalf("assistant raw = %q, want final answer", answer.Raw)
	}
}

func TestPromptRequestWithoutChoicesStillRendersModal(t *testing.T) {
	model := NewModel(Config{
		AppName:   "CAELIS",
		Version:   "dev",
		Workspace: "/tmp/workspace",
		Commands:  DefaultCommands(),
		Wizards:   DefaultWizards(),
	})

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	m := updated.(*Model)
	updated, _ = m.Update(PromptRequestMsg{
		Title:    "Approval Required",
		Prompt:   "Approval Required",
		Response: make(chan PromptResponse, 1),
	})
	m = updated.(*Model)

	view := m.View().Content
	if !strings.Contains(view, "Approval Required") {
		t.Fatalf("prompt view = %q, want modal title", view)
	}
}

func TestPromptRequestKeepsGatewayToolContentVisible(t *testing.T) {
	model := NewModel(Config{
		AppName:   "CAELIS",
		Version:   "dev",
		Workspace: "/tmp/workspace",
		Commands:  DefaultCommands(),
		Wizards:   DefaultWizards(),
	})

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m := updated.(*Model)
	updated, _ = m.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindToolCall,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			Origin:     &appgateway.EventOrigin{Scope: appgateway.EventScopeMain, ScopeID: "root-session"},
			ToolCall: &appgateway.ToolCallPayload{
				CallID:   "call-1",
				ToolName: "READ",
				ArgsText: "/tmp/demo.txt",
				Status:   "running",
				Scope:    appgateway.EventScopeMain,
			},
		},
	})
	m = updated.(*Model)
	updated, _ = m.Update(PromptRequestMsg{
		Title:    "Approval Required",
		Prompt:   "Approval Required",
		Response: make(chan PromptResponse, 1),
		Choices: []PromptChoice{
			{Label: "Allow once", Value: "allow_once"},
			{Label: "Reject once", Value: "reject_once"},
		},
		DefaultChoice: "allow_once",
	})
	m = updated.(*Model)

	view := m.View().Content
	if !strings.Contains(view, "READ") {
		t.Fatalf("view = %q, want tool row to remain visible", view)
	}
	if !strings.Contains(view, "Approval Required") {
		t.Fatalf("view = %q, want prompt title", view)
	}
}

func TestRunningGatewayToolCallIsVisibleBeforeTaskCompletes(t *testing.T) {
	model := NewModel(Config{
		AppName:   "CAELIS",
		Version:   "dev",
		Workspace: "/tmp/workspace",
		Commands:  DefaultCommands(),
		Wizards:   DefaultWizards(),
	})

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m := updated.(*Model)
	updated, _ = m.Update(SetRunningMsg{Running: true})
	m = updated.(*Model)
	updated, _ = m.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindToolCall,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			Origin:     &appgateway.EventOrigin{Scope: appgateway.EventScopeMain, ScopeID: "root-session"},
			ToolCall: &appgateway.ToolCallPayload{
				CallID:   "call-1",
				ToolName: "BASH",
				ArgsText: `echo "hi"`,
				Status:   "running",
				Scope:    appgateway.EventScopeMain,
			},
		},
	})
	m = updated.(*Model)

	view := m.View().Content
	if !strings.Contains(view, "BASH") {
		t.Fatalf("view = %q, want running tool call before task result", view)
	}
}

func TestPendingGatewayToolCallIsVisibleBeforeTaskCompletes(t *testing.T) {
	model := NewModel(Config{
		AppName:   "CAELIS",
		Version:   "dev",
		Workspace: "/tmp/workspace",
		Commands:  DefaultCommands(),
		Wizards:   DefaultWizards(),
	})

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m := updated.(*Model)
	updated, _ = m.Update(SetRunningMsg{Running: true})
	m = updated.(*Model)
	updated, _ = m.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindToolCall,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			Origin:     &appgateway.EventOrigin{Scope: appgateway.EventScopeMain, ScopeID: "root-session"},
			ToolCall: &appgateway.ToolCallPayload{
				CallID:   "call-1",
				ToolName: "LIST",
				ArgsText: `/tmp/workspace`,
				Status:   "pending",
				Scope:    appgateway.EventScopeMain,
			},
		},
	})
	m = updated.(*Model)

	view := m.View().Content
	if !strings.Contains(view, "LIST") {
		t.Fatalf("view = %q, want pending tool call before task result", view)
	}
}
