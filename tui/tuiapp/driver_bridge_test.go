package tuiapp

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	tuiadapterruntime "github.com/OnslaughtSnail/caelis/gateway/adapter/tui/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func TestSlashNewClearsHistoryBeforeNotice(t *testing.T) {
	driver := &bridgeTestDriver{
		status:     tuiadapterruntime.StatusSnapshot{Model: "gpt-4o", ModeLabel: "default"},
		newSession: sdksession.Session{SessionRef: sdksession.SessionRef{SessionID: "new-session"}},
	}
	var msgs []tea.Msg
	slashNew(driver, func(msg tea.Msg) { msgs = append(msgs, msg) })
	if len(msgs) < 2 {
		t.Fatalf("slashNew() emitted %d messages, want at least 2", len(msgs))
	}
	if _, ok := msgs[0].(ClearHistoryMsg); !ok {
		t.Fatalf("first msg = %#v, want ClearHistoryMsg", msgs[0])
	}
	if log, ok := msgs[1].(LogChunkMsg); !ok || !strings.Contains(log.Chunk, "new session") {
		t.Fatalf("second msg = %#v, want new session notice", msgs[1])
	}
}

func TestSlashHelpListsMinimalCoreCommands(t *testing.T) {
	var msgs []tea.Msg
	slashHelp(func(msg tea.Msg) { msgs = append(msgs, msg) })
	if len(msgs) != 1 {
		t.Fatalf("slashHelp() emitted %d messages, want 1", len(msgs))
	}
	log, ok := msgs[0].(LogChunkMsg)
	if !ok {
		t.Fatalf("slashHelp() msg = %#v, want LogChunkMsg", msgs[0])
	}
	for _, want := range []string{"/agent list | /agent add <builtin> | /agent use <agent|local> | /agent remove <agent>", "/connect", "/model use <alias> | /model del <alias>", "/compact [note]", "/resume [session-id]"} {
		if !strings.Contains(log.Chunk, want) {
			t.Fatalf("slashHelp() chunk = %q, want substring %q", log.Chunk, want)
		}
	}
}

func TestDefaultCommandsStayInHelpText(t *testing.T) {
	helpText := defaultHelpText()
	for _, command := range DefaultCommands() {
		if !strings.Contains(helpText, "/"+command) {
			t.Fatalf("defaultHelpText() = %q, want command /%s", helpText, command)
		}
	}
}

func TestDefaultCommandsAreRecognizedByDispatch(t *testing.T) {
	driver := &bridgeTestDriver{
		status:     tuiadapterruntime.StatusSnapshot{},
		newSession: sdksession.Session{SessionRef: sdksession.SessionRef{SessionID: "new-session"}},
	}
	for _, command := range DefaultCommands() {
		var msgs []tea.Msg
		result := dispatchSlashCommand(driver, &ProgramSender{Send: func(msg tea.Msg) { msgs = append(msgs, msg) }}, "/"+command)
		if command == "exit" || command == "quit" {
			if !result.ExitNow {
				t.Fatalf("/%s did not request exit", command)
			}
			continue
		}
		for _, msg := range msgs {
			log, ok := msg.(LogChunkMsg)
			if ok && strings.Contains(log.Chunk, "unknown command") {
				t.Fatalf("/%s was treated as unknown: %q", command, log.Chunk)
			}
		}
	}
}

func TestSlashResumeClearsHistoryBeforeReplay(t *testing.T) {
	driver := &bridgeTestDriver{
		status:         tuiadapterruntime.StatusSnapshot{Model: "gpt-4o", ModeLabel: "default"},
		resumedSession: sdksession.Session{SessionRef: sdksession.SessionRef{SessionID: "resumed-session"}},
		replay: []appgateway.EventEnvelope{{
			Event: appgateway.Event{
				Kind: appgateway.EventKindAssistantMessage,
				Narrative: &appgateway.NarrativePayload{
					Role:  appgateway.NarrativeRoleAssistant,
					Text:  "history reply",
					Final: true,
				},
			},
		}},
	}
	var msgs []tea.Msg
	slashResume(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "resumed-session")
	if len(msgs) < 2 {
		t.Fatalf("slashResume() emitted %d messages, want at least 2", len(msgs))
	}
	if _, ok := msgs[0].(ClearHistoryMsg); !ok {
		t.Fatalf("first msg = %#v, want ClearHistoryMsg", msgs[0])
	}
	var sawReplay bool
	for _, msg := range msgs {
		if log, ok := msg.(LogChunkMsg); ok && (strings.Contains(log.Chunk, "resumed session") || strings.Contains(log.Chunk, "replayed")) {
			t.Fatalf("slashResume() emitted noisy resume notice: %#v", log)
		}
		if env, ok := msg.(appgateway.EventEnvelope); ok {
			if env.Event.Narrative != nil && env.Event.Narrative.Text == "history reply" {
				sawReplay = true
			}
		}
	}
	if !sawReplay {
		t.Fatalf("slashResume() messages = %#v, want replayed gateway history", msgs)
	}
}

