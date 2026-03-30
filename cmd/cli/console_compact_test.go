package main

import (
	"bytes"
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

type stubCompactStrategy struct{}

func (s stubCompactStrategy) Summarize(ctx context.Context, llm model.LLM, in runtime.CompactionSummarizeInput) (runtime.CompactionSummarizeResult, error) {
	_ = ctx
	_ = llm
	return runtime.CompactionSummarizeResult{
		Text:             "## Active Objective\n- keep going",
		SummarizedEvents: len(in.Events),
	}, nil
}

type compactOnlyLLM struct{}

func (l compactOnlyLLM) Name() string { return "fake" }
func (l compactOnlyLLM) Generate(context.Context, *model.Request) iter.Seq2[*model.StreamEvent, error] {
	return func(func(*model.StreamEvent, error) bool) {}
}

func TestHandleCompact_ShowsTokenDeltaAndRefreshesStatus(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{
		LogStore:   store,
		StateStore: store,
		Compaction: runtime.CompactionConfig{
			WatermarkRatio:    0.01,
			MinWatermarkRatio: 0.01,
			Strategy:          stubCompactStrategy{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-cli-compact"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []*session.Event{
		{Message: model.NewTextMessage(model.RoleUser, strings.Repeat("user context ", 120))},
		{Message: model.NewTextMessage(model.RoleAssistant, strings.Repeat("assistant context ", 120))},
	} {
		if err := store.AppendEvent(context.Background(), sess, ev); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	sender := &testSender{}
	c := &cliConsole{
		baseCtx:       context.Background(),
		rt:            rt,
		appName:       sess.AppName,
		userID:        sess.UserID,
		sessionID:     sess.ID,
		contextWindow: 2048,
		llm:           compactOnlyLLM{},
		out:           &out,
		ui:            newUI(&out, true, false),
		tuiSender:     sender,
	}

	if _, err := handleCompact(c, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "正在压缩上下文") {
		t.Fatalf("expected compact progress note removed, got %q", got)
	}
	if !strings.Contains(got, "compact: success, ") || !strings.Contains(got, " -> ") || !strings.Contains(got, " tokens") {
		t.Fatalf("expected compact success output, got %q", got)
	}
	if strings.Contains(got, "event_id=") {
		t.Fatalf("expected compact output without event id, got %q", got)
	}
	if c.lastPromptTokens <= 0 {
		t.Fatalf("expected compact to refresh lastPromptTokens, got %d", c.lastPromptTokens)
	}
	var status tuievents.SetStatusMsg
	var found bool
	for _, raw := range sender.msgs {
		msg, ok := raw.(tuievents.SetStatusMsg)
		if !ok {
			continue
		}
		status = msg
		found = true
	}
	if !found {
		t.Fatalf("expected compact to push updated TUI status, got %#v", sender.msgs)
	}
	if !strings.Contains(status.Context, "/") {
		t.Fatalf("expected compact status context ratio, got %#v", status)
	}
}
