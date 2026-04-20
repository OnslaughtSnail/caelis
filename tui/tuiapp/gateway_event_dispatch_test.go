package tuiapp

import (
	"strings"
	"testing"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func newGatewayEventTestModel() *Model {
	return NewModel(Config{
		AppName:         "CAELIS",
		Version:         "dev",
		Workspace:       "/tmp/workspace",
		ShowWelcomeCard: true,
		Commands:        DefaultCommands(),
		Wizards:         DefaultWizards(),
	})
}

func TestModelUpdateConsumesGatewayAssistantEventIntoMainTurnBlock(t *testing.T) {
	model := newGatewayEventTestModel()

	updated, _ := model.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindAssistantMessage,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			Narrative: &appgateway.NarrativePayload{
				Role:  appgateway.NarrativeRoleAssistant,
				Text:  "gateway answer",
				Final: true,
				Scope: appgateway.EventScopeMain,
			},
		},
	})
	m := updated.(*Model)

	if got := len(m.doc.Blocks()); got != 1 {
		t.Fatalf("doc.Len() = %d, want 1", got)
	}
	block, ok := m.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", m.doc.Blocks()[0])
	}
	if len(block.Events) != 1 || block.Events[0].Kind != SEAssistant || block.Events[0].Text != "gateway answer" {
		t.Fatalf("main turn events = %#v, want assistant narrative event", block.Events)
	}
}

func TestModelUpdateConsumesGatewayToolEventsWithoutTranscriptRecovery(t *testing.T) {
	model := newGatewayEventTestModel()

	updated, _ := model.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindToolCall,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolCall: &appgateway.ToolCallPayload{
				CallID:   "call-1",
				ToolName: "READ",
				ArgsText: `/tmp/demo.txt`,
				Status:   "running",
				Scope:    appgateway.EventScopeMain,
			},
		},
	})
	m := updated.(*Model)
	updated, _ = m.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindToolResult,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolResult: &appgateway.ToolResultPayload{
				CallID:     "call-1",
				ToolName:   "READ",
				OutputText: "/tmp/demo.txt",
				Status:     "completed",
				Scope:      appgateway.EventScopeMain,
			},
		},
	})
	m = updated.(*Model)

	if got := len(m.doc.Blocks()); got != 1 {
		t.Fatalf("doc.Len() = %d, want 1", got)
	}
	block, ok := m.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", m.doc.Blocks()[0])
	}
	if len(block.Events) != 1 {
		t.Fatalf("len(block.Events) = %d, want 1", len(block.Events))
	}
	ev := block.Events[0]
	if ev.Kind != SEToolCall || ev.CallID != "call-1" || ev.Name != "READ" || !ev.Done {
		t.Fatalf("tool event = %#v, want finalized direct tool event", ev)
	}
	for _, item := range m.doc.Blocks() {
		if _, ok := item.(*TranscriptBlock); ok {
			t.Fatalf("unexpected transcript block %#v; want direct structured tool rendering", item)
		}
	}
}

func TestGatewayAssistantFinalKeepsReasoningVisible(t *testing.T) {
	model := newGatewayEventTestModel()

	updated, _ := model.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindAssistantMessage,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			Narrative: &appgateway.NarrativePayload{
				Role:          appgateway.NarrativeRoleAssistant,
				ReasoningText: "thinking through the plan",
				Final:         false,
				Scope:         appgateway.EventScopeMain,
			},
		},
	})
	m := updated.(*Model)
	updated, _ = m.Update(appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindAssistantMessage,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			Narrative: &appgateway.NarrativePayload{
				Role:          appgateway.NarrativeRoleAssistant,
				ReasoningText: "thinking through the plan",
				Text:          "final answer",
				Final:         true,
				Scope:         appgateway.EventScopeMain,
			},
		},
	})
	m = updated.(*Model)

	block, ok := m.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", m.doc.Blocks()[0])
	}
	rows := block.Render(BlockRenderContext{
		Width:     80,
		TermWidth: 80,
		Theme:     m.theme,
	})
	var plain []string
	for _, row := range rows {
		plain = append(plain, row.Plain)
	}
	joined := strings.Join(plain, "\n")
	if !strings.Contains(joined, "thinking through the plan") {
		t.Fatalf("rendered rows = %q, want reasoning text to remain visible", joined)
	}
	if !strings.Contains(joined, "final answer") {
		t.Fatalf("rendered rows = %q, want assistant text", joined)
	}
}