func TestExecuteLineViaDriverStreamsGatewayEventsDirectly(t *testing.T) {
	turn := &bridgeTestTurn{
		events: make(chan appgateway.EventEnvelope, 1),
	}
	turn.events <- appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindAssistantMessage,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			Narrative: &appgateway.NarrativePayload{
				Role:  appgateway.NarrativeRoleAssistant,
				Text:  "direct gateway event",
				Final: true,
				Scope: appgateway.EventScopeMain,
			},
		},
	}
	close(turn.events)

	driver := &bridgeSubmitDriver{turn: turn}
	var msgs []tea.Msg
	result := executeLineViaDriver(driver, &ProgramSender{Send: func(msg tea.Msg) { msgs = append(msgs, msg) }}, Submission{Text: "hello"})
	if result.Err != nil {
		t.Fatalf("executeLineViaDriver() err = %v", result.Err)
	}
	if len(msgs) != 1 {
		t.Fatalf("executeLineViaDriver() emitted %d msgs, want 1", len(msgs))
	}
	if _, ok := msgs[0].(appgateway.EventEnvelope); !ok {
		t.Fatalf("first msg = %#v, want appgateway.EventEnvelope", msgs[0])
	}
}

func TestExecuteLineViaDriverForwardsTerminalStreamEvents(t *testing.T) {
	turn := &bridgeTestTurn{
		events: make(chan appgateway.EventEnvelope, 1),
	}
	turn.events <- appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindToolResult,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			ToolResult: &appgateway.ToolResultPayload{
				CallID:   "call-1",
				ToolName: "BASH",
				RawOutput: map[string]any{
					"task_id":       "task-1",
					"terminal_id":   "terminal-1",
					"running":       true,
					"state":         "running",
					"stdout_cursor": int64(4),
				},
				Status: appgateway.ToolStatusRunning,
			},
		},
	}
	close(turn.events)
	terminalEvents := make(chan appgateway.EventEnvelope, 1)
	terminalEvents <- appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind: appgateway.EventKindToolResult,
			ToolResult: &appgateway.ToolResultPayload{
				CallID:   "call-1",
				ToolName: "BASH",
				RawOutput: map[string]any{
					"stream": "stdout",
					"text":   "streamed\n",
				},
				Status: appgateway.ToolStatusRunning,
			},
		},
	}
	close(terminalEvents)

	driver := &bridgeSubmitDriver{turn: turn, terminalEvents: terminalEvents}
	var msgs []tea.Msg
	result := executeLineViaDriver(driver, &ProgramSender{Send: func(msg tea.Msg) { msgs = append(msgs, msg) }}, Submission{Text: "hello"})
	if result.Err != nil {
		t.Fatalf("executeLineViaDriver() err = %v", result.Err)
	}
	deadline := time.After(2 * time.Second)
	for {
		var sawStream bool
		for _, msg := range msgs {
			env, ok := msg.(appgateway.EventEnvelope)
			if !ok || env.Event.ToolResult == nil {
				continue
			}
			if env.Event.ToolResult.RawOutput["text"] == "streamed\n" {
				sawStream = true
			}
		}
		if sawStream {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("messages = %#v, want forwarded terminal stream event", msgs)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if driver.terminalSubscribeCalls != 1 {
		t.Fatalf("terminalSubscribeCalls = %d, want 1", driver.terminalSubscribeCalls)
	}
}

func TestSlashResumeReplaysGatewayEventsDirectly(t *testing.T) {
	driver := &bridgeTestDriver{
		status:         tuiadapterruntime.StatusSnapshot{Model: "gpt-4o", ModeLabel: "default"},
		resumedSession: sdksession.Session{SessionRef: sdksession.SessionRef{SessionID: "resumed-session"}},
		replay: []appgateway.EventEnvelope{{
			Event: appgateway.Event{
				Kind:       appgateway.EventKindAssistantMessage,
				SessionRef: sdksession.SessionRef{SessionID: "root-session"},
				Narrative: &appgateway.NarrativePayload{
					Role:  appgateway.NarrativeRoleAssistant,
					Text:  "history reply",
					Final: true,
					Scope: appgateway.EventScopeMain,
				},
			},
		}},
	}
	var msgs []tea.Msg
	slashResume(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "resumed-session")
	var sawReplay bool
	for _, msg := range msgs {
		if env, ok := msg.(appgateway.EventEnvelope); ok && env.Event.Narrative != nil && env.Event.Narrative.Text == "history reply" {
			sawReplay = true
		}
	}
	if !sawReplay {
		t.Fatalf("slashResume() messages = %#v, want replayed gateway envelope", msgs)
	}
}

func TestSlashConnectCallsDriverAndUpdatesStatus(t *testing.T) {
	driver := &bridgeTestDriver{
		status:        tuiadapterruntime.StatusSnapshot{Model: "minimax/MiniMax-M1", ModeLabel: "default", Workspace: "/tmp/ws"},
		connectStatus: tuiadapterruntime.StatusSnapshot{Model: "minimax/MiniMax-M2", ModeLabel: "default", Workspace: "/tmp/ws"},
	}
	var msgs []tea.Msg
	slashConnect(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "minimax MiniMax-M2 - 60 sk-test 204800 8192 low,medium")
	if driver.connectCalls != 1 {
		t.Fatalf("connectCalls = %d, want 1", driver.connectCalls)
	}
	if got := driver.lastConnect.Provider; got != "minimax" {
		t.Fatalf("lastConnect.Provider = %q, want minimax", got)
	}
	if got := driver.lastConnect.Model; got != "MiniMax-M2" {
		t.Fatalf("lastConnect.Model = %q, want MiniMax-M2", got)
	}
	if got := driver.lastConnect.APIKey; got != "sk-test" {
		t.Fatalf("lastConnect.APIKey = %q, want sk-test", got)
	}
	if got := driver.lastConnect.ContextWindowTokens; got != 204800 {
		t.Fatalf("lastConnect.ContextWindowTokens = %d, want 204800", got)
	}
	if got := driver.lastConnect.MaxOutputTokens; got != 8192 {
		t.Fatalf("lastConnect.MaxOutputTokens = %d, want 8192", got)
	}
	if len(msgs) == 0 {
		t.Fatal("slashConnect() emitted no messages")
	}
}

func TestFormatContextUsageStatus(t *testing.T) {
	if got := formatContextUsageStatus(12600, 88000); got != "12.6k/88k(14%)" {
		t.Fatalf("formatContextUsageStatus() = %q, want %q", got, "12.6k/88k(14%)")
	}
	if got := formatContextUsageStatus(0, 88000); got != "0/88k(0%)" {
		t.Fatalf("formatContextUsageStatus() zero = %q, want %q", got, "0/88k(0%)")
	}
}

func TestSlashAgentDispatchesPrimarySubcommands(t *testing.T) {
	driver := &bridgeTestDriver{
		agentList: []tuiadapterruntime.AgentCandidate{{
			Name:        "copilot",
			Description: "ACP sidecar",
		}},
		agentStatus: tuiadapterruntime.AgentStatusSnapshot{
			SessionID:       "sess-1",
			ControllerKind:  "acp",
			ControllerLabel: "copilot",
			Participants: []tuiadapterruntime.AgentParticipantSnapshot{{
				ID:    "participant-1",
				Label: "copilot",
				Role:  "sidecar",
			}},
		},
	}
	var msgs []tea.Msg
	slashAgent(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "list")
	slashAgent(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "add copilot")
	slashAgent(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "remove copilot")
	slashAgent(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "use copilot")
	if driver.listAgentCalls != 3 || driver.agentStatusCalls != 1 || driver.addAgentCalls != 1 || driver.removeAgentCalls != 1 || driver.handoffAgentCalls != 1 || driver.askAgentCalls != 0 {
		t.Fatalf("agent calls = list:%d status:%d add:%d remove:%d use:%d ask:%d", driver.listAgentCalls, driver.agentStatusCalls, driver.addAgentCalls, driver.removeAgentCalls, driver.handoffAgentCalls, driver.askAgentCalls)
	}
	if driver.lastAddedAgent != "copilot" || driver.lastRemovedAgent != "copilot" || driver.lastHandoffAgent != "copilot" || driver.lastAskedAgent != "" || driver.lastAskedPrompt != "" {
		t.Fatalf("agent targets = add:%q remove:%q handoff:%q ask:%q prompt:%q", driver.lastAddedAgent, driver.lastRemovedAgent, driver.lastHandoffAgent, driver.lastAskedAgent, driver.lastAskedPrompt)
	}
	if len(msgs) == 0 {
		t.Fatal("slashAgent() emitted no messages")
	}
}

func TestSlashAgentHelpAndRecovery(t *testing.T) {
	driver := &bridgeTestDriver{}
	var msgs []tea.Msg
	slashAgent(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "")
	slashAgent(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "remove")
	if len(msgs) < 2 {
		t.Fatalf("slashAgent() emitted %d messages, want help and recovery", len(msgs))
	}
	joined := ""
	for _, msg := range msgs {
		if log, ok := msg.(LogChunkMsg); ok {
			joined += log.Chunk
		}
	}
	for _, want := range []string{"/agent commands:", "usage: /agent remove <agent>"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("slashAgent() output = %q, want substring %q", joined, want)
		}
	}
}

