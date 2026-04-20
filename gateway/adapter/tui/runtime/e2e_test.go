package runtime

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/app/gatewayapp"
	"github.com/OnslaughtSnail/caelis/sdk/model/providers/e2etest"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func TestGatewayDriverProviderLiveTurnE2E(t *testing.T) {
	spec := e2etest.RequireLLM(t, e2etest.Config{
		DefaultProvider: "minimax",
		DefaultModel:    "MiniMax-M2",
		Timeout:         90 * time.Second,
		MaxTokens:       256,
	})

	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "tui-runtime-e2e",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   "tui-runtime-e2e",
		WorkspaceCWD:   t.TempDir(),
		PermissionMode: "default",
		Model: gatewayapp.ModelConfig{
			Provider: spec.Provider,
			Model:    spec.Model,
			TokenEnv: providerTokenEnv(spec.Provider),
		},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}

	driver, err := NewGatewayDriver(context.Background(), stack, "tui-runtime-live", "cli-tui", spec.Provider+"/"+spec.Model)
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	start := time.Now()
	turn, err := driver.Submit(ctx, Submission{
		Text: "Reply with exactly: tui runtime live e2e ok",
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if turn == nil {
		t.Fatal("Submit() returned nil turn")
	}
	defer turn.Close()

	var (
		sawChunk     bool
		finalText    string
		firstEventAt time.Time
	)
	for env := range turn.Events() {
		if env.Err != nil {
			t.Fatalf("turn event error = %v", env.Err)
		}
		if env.Event.SessionEvent == nil {
			continue
		}
		if firstEventAt.IsZero() {
			firstEventAt = time.Now()
		}
		event := env.Event.SessionEvent
		if event.Type == sdksession.EventTypeAssistant && event.Visibility == sdksession.VisibilityUIOnly {
			sawChunk = true
		}
		if event.Type == sdksession.EventTypeAssistant && event.Visibility == sdksession.VisibilityCanonical {
			finalText = strings.TrimSpace(event.Text)
		}
	}
	if firstEventAt.IsZero() {
		t.Fatal("expected at least one live turn event")
	}
	if delay := firstEventAt.Sub(start); delay > 2*time.Second {
		t.Fatalf("first turn event arrived after %s, want under 2s", delay)
	}
	if !sawChunk {
		t.Fatal("expected streaming UI-only assistant chunk on cli-tui surface")
	}
	if finalText != "tui runtime live e2e ok" {
		t.Fatalf("final assistant = %q, want %q", finalText, "tui runtime live e2e ok")
	}
}

func TestGatewayDriverProviderConnectThenSubmitE2E(t *testing.T) {
	spec := e2etest.RequireLLM(t, e2etest.Config{
		DefaultProvider: "minimax",
		DefaultModel:    "MiniMax-M2",
		Timeout:         90 * time.Second,
		MaxTokens:       256,
	})

	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "tui-runtime-connect-e2e",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   "tui-runtime-connect-e2e",
		WorkspaceCWD:   t.TempDir(),
		PermissionMode: "default",
		Model: gatewayapp.ModelConfig{
			Provider: spec.Provider,
			Model:    spec.Model,
			TokenEnv: providerTokenEnv(spec.Provider),
		},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}

	driver, err := NewGatewayDriver(context.Background(), stack, "tui-runtime-connect", "cli-tui", spec.Provider+"/"+spec.Model)
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}

	connectCfg := ConnectConfig{
		Provider: spec.Provider,
		Model:    spec.Model,
		APIKey:   strings.TrimSpace(os.Getenv(providerTokenEnv(spec.Provider))),
	}
	status, err := driver.Connect(context.Background(), connectCfg)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if got := strings.TrimSpace(status.Model); got == "" {
		t.Fatal("Connect() returned empty model status")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	turn, err := driver.Submit(ctx, Submission{
		Text: "Reply with exactly: tui runtime connect e2e ok",
	})
	if err != nil {
		t.Fatalf("Submit() after Connect error = %v", err)
	}
	if turn == nil {
		t.Fatal("Submit() after Connect returned nil turn")
	}
	defer turn.Close()

	finalText := collectFinalAssistantText(t, turn)
	if finalText != "tui runtime connect e2e ok" {
		t.Fatalf("final assistant after Connect = %q, want %q", finalText, "tui runtime connect e2e ok")
	}
}

