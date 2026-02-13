package policy

import (
	"context"
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
	_, err := hook.BeforeTool(context.Background(), ToolInput{
		Call: model.ToolCall{
			Name: "WRITE",
			Args: map[string]any{"path": "a.txt"},
		},
		Capability: toolcap.Capability{
			Operations: []toolcap.Operation{toolcap.OperationFileWrite},
			Risk:       toolcap.RiskMedium,
		},
	})
	if err == nil {
		t.Fatal("expected denial without prior READ evidence")
	}
}

func TestRequireReadBeforeWrite_AllowsWithReadEvidence(t *testing.T) {
	hook := RequireReadBeforeWrite(ReadBeforeWriteConfig{})
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
							"path": "a.txt",
						},
					},
				},
			},
		},
	}
	_, err := hook.BeforeTool(ctx, ToolInput{
		Call: model.ToolCall{
			Name: "WRITE",
			Args: map[string]any{"path": "a.txt"},
		},
		Capability: toolcap.Capability{
			Operations: []toolcap.Operation{toolcap.OperationFileWrite},
			Risk:       toolcap.RiskMedium,
		},
	})
	if err != nil {
		t.Fatalf("expected allow with prior READ evidence, got %v", err)
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