func TestDynamicAgentSlashAndHandleContinuation(t *testing.T) {
	driver := &bridgeTestDriver{
		agentList: []tuiadapterruntime.AgentCandidate{{Name: "copilot"}},
		subagentSnapshot: tuiadapterruntime.SubagentSnapshot{
			Handle:        "jeff",
			Mention:       "@jeff",
			Agent:         "copilot",
			TaskID:        "task-1",
			State:         "completed",
			OutputPreview: "child ok",
		},
	}
	var msgs []tea.Msg
	result := dispatchSlashCommand(driver, &ProgramSender{Send: func(msg tea.Msg) { msgs = append(msgs, msg) }}, "/copilot inspect")
	if result.Err != nil {
		t.Fatalf("dynamic slash error = %v", result.Err)
	}
	if driver.lastStartedAgent != "copilot" || driver.lastStartedPrompt != "inspect" {
		t.Fatalf("started agent=%q prompt=%q", driver.lastStartedAgent, driver.lastStartedPrompt)
	}
	result = executeLineViaDriver(driver, &ProgramSender{Send: func(msg tea.Msg) { msgs = append(msgs, msg) }}, Submission{Text: "@jeff continue"})
	if result.Err != nil {
		t.Fatalf("handle continuation error = %v", result.Err)
	}
	if driver.lastContinuedHandle != "jeff" || driver.lastContinuedPrompt != "continue" {
		t.Fatalf("continued handle=%q prompt=%q", driver.lastContinuedHandle, driver.lastContinuedPrompt)
	}
	if len(msgs) == 0 {
		t.Fatal("dynamic slash emitted no messages")
	}
}

