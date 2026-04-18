package gateway_test

import (
	"context"
	"strings"
	"testing"
	"time"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	"github.com/OnslaughtSnail/caelis/sdk/model/providers/e2etest"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	"github.com/OnslaughtSnail/caelis/sdk/runtime/agents/chat"
	localruntime "github.com/OnslaughtSnail/caelis/sdk/runtime/local"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sessionfile "github.com/OnslaughtSnail/caelis/sdk/session/file"
)

func TestGatewayProviderLiveTurnAndReplayE2E(t *testing.T) {
	spec := e2etest.RequireLLM(t, e2etest.Config{
		DefaultProvider: "minimax",
		DefaultModel:    "MiniMax-M2",
		Timeout:         90 * time.Second,
		MaxTokens:       256,
	})

	gw, session := newGatewayProviderStack(t, spec.LLM)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	start := time.Now()
	result, err := gw.BeginTurn(ctx, appgateway.BeginTurnRequest{
		SessionRef: session.SessionRef,
		Input:      "Reply with exactly: gateway provider live e2e ok",
		Surface:    "cli-tui",
	})
	if err != nil {
		t.Fatalf("BeginTurn() error = %v", err)
	}
	defer result.Handle.Close()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("BeginTurn() blocked for %s, want live handle under 1s", elapsed)
	}

	var (
		sawUser      bool
		sawChunk     bool
		finalText    string
		firstEventAt time.Time
	)
	for env := range result.Handle.Events() {
		if env.Err != nil {
			t.Fatalf("handle event error = %v", env.Err)
		}
		if env.Event.SessionEvent == nil {
			continue
		}
		if firstEventAt.IsZero() {
			firstEventAt = time.Now()
		}
		event := env.Event.SessionEvent
		switch {
		case event.Type == sdksession.EventTypeUser:
			sawUser = true
		case event.Type == sdksession.EventTypeAssistant &&
			event.Visibility == sdksession.VisibilityUIOnly &&
			event.Protocol != nil &&
			(event.Protocol.UpdateType == string(sdksession.ProtocolUpdateTypeAgentMessage) ||
				event.Protocol.UpdateType == string(sdksession.ProtocolUpdateTypeAgentThought)):
			sawChunk = true
		case event.Type == sdksession.EventTypeAssistant &&
			event.Visibility == sdksession.VisibilityCanonical:
			finalText = strings.TrimSpace(event.Text)
		}
	}
	if firstEventAt.IsZero() {
		t.Fatal("expected at least one live gateway event")
	}
	if delay := firstEventAt.Sub(start); delay > 2*time.Second {
		t.Fatalf("first gateway event arrived after %s, want under 2s", delay)
	}
	if !sawUser {
		t.Fatal("expected live user event")
	}
	if !sawChunk {
		t.Fatal("expected ACP-compatible assistant chunk/thought event before final response")
	}
	if got := strings.TrimSpace(finalText); got != "gateway provider live e2e ok" {
		t.Fatalf("final assistant = %q, want %q", got, "gateway provider live e2e ok")
	}

	replayed, err := gw.ReplayEvents(ctx, appgateway.ReplayEventsRequest{
		SessionRef: session.SessionRef,
	})
	if err != nil {
		t.Fatalf("ReplayEvents() error = %v", err)
	}
	if replayed.HasLiveHandle {
		t.Fatal("ReplayEvents().HasLiveHandle = true, want false after turn completion")
	}
	var (
		replayUser  bool
		replayFinal string
	)
	for _, env := range replayed.Events {
		if env.Event.SessionEvent == nil {
			continue
		}
		switch env.Event.SessionEvent.Type {
		case sdksession.EventTypeUser:
			replayUser = true
		case sdksession.EventTypeAssistant:
			if env.Event.SessionEvent.Visibility == sdksession.VisibilityUIOnly {
				t.Fatalf("ReplayEvents() included transient UI-only event: %+v", env.Event.SessionEvent)
			}
			replayFinal = strings.TrimSpace(env.Event.SessionEvent.Text)
		}
	}
	if !replayUser {
		t.Fatal("expected replayed user event")
	}
	if replayFinal != "gateway provider live e2e ok" {
		t.Fatalf("replayed final assistant = %q, want %q", replayFinal, "gateway provider live e2e ok")
	}
}

