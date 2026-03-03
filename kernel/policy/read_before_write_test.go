package policy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

type policyHistoryCtx struct {
	context.Context
	events []*session.Event
}

func (c policyHistoryCtx) History() []*session.Event {
	out := make([]*session.Event, 0, len(c.events))
	for _, ev := range c.events {
		if ev == nil {
			continue
		}
		cp := *ev
		out = append(out, &cp)
	}
	return out
}

func TestRequireReadBeforeWrite_DeniesWithoutReadEvidence(t *testing.T) {
	hook := RequireReadBeforeWrite(ReadBeforeWriteConfig{})
	target := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(target, []byte("non-empty"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := hook.BeforeTool(context.Background(), ToolInput{
		Call: model.ToolCall{
			Name: "WRITE",
			Args: "{}",
		},
		Args: map[string]any{"path": target},
		Capability: toolcap.Capability{
			Operations: []toolcap.Operation{toolcap.OperationFileWrite},
			Risk:       toolcap.RiskMedium,
		},
	})
	if err != nil {
		t.Fatalf("expected no hard error, got %v", err)
	}
	out.Decision = NormalizeDecision(out.Decision)
	if out.Decision.Effect != DecisionEffectDeny {
		t.Fatalf("expected deny decision without prior READ evidence, got %q", out.Decision.Effect)
	}
}

func TestRequireReadBeforeWrite_AllowsWithReadEvidence(t *testing.T) {
	hook := RequireReadBeforeWrite(ReadBeforeWriteConfig{})
	target := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(target, []byte("non-empty"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := policyHistoryCtx{
		Context: context.Background(),
		events: []*session.Event{
			{
				ID:   "read_1",
				Time: time.Now(),
				Message: model.Message{
					Role: model.RoleTool,
					ToolResponse: &model.ToolResponse{
						ID:   "call_read_1",
						Name: "READ",
						Result: map[string]any{
							"path": target,
						},
					},
				},
			},
		},
	}
	out, err := hook.BeforeTool(ctx, ToolInput{
		Call: model.ToolCall{
			Name: "WRITE",
			Args: "{}",
		},
		Args: map[string]any{"path": target},
		Capability: toolcap.Capability{
			Operations: []toolcap.Operation{toolcap.OperationFileWrite},
			Risk:       toolcap.RiskMedium,
		},
	})
	if err != nil {
		t.Fatalf("expected allow with prior READ evidence, got %v", err)
	}
	out.Decision = NormalizeDecision(out.Decision)
	if out.Decision.Effect != DecisionEffectAllow {
		t.Fatalf("expected allow decision, got %q", out.Decision.Effect)
	}
}

func TestRequireReadBeforeWrite_SkipsNonWriteTools(t *testing.T) {
	hook := RequireReadBeforeWrite(ReadBeforeWriteConfig{})
	_, err := hook.BeforeTool(context.Background(), ToolInput{
		Call: model.ToolCall{Name: "LIST"},
		Capability: toolcap.Capability{
			Operations: []toolcap.Operation{toolcap.OperationFileRead},
			Risk:       toolcap.RiskLow,
		},
	})
	if err != nil {
		t.Fatalf("expected non-write tool to pass, got %v", err)
	}
}

func TestRequireReadBeforeWrite_AllowsNewFileWithoutRead(t *testing.T) {
	hook := RequireReadBeforeWrite(ReadBeforeWriteConfig{})
	target := filepath.Join(t.TempDir(), "new.txt")
	out, err := hook.BeforeTool(context.Background(), ToolInput{
		Call: model.ToolCall{
			Name: "WRITE",
			Args: "{}",
		},
		Args: map[string]any{"path": target},
		Capability: toolcap.Capability{
			Operations: []toolcap.Operation{toolcap.OperationFileWrite},
			Risk:       toolcap.RiskMedium,
		},
	})
	if err != nil {
		t.Fatalf("expected allow for new file, got %v", err)
	}
	out.Decision = NormalizeDecision(out.Decision)
	if out.Decision.Effect != DecisionEffectAllow {
		t.Fatalf("expected allow decision, got %q", out.Decision.Effect)
	}
}

func TestRequireReadBeforeWrite_AllowsEmptyFileWithoutRead(t *testing.T) {
	hook := RequireReadBeforeWrite(ReadBeforeWriteConfig{})
	target := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(target, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := hook.BeforeTool(context.Background(), ToolInput{
		Call: model.ToolCall{
			Name: "WRITE",
			Args: "{}",
		},
		Args: map[string]any{"path": target},
		Capability: toolcap.Capability{
			Operations: []toolcap.Operation{toolcap.OperationFileWrite},
			Risk:       toolcap.RiskMedium,
		},
	})
	if err != nil {
		t.Fatalf("expected allow for empty file, got %v", err)
	}
	out.Decision = NormalizeDecision(out.Decision)
	if out.Decision.Effect != DecisionEffectAllow {
		t.Fatalf("expected allow decision, got %q", out.Decision.Effect)
	}
}