func TestDynamicAgentSlashWatchesRunningSubagentUntilOutput(t *testing.T) {
	stream := make(chan tuiadapterruntime.SubagentStreamFrame, 1)
	stream <- tuiadapterruntime.SubagentStreamFrame{
		TaskID:  "task-1",
		Stream:  "stdout",
		Text:    "我是 copilot 子代理",
		Running: false,
		Closed:  true,
	}
	close(stream)
	driver := &bridgeTestDriver{
		agentList: []tuiadapterruntime.AgentCandidate{{Name: "copilot"}},
		subagentSnapshot: tuiadapterruntime.SubagentSnapshot{
			Handle:  "mike",
			Mention: "@mike",
			Agent:   "copilot",
			TaskID:  "task-1",
			State:   "running",
			Running: true,
		},
		subagentStream: stream,
		waitSubagentSnapshot: tuiadapterruntime.SubagentSnapshot{
			Handle:        "mike",
			Mention:       "@mike",
			Agent:         "copilot",
			TaskID:        "task-1",
			State:         "completed",
			Running:       false,
			OutputPreview: "我是 copilot 子代理",
		},
	}
	msgs := make(chan tea.Msg, 16)
	result := dispatchSlashCommand(driver, &ProgramSender{Send: func(msg tea.Msg) { msgs <- msg }}, "/copilot 介绍一下你自己")
	if result.Err != nil {
		t.Fatalf("dynamic slash error = %v", result.Err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-msgs:
			switch typed := msg.(type) {
			case SubagentStartMsg:
				t.Fatalf("dynamic slash emitted SPAWN panel start message: %#v", typed)
			case RawDeltaMsg:
				if typed.Target == RawDeltaTargetSubagent {
					t.Fatalf("dynamic slash emitted SPAWN subagent delta: %#v", typed)
				}
			case AssistantStreamMsg:
				if typed.Actor == "@mike" && strings.Contains(typed.Text, "copilot 子代理") {
					if driver.subagentStreamSubscribeCalls == 0 {
						t.Fatal("SubscribeSubagentStream was not called for running dynamic slash child")
					}
					return
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for watched subagent output; stream subscribe calls=%d wait calls=%d", driver.subagentStreamSubscribeCalls, driver.waitSubagentCalls)
		}
	}
}

func TestSlashConnectParsesEnvironmentVariableSecret(t *testing.T) {
	driver := &bridgeTestDriver{
		connectStatus: tuiadapterruntime.StatusSnapshot{Model: "openai/gpt-4o"},
	}
	slashConnect(driver, func(tea.Msg) {}, "openai gpt-4o - 60 env:OPENAI_API_KEY")
	if got := driver.lastConnect.TokenEnv; got != "OPENAI_API_KEY" {
		t.Fatalf("lastConnect.TokenEnv = %q, want OPENAI_API_KEY", got)
	}
	if got := driver.lastConnect.APIKey; got != "" {
		t.Fatalf("lastConnect.APIKey = %q, want empty when env:... is used", got)
	}
}

func TestSlashModelUseCallsDriverAndUpdatesStatus(t *testing.T) {
	driver := &bridgeTestDriver{
		status:         tuiadapterruntime.StatusSnapshot{Model: "minimax/MiniMax-M1", ModeLabel: "default", Workspace: "/tmp/ws"},
		useModelStatus: tuiadapterruntime.StatusSnapshot{Model: "minimax/MiniMax-M2", ModeLabel: "default", Workspace: "/tmp/ws"},
	}
	var msgs []tea.Msg
	slashModel(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "use minimax/MiniMax-M2")
	if driver.useModelCalls != 1 {
		t.Fatalf("useModelCalls = %d, want 1", driver.useModelCalls)
	}
	if got := driver.lastModelAlias; got != "minimax/MiniMax-M2" {
		t.Fatalf("lastModelAlias = %q, want minimax/MiniMax-M2", got)
	}
	if got := driver.lastReasoningEffort; got != "" {
		t.Fatalf("lastReasoningEffort = %q, want empty", got)
	}
	if len(msgs) == 0 {
		t.Fatal("slashModel(use) emitted no messages")
	}
}

func TestSlashModelUsePassesReasoningLevel(t *testing.T) {
	driver := &bridgeTestDriver{
		status:         tuiadapterruntime.StatusSnapshot{Model: "deepseek/deepseek-v4-pro", ModeLabel: "default", Workspace: "/tmp/ws"},
		useModelStatus: tuiadapterruntime.StatusSnapshot{Model: "deepseek/deepseek-v4-pro [high]", ModeLabel: "default", Workspace: "/tmp/ws"},
	}
	slashModel(driver, func(tea.Msg) {}, "use deepseek/deepseek-v4-pro high")
	if driver.useModelCalls != 1 {
		t.Fatalf("useModelCalls = %d, want 1", driver.useModelCalls)
	}
	if got := driver.lastModelAlias; got != "deepseek/deepseek-v4-pro" {
		t.Fatalf("lastModelAlias = %q, want deepseek/deepseek-v4-pro", got)
	}
	if got := driver.lastReasoningEffort; got != "high" {
		t.Fatalf("lastReasoningEffort = %q, want high", got)
	}
}

func TestSlashModelDeleteCallsDriverAndRefreshesStatus(t *testing.T) {
	driver := &bridgeTestDriver{
		status: tuiadapterruntime.StatusSnapshot{Model: "minimax/MiniMax-M1", ModeLabel: "default", Workspace: "/tmp/ws"},
	}
	var msgs []tea.Msg
	slashModel(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "del minimax/MiniMax-M1")
	if driver.deleteModelCalls != 1 {
		t.Fatalf("deleteModelCalls = %d, want 1", driver.deleteModelCalls)
	}
	if got := driver.lastDeletedAlias; got != "minimax/MiniMax-M1" {
		t.Fatalf("lastDeletedAlias = %q, want minimax/MiniMax-M1", got)
	}
	if len(msgs) == 0 {
		t.Fatal("slashModel(del) emitted no messages")
	}
}

func TestSlashModelDeleteClearsStatusWhenNoModelRemains(t *testing.T) {
	driver := &bridgeTestDriver{
		status: tuiadapterruntime.StatusSnapshot{Workspace: "/tmp/ws"},
	}
	var msgs []tea.Msg
	slashModel(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "del codefree/glm-5.1")
	for _, msg := range msgs {
		status, ok := msg.(SetStatusMsg)
		if !ok {
			continue
		}
		if status.Model != "not configured (/connect)" {
			t.Fatalf("status model = %q, want not configured placeholder", status.Model)
		}
		return
	}
	t.Fatalf("slashModel(del) messages = %#v, want SetStatusMsg", msgs)
}

func TestSlashStatusShowsGuidanceAndWarnings(t *testing.T) {
	driver := &bridgeTestDriver{
		status: tuiadapterruntime.StatusSnapshot{
			SessionID:               "sess-1",
			StoreDir:                "/tmp/.caelis",
			Workspace:               "/tmp/ws",
			SandboxRequestedBackend: "seatbelt",
			SandboxResolvedBackend:  "host",
			Route:                   "host",
			FallbackReason:          "seatbelt is unavailable",
			HostExecution:           true,
			FullAccessMode:          true,
			MissingAPIKey:           true,
		},
	}
	var msgs []tea.Msg
	slashStatus(driver, func(msg tea.Msg) { msgs = append(msgs, msg) })
	if len(msgs) != 1 {
		t.Fatalf("slashStatus() emitted %d messages, want 1", len(msgs))
	}
	log, ok := msgs[0].(LogChunkMsg)
	if !ok {
		t.Fatalf("slashStatus() msg = %#v, want LogChunkMsg", msgs[0])
	}
	for _, want := range []string{"/connect", "warning: API key is missing", "warning: commands may run on the host", "/tmp/.caelis"} {
		if !strings.Contains(log.Chunk, want) {
			t.Fatalf("slashStatus() chunk = %q, want substring %q", log.Chunk, want)
		}
	}
}

func TestFriendlyCommandErrorMakesResumeActionable(t *testing.T) {
	err := friendlyCommandError("resume session", fmt.Errorf("gateway: session not found"))
	if !strings.Contains(err.Error(), "/resume") {
		t.Fatalf("friendlyCommandError() = %q, want /resume guidance", err)
	}
}

type bridgeTestDriver struct {
	status                       tuiadapterruntime.StatusSnapshot
	connectStatus                tuiadapterruntime.StatusSnapshot
	useModelStatus               tuiadapterruntime.StatusSnapshot
	newSession                   sdksession.Session
	resumedSession               sdksession.Session
	replay                       []appgateway.EventEnvelope
	connectCalls                 int
	useModelCalls                int
	deleteModelCalls             int
	listAgentCalls               int
	agentStatusCalls             int
	addAgentCalls                int
	removeAgentCalls             int
	handoffAgentCalls            int
	askAgentCalls                int
	lastConnect                  tuiadapterruntime.ConnectConfig
	lastModelAlias               string
	lastReasoningEffort          string
	lastDeletedAlias             string
	lastAddedAgent               string
	lastRemovedAgent             string
	lastHandoffAgent             string
	lastAskedAgent               string
	lastAskedPrompt              string
	lastStartedAgent             string
	lastStartedPrompt            string
	lastContinuedHandle          string
	lastContinuedPrompt          string
	subagentSnapshot             tuiadapterruntime.SubagentSnapshot
	subagentStream               <-chan tuiadapterruntime.SubagentStreamFrame
	subagentStreamSubscribeCalls int
	waitSubagentCalls            int
	lastWaitTaskID               string
	waitSubagentSnapshot         tuiadapterruntime.SubagentSnapshot
	agentList                    []tuiadapterruntime.AgentCandidate
	agentStatus                  tuiadapterruntime.AgentStatusSnapshot
}

type bridgeTestTurn struct {
	events chan appgateway.EventEnvelope
}

func (t *bridgeTestTurn) HandleID() string { return "handle-1" }
func (t *bridgeTestTurn) RunID() string    { return "run-1" }
func (t *bridgeTestTurn) TurnID() string   { return "turn-1" }
func (t *bridgeTestTurn) SessionRef() sdksession.SessionRef {
	return sdksession.SessionRef{SessionID: "root-session"}
}
func (t *bridgeTestTurn) Events() <-chan appgateway.EventEnvelope { return t.events }
func (t *bridgeTestTurn) Submit(context.Context, appgateway.SubmitRequest) error {
	return nil
}
func (t *bridgeTestTurn) Cancel() bool { return false }
func (t *bridgeTestTurn) Close() error { return nil }

type bridgeSubmitDriver struct {
	turn                   tuiadapterruntime.Turn
	terminalEvents         <-chan appgateway.EventEnvelope
	terminalSubscribeCalls int
}

func (d *bridgeSubmitDriver) Status(context.Context) (tuiadapterruntime.StatusSnapshot, error) {
	return tuiadapterruntime.StatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) WorkspaceDir() string { return "" }
func (d *bridgeSubmitDriver) Submit(context.Context, tuiadapterruntime.Submission) (tuiadapterruntime.Turn, error) {
	return d.turn, nil
}
func (d *bridgeSubmitDriver) SubscribeStream(context.Context, appgateway.EventEnvelope) (<-chan appgateway.EventEnvelope, bool) {
	d.terminalSubscribeCalls++
	if d.terminalEvents == nil {
		return nil, false
	}
	return d.terminalEvents, true
}
func (d *bridgeSubmitDriver) Interrupt(context.Context) error { return nil }
func (d *bridgeSubmitDriver) NewSession(context.Context) (sdksession.Session, error) {
	return sdksession.Session{}, nil
}
func (d *bridgeSubmitDriver) ResumeSession(context.Context, string) (sdksession.Session, error) {
	return sdksession.Session{}, nil
}
func (d *bridgeSubmitDriver) ListSessions(context.Context, int) ([]tuiadapterruntime.ResumeCandidate, error) {
	return nil, nil
}
func (d *bridgeSubmitDriver) ReplayEvents(context.Context) ([]appgateway.EventEnvelope, error) {
	return nil, nil
}
func (d *bridgeSubmitDriver) Compact(context.Context, string) error { return nil }
func (d *bridgeSubmitDriver) Connect(context.Context, tuiadapterruntime.ConnectConfig) (tuiadapterruntime.StatusSnapshot, error) {
	return tuiadapterruntime.StatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) UseModel(context.Context, string, ...string) (tuiadapterruntime.StatusSnapshot, error) {
	return tuiadapterruntime.StatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) DeleteModel(context.Context, string) error { return nil }
func (d *bridgeSubmitDriver) CycleSessionMode(context.Context) (tuiadapterruntime.StatusSnapshot, error) {
	return tuiadapterruntime.StatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) SetSandboxBackend(context.Context, string) (tuiadapterruntime.StatusSnapshot, error) {
	return tuiadapterruntime.StatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) SetSandboxMode(context.Context, string) (tuiadapterruntime.StatusSnapshot, error) {
	return tuiadapterruntime.StatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) ListAgents(context.Context, int) ([]tuiadapterruntime.AgentCandidate, error) {
	return nil, nil
}
func (d *bridgeSubmitDriver) AgentStatus(context.Context) (tuiadapterruntime.AgentStatusSnapshot, error) {
	return tuiadapterruntime.AgentStatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) AddAgent(context.Context, string) (tuiadapterruntime.AgentStatusSnapshot, error) {
	return tuiadapterruntime.AgentStatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) RemoveAgent(context.Context, string) (tuiadapterruntime.AgentStatusSnapshot, error) {
	return tuiadapterruntime.AgentStatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) HandoffAgent(context.Context, string) (tuiadapterruntime.AgentStatusSnapshot, error) {
	return tuiadapterruntime.AgentStatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) AskAgent(context.Context, string, string) (tuiadapterruntime.AgentStatusSnapshot, error) {
	return tuiadapterruntime.AgentStatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) StartAgentSubagent(context.Context, string, string) (tuiadapterruntime.SubagentSnapshot, error) {
	return tuiadapterruntime.SubagentSnapshot{}, nil
}
func (d *bridgeSubmitDriver) ContinueSubagent(context.Context, string, string) (tuiadapterruntime.SubagentSnapshot, error) {
	return tuiadapterruntime.SubagentSnapshot{}, nil
}
func (d *bridgeSubmitDriver) CompleteMention(context.Context, string, int) ([]tuiadapterruntime.CompletionCandidate, error) {
	return nil, nil
}
func (d *bridgeSubmitDriver) CompleteFile(context.Context, string, int) ([]tuiadapterruntime.CompletionCandidate, error) {
	return nil, nil
}
func (d *bridgeSubmitDriver) CompleteSkill(context.Context, string, int) ([]tuiadapterruntime.CompletionCandidate, error) {
	return nil, nil
}
func (d *bridgeSubmitDriver) CompleteResume(context.Context, string, int) ([]tuiadapterruntime.ResumeCandidate, error) {
	return nil, nil
}
func (d *bridgeSubmitDriver) CompleteSlashArg(context.Context, string, string, int) ([]tuiadapterruntime.SlashArgCandidate, error) {
	return nil, nil
}

var _ tuiadapterruntime.Turn = (*bridgeTestTurn)(nil)
var _ tuiadapterruntime.Driver = (*bridgeSubmitDriver)(nil)

var _ = time.Time{}

func (d *bridgeTestDriver) Status(context.Context) (tuiadapterruntime.StatusSnapshot, error) {
	return d.status, nil
}
func (d *bridgeTestDriver) WorkspaceDir() string { return "" }
func (d *bridgeTestDriver) Submit(context.Context, tuiadapterruntime.Submission) (tuiadapterruntime.Turn, error) {
	return nil, nil
}
func (d *bridgeTestDriver) Interrupt(context.Context) error { return nil }
func (d *bridgeTestDriver) NewSession(context.Context) (sdksession.Session, error) {
	return d.newSession, nil
}
func (d *bridgeTestDriver) ResumeSession(context.Context, string) (sdksession.Session, error) {
	return d.resumedSession, nil
}
func (d *bridgeTestDriver) ListSessions(context.Context, int) ([]tuiadapterruntime.ResumeCandidate, error) {
	return nil, nil
}
func (d *bridgeTestDriver) ReplayEvents(context.Context) ([]appgateway.EventEnvelope, error) {
	return d.replay, nil
}
func (d *bridgeTestDriver) Compact(context.Context, string) error { return nil }
func (d *bridgeTestDriver) Connect(_ context.Context, cfg tuiadapterruntime.ConnectConfig) (tuiadapterruntime.StatusSnapshot, error) {
	d.connectCalls++
	d.lastConnect = cfg
	if d.connectStatus.Model != "" || d.connectStatus.Workspace != "" || d.connectStatus.ModeLabel != "" {
		return d.connectStatus, nil
	}
	return d.status, nil
}
func (d *bridgeTestDriver) UseModel(_ context.Context, alias string, reasoningEffort ...string) (tuiadapterruntime.StatusSnapshot, error) {
	d.useModelCalls++
	d.lastModelAlias = alias
	if len(reasoningEffort) > 0 {
		d.lastReasoningEffort = reasoningEffort[0]
	}
	if d.useModelStatus.Model != "" || d.useModelStatus.Workspace != "" || d.useModelStatus.ModeLabel != "" {
		return d.useModelStatus, nil
	}
	return d.status, nil
}
func (d *bridgeTestDriver) DeleteModel(_ context.Context, alias string) error {
	d.deleteModelCalls++
	d.lastDeletedAlias = alias
	return nil
}
func (d *bridgeTestDriver) CycleSessionMode(context.Context) (tuiadapterruntime.StatusSnapshot, error) {
	return d.status, nil
}
func (d *bridgeTestDriver) SetSandboxBackend(context.Context, string) (tuiadapterruntime.StatusSnapshot, error) {
	return d.status, nil
}
func (d *bridgeTestDriver) SetSandboxMode(context.Context, string) (tuiadapterruntime.StatusSnapshot, error) {
	return d.status, nil
}
func (d *bridgeTestDriver) ListAgents(context.Context, int) ([]tuiadapterruntime.AgentCandidate, error) {
	d.listAgentCalls++
	return d.agentList, nil
}
func (d *bridgeTestDriver) AgentStatus(context.Context) (tuiadapterruntime.AgentStatusSnapshot, error) {
	d.agentStatusCalls++
	return d.agentStatus, nil
}
func (d *bridgeTestDriver) AddAgent(_ context.Context, target string) (tuiadapterruntime.AgentStatusSnapshot, error) {
	d.addAgentCalls++
	d.lastAddedAgent = target
	return d.agentStatus, nil
}
func (d *bridgeTestDriver) RemoveAgent(_ context.Context, target string) (tuiadapterruntime.AgentStatusSnapshot, error) {
	d.removeAgentCalls++
	d.lastRemovedAgent = target
	return d.agentStatus, nil
}
func (d *bridgeTestDriver) HandoffAgent(_ context.Context, target string) (tuiadapterruntime.AgentStatusSnapshot, error) {
	d.handoffAgentCalls++
	d.lastHandoffAgent = target
	return d.agentStatus, nil
}
func (d *bridgeTestDriver) AskAgent(_ context.Context, target string, prompt string) (tuiadapterruntime.AgentStatusSnapshot, error) {
	d.askAgentCalls++
	d.lastAskedAgent = target
	d.lastAskedPrompt = prompt
	return d.agentStatus, nil
}
func (d *bridgeTestDriver) StartAgentSubagent(_ context.Context, agent string, prompt string) (tuiadapterruntime.SubagentSnapshot, error) {
	d.lastStartedAgent = agent
	d.lastStartedPrompt = prompt
	return d.subagentSnapshot, nil
}
func (d *bridgeTestDriver) ContinueSubagent(_ context.Context, handle string, prompt string) (tuiadapterruntime.SubagentSnapshot, error) {
	d.lastContinuedHandle = handle
	d.lastContinuedPrompt = prompt
	return d.subagentSnapshot, nil
}
func (d *bridgeTestDriver) SubscribeSubagentStream(_ context.Context, taskID string) (<-chan tuiadapterruntime.SubagentStreamFrame, bool) {
	d.subagentStreamSubscribeCalls++
	if d.subagentStream == nil {
		return nil, false
	}
	d.lastWaitTaskID = taskID
	return d.subagentStream, true
}
func (d *bridgeTestDriver) WaitSubagent(_ context.Context, taskID string) (tuiadapterruntime.SubagentSnapshot, error) {
	d.waitSubagentCalls++
	d.lastWaitTaskID = taskID
	if strings.TrimSpace(d.waitSubagentSnapshot.TaskID) == "" {
		return d.subagentSnapshot, nil
	}
	return d.waitSubagentSnapshot, nil
}
func (d *bridgeTestDriver) CompleteMention(context.Context, string, int) ([]tuiadapterruntime.CompletionCandidate, error) {
	return nil, nil
}
func (d *bridgeTestDriver) CompleteFile(context.Context, string, int) ([]tuiadapterruntime.CompletionCandidate, error) {
	return nil, nil
}
func (d *bridgeTestDriver) CompleteSkill(context.Context, string, int) ([]tuiadapterruntime.CompletionCandidate, error) {
	return nil, nil
}
func (d *bridgeTestDriver) CompleteResume(context.Context, string, int) ([]tuiadapterruntime.ResumeCandidate, error) {
	return nil, nil
}
func (d *bridgeTestDriver) CompleteSlashArg(context.Context, string, string, int) ([]tuiadapterruntime.SlashArgCandidate, error) {
	return nil, nil
}