func TestGatewayStreamingNarrativeKeepsReasoningAnswerBoundaries(t *testing.T) {
	model := newGatewayEventTestModel()

	send := func(payload *appgateway.NarrativePayload) *Model {
		updated, _ := model.Update(appgateway.EventEnvelope{
			Event: appgateway.Event{
				Kind:       appgateway.EventKindAssistantMessage,
				SessionRef: sdksession.SessionRef{SessionID: "root-session"},
				Narrative:  payload,
			},
		})
		model = updated.(*Model)
		return model
	}

	send(&appgateway.NarrativePayload{
		Role:          appgateway.NarrativeRoleAssistant,
		ReasoningText: "think-1 ",
		Final:         false,
		Scope:         appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:  appgateway.NarrativeRoleAssistant,
		Text:  "answer-1 ",
		Final: false,
		Scope: appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:          appgateway.NarrativeRoleAssistant,
		ReasoningText: "think-2 ",
		Final:         false,
		Scope:         appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:  appgateway.NarrativeRoleAssistant,
		Text:  "answer-2",
		Final: false,
		Scope: appgateway.EventScopeMain,
	})

	block, ok := model.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", model.doc.Blocks()[0])
	}
	if got := len(block.Events); got != 2 {
		t.Fatalf("len(block.Events) = %d, want 2 active narrative streams; got %#v", got, block.Events)
	}
	wantKinds := []SubagentEventKind{SEReasoning, SEAssistant}
	wantTexts := []string{"think-1think-2", "answer-1answer-2"}
	for i := range wantKinds {
		if block.Events[i].Kind != wantKinds[i] || block.Events[i].Text != wantTexts[i] {
			t.Fatalf("block.Events[%d] = %#v, want kind=%v text=%q", i, block.Events[i], wantKinds[i], wantTexts[i])
		}
	}
}

func TestGatewayInterleavedStreamingFinalReplacesMatchingNarrativeOnly(t *testing.T) {
	model := newGatewayEventTestModel()

	send := func(payload *appgateway.NarrativePayload) *Model {
		updated, _ := model.Update(appgateway.EventEnvelope{
			Event: appgateway.Event{
				Kind:       appgateway.EventKindAssistantMessage,
				SessionRef: sdksession.SessionRef{SessionID: "root-session"},
				Narrative:  payload,
			},
		})
		model = updated.(*Model)
		return model
	}

	send(&appgateway.NarrativePayload{
		Role:          appgateway.NarrativeRoleAssistant,
		ReasoningText: "r1",
		Final:         false,
		Scope:         appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:  appgateway.NarrativeRoleAssistant,
		Text:  "a1",
		Final: false,
		Scope: appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:          appgateway.NarrativeRoleAssistant,
		ReasoningText: "r2-partial",
		Final:         false,
		Scope:         appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:  appgateway.NarrativeRoleAssistant,
		Text:  "a2-partial",
		Final: false,
		Scope: appgateway.EventScopeMain,
	})
	send(&appgateway.NarrativePayload{
		Role:          appgateway.NarrativeRoleAssistant,
		ReasoningText: "r2-final",
		Text:          "a2-final",
		Final:         true,
		Scope:         appgateway.EventScopeMain,
	})

	block, ok := model.doc.Blocks()[0].(*MainACPTurnBlock)
	if !ok {
		t.Fatalf("first block = %#v, want MainACPTurnBlock", model.doc.Blocks()[0])
	}
	if got := len(block.Events); got != 2 {
		t.Fatalf("len(block.Events) = %d, want 2; got %#v", got, block.Events)
	}
	wantKinds := []SubagentEventKind{SEReasoning, SEAssistant}
	wantTexts := []string{"r2-final", "a2-final"}
	for i := range wantKinds {
		if block.Events[i].Kind != wantKinds[i] || block.Events[i].Text != wantTexts[i] {
			t.Fatalf("block.Events[%d] = %#v, want kind=%v text=%q", i, block.Events[i], wantKinds[i], wantTexts[i])
		}
	}
}
