package policy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

type policyHistoryCtx struct {
	context.Context
	events []*session.Event
	state  session.ReadonlyState
}

func (c policyHistoryCtx) Events() session.Events {
	return session.NewEvents(c.events)
}

func (c policyHistoryCtx) ReadonlyState() session.ReadonlyState {
	if c.state != nil {
		return c.state
	}
	return session.NewReadonlyState(nil)
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

func TestRequireReadBeforeWrite_AfterToolPersistsReadEvidenceIndex(t *testing.T) {
	hook := RequireReadBeforeWrite(ReadBeforeWriteConfig{})
	store := inmemory.New()
	sess, err := store.GetOrCreate(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "a.txt")
	ctx := policyHistoryCtx{
		Context: session.WithStateContext(context.Background(), sess, store),
	}
	out, err := hook.AfterTool(ctx, ToolOutput{
		Call: model.ToolCall{Name: "READ"},
		Result: map[string]any{
			"path": target,
		},
	})
	if err != nil {
		t.Fatalf("expected after-tool persistence to succeed, got %v", err)
	}
	if out.Call.Name != "READ" {
		t.Fatalf("unexpected tool output mutation: %+v", out.Call)
	}
	values, err := store.SnapshotState(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := values[readBeforeWriteStateKey]
	if !ok || !readPathIndexContains(raw, normalizePathForComparison(target)) {
		t.Fatalf("expected persisted read path index, got %#v", values)
	}
}

func TestRequireReadBeforeWrite_AllowsWithPersistedReadIndexWhenReadonlyStateIsStale(t *testing.T) {
	hook := RequireReadBeforeWrite(ReadBeforeWriteConfig{})
	store := inmemory.New()
	sess, err := store.GetOrCreate(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(target, []byte("non-empty"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateState(context.Background(), sess, func(values map[string]any) (map[string]any, error) {
		if values == nil {
			values = map[string]any{}
		}
		values[readBeforeWriteStateKey] = []string{normalizePathForComparison(target)}
		values[readBeforeWriteIndexReadyStateKey] = true
		return values, nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := policyHistoryCtx{
		Context: session.WithStateContext(context.Background(), sess, store),
		state:   session.NewReadonlyState(nil),
	}
	out, err := hook.BeforeTool(ctx, ToolInput{
		Call: model.ToolCall{Name: "WRITE", Args: "{}"},
		Args: map[string]any{"path": target},
		Capability: toolcap.Capability{
			Operations: []toolcap.Operation{toolcap.OperationFileWrite},
			Risk:       toolcap.RiskMedium,
		},
	})
	if err != nil {
		t.Fatalf("expected allow with persisted read index, got %v", err)
	}
	out.Decision = NormalizeDecision(out.Decision)
	if out.Decision.Effect != DecisionEffectAllow {
		t.Fatalf("expected allow decision from persisted read index, got %q", out.Decision.Effect)
	}
}

func TestRequireReadBeforeWrite_BackfillsReadIndexFromPersistedEvents(t *testing.T) {
	hook := RequireReadBeforeWrite(ReadBeforeWriteConfig{})
	store := inmemory.New()
	sess, err := store.GetOrCreate(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(target, []byte("non-empty"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
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
	}); err != nil {
		t.Fatal(err)
	}
	ctx := policyHistoryCtx{
		Context: session.WithStateContext(context.Background(), sess, store),
		state:   session.NewReadonlyState(nil),
	}
	out, err := hook.BeforeTool(ctx, ToolInput{
		Call: model.ToolCall{Name: "WRITE", Args: "{}"},
		Args: map[string]any{"path": target},
		Capability: toolcap.Capability{
			Operations: []toolcap.Operation{toolcap.OperationFileWrite},
			Risk:       toolcap.RiskMedium,
		},
	})
	if err != nil {
		t.Fatalf("expected allow after persisted event backfill, got %v", err)
	}
	out.Decision = NormalizeDecision(out.Decision)
	if out.Decision.Effect != DecisionEffectAllow {
		t.Fatalf("expected allow after persisted event backfill, got %q", out.Decision.Effect)
	}
	values, err := store.SnapshotState(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := values[readBeforeWriteStateKey]
	if !ok || !readPathIndexContains(raw, normalizePathForComparison(target)) {
		t.Fatalf("expected backfilled read path index, got %#v", values)
	}
}