func TestGatewayProviderNonStreamingOverrideE2E(t *testing.T) {
	spec := e2etest.RequireLLM(t, e2etest.Config{
		DefaultProvider: "minimax",
		DefaultModel:    "MiniMax-M2",
		Timeout:         90 * time.Second,
		MaxTokens:       256,
	})

	gw, session := newGatewayProviderStack(t, spec.LLM)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	result, err := gw.BeginTurn(ctx, appgateway.BeginTurnRequest{
		SessionRef: session.SessionRef,
		Input:      "Reply with exactly: gateway provider nonstream e2e ok",
		Surface:    "headless",
		Request: sdkruntime.ModelRequestOptions{
			Stream: boolPtr(false),
		},
	})
	if err != nil {
		t.Fatalf("BeginTurn() error = %v", err)
	}
	defer result.Handle.Close()

	var (
		sawChunk  bool
		finalText string
	)
	for env := range result.Handle.Events() {
		if env.Err != nil {
			t.Fatalf("handle event error = %v", env.Err)
		}
		if env.Event.SessionEvent == nil {
			continue
		}
		event := env.Event.SessionEvent
		if event.Type == sdksession.EventTypeAssistant &&
			event.Visibility == sdksession.VisibilityUIOnly {
			sawChunk = true
		}
		if event.Type == sdksession.EventTypeAssistant &&
			event.Visibility == sdksession.VisibilityCanonical {
			finalText = strings.TrimSpace(event.Text)
		}
	}
	if sawChunk {
		t.Fatal("expected no UI-only chunk events when stream=false")
	}
	if got := strings.TrimSpace(finalText); got != "gateway provider nonstream e2e ok" {
		t.Fatalf("final assistant = %q, want %q", got, "gateway provider nonstream e2e ok")
	}
}

func TestGatewayProviderHeadlessDefaultNonStreamingE2E(t *testing.T) {
	spec := e2etest.RequireLLM(t, e2etest.Config{
		DefaultProvider: "minimax",
		DefaultModel:    "MiniMax-M2",
		Timeout:         90 * time.Second,
		MaxTokens:       256,
	})

	gw, session := newGatewayProviderStack(t, spec.LLM)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	result, err := gw.BeginTurn(ctx, appgateway.BeginTurnRequest{
		SessionRef: session.SessionRef,
		Input:      "Reply with exactly: gateway provider headless default e2e ok",
		Surface:    "headless",
	})
	if err != nil {
		t.Fatalf("BeginTurn() error = %v", err)
	}
	defer result.Handle.Close()

	var (
		sawChunk  bool
		finalText string
	)
	for env := range result.Handle.Events() {
		if env.Err != nil {
			t.Fatalf("handle event error = %v", env.Err)
		}
		if env.Event.SessionEvent == nil {
			continue
		}
		event := env.Event.SessionEvent
		if event.Type == sdksession.EventTypeAssistant &&
			event.Visibility == sdksession.VisibilityUIOnly {
			sawChunk = true
		}
		if event.Type == sdksession.EventTypeAssistant &&
			event.Visibility == sdksession.VisibilityCanonical {
			finalText = strings.TrimSpace(event.Text)
		}
	}
	if sawChunk {
		t.Fatal("expected no UI-only chunk events for headless default surface policy")
	}
	if got := strings.TrimSpace(finalText); got != "gateway provider headless default e2e ok" {
		t.Fatalf("final assistant = %q, want %q", got, "gateway provider headless default e2e ok")
	}
}

func newGatewayProviderStack(t *testing.T, model sdkmodel.LLM) (*appgateway.Gateway, sdksession.Session) {
	t.Helper()

	root := t.TempDir()
	sessions := sessionfile.NewService(sessionfile.NewStore(sessionfile.Config{RootDir: root}))
	rt, err := localruntime.New(localruntime.Config{
		Sessions: sessions,
		AgentFactory: chat.Factory{
			SystemPrompt: "Answer tersely.",
		},
	})
	if err != nil {
		t.Fatalf("localruntime.New() error = %v", err)
	}
	gw, err := appgateway.New(appgateway.Config{
		Sessions: sessions,
		Runtime:  rt,
		Resolver: testResolver{model: model},
	})
	if err != nil {
		t.Fatalf("gateway.New() error = %v", err)
	}
	session, err := gw.StartSession(context.Background(), appgateway.StartSessionRequest{
		AppName: "caelis",
		UserID:  "user-1",
		Workspace: sdksession.WorkspaceRef{
			Key: "ws-gateway-e2e",
			CWD: root,
		},
		PreferredSessionID: "gateway-provider-e2e",
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	return gw, session
}

type testResolver struct {
	model sdkmodel.LLM
}

func (r testResolver) ResolveTurn(_ context.Context, intent appgateway.TurnIntent) (appgateway.ResolvedTurn, error) {
	return appgateway.ResolvedTurn{
		RunRequest: sdkruntime.RunRequest{
			SessionRef: intent.SessionRef,
			Input:      intent.Input,
			AgentSpec: sdkruntime.AgentSpec{
				Name:  "main",
				Model: r.model,
			},
		},
	}, nil
}

func boolPtr(v bool) *bool { return &v }