func TestGatewayDriverProviderMultiTurnNewAndResumeE2E(t *testing.T) {
	spec := e2etest.RequireLLM(t, e2etest.Config{
		DefaultProvider: "minimax",
		DefaultModel:    "MiniMax-M2",
		Timeout:         90 * time.Second,
		MaxTokens:       256,
	})

	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "tui-runtime-session-e2e",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   "tui-runtime-session-e2e",
		WorkspaceCWD:   t.TempDir(),
		PermissionMode: "default",
		Model: gatewayapp.ModelConfig{
			Provider: spec.Provider,
			Model:    spec.Model,
			TokenEnv: providerTokenEnv(spec.Provider),
		},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}

	driver, err := NewGatewayDriver(context.Background(), stack, "tui-runtime-session", "cli-tui", spec.Provider+"/"+spec.Model)
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	turn1, err := driver.Submit(ctx, Submission{Text: "Reply with exactly: tui runtime turn one ok"})
	if err != nil {
		t.Fatalf("first Submit() error = %v", err)
	}
	if turn1 == nil {
		t.Fatal("first Submit() returned nil turn")
	}
	firstFinal := collectFinalAssistantText(t, turn1)
	turn1.Close()
	if firstFinal != "tui runtime turn one ok" {
		t.Fatalf("first final assistant = %q, want %q", firstFinal, "tui runtime turn one ok")
	}

	statusBeforeNew, err := driver.Status(ctx)
	if err != nil {
		t.Fatalf("Status() before NewSession error = %v", err)
	}
	originalSessionID := strings.TrimSpace(statusBeforeNew.SessionID)
	if originalSessionID == "" {
		t.Fatal("expected original session id before NewSession")
	}

	newSession, err := driver.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if strings.TrimSpace(newSession.SessionID) == "" {
		t.Fatal("NewSession() returned empty session id")
	}
	if newSession.SessionID == originalSessionID {
		t.Fatalf("NewSession() session id = %q, want different from %q", newSession.SessionID, originalSessionID)
	}

	turn2, err := driver.Submit(ctx, Submission{Text: "Reply with exactly: tui runtime turn two ok"})
	if err != nil {
		t.Fatalf("second Submit() error = %v", err)
	}
	if turn2 == nil {
		t.Fatal("second Submit() returned nil turn")
	}
	secondFinal := collectFinalAssistantText(t, turn2)
	turn2.Close()
	if secondFinal != "tui runtime turn two ok" {
		t.Fatalf("second final assistant = %q, want %q", secondFinal, "tui runtime turn two ok")
	}

	if _, err := driver.ResumeSession(ctx, originalSessionID); err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	replayed, err := driver.ReplayEvents(ctx)
	if err != nil {
		t.Fatalf("ReplayEvents() error = %v", err)
	}
	var replayedFinal string
	for _, env := range replayed {
		if env.Event.SessionEvent == nil {
			continue
		}
		event := env.Event.SessionEvent
		if event.Type == sdksession.EventTypeAssistant && event.Visibility == sdksession.VisibilityCanonical {
			replayedFinal = strings.TrimSpace(event.Text)
		}
	}
	if replayedFinal != "tui runtime turn one ok" {
		t.Fatalf("replayed final assistant = %q, want %q", replayedFinal, "tui runtime turn one ok")
	}
}

func collectFinalAssistantText(t *testing.T, turn Turn) string {
	t.Helper()
	var finalText string
	for env := range turn.Events() {
		if env.Err != nil {
			t.Fatalf("turn event error = %v", env.Err)
		}
		if env.Event.SessionEvent == nil {
			continue
		}
		event := env.Event.SessionEvent
		if event.Type == sdksession.EventTypeAssistant && event.Visibility == sdksession.VisibilityCanonical {
			finalText = strings.TrimSpace(event.Text)
		}
	}
	return finalText
}

func providerTokenEnv(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "minimax":
		return "MINIMAX_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "openai-compatible":
		return "OPENAI_COMPATIBLE_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "gemini":
		return "GEMINI_API_KEY"
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "anthropic-compatible":
		return "ANTHROPIC_COMPATIBLE_API_KEY"
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "xiaomi", "mimo":
		return "XIAOMI_API_KEY"
	case "volcengine", "volcengine-coding-plan", "volcengine_coding_plan":
		return "VOLCENGINE_API_KEY"
	default:
		return ""
	}
}
