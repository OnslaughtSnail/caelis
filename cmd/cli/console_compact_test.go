package main

import (
	"bytes"
	"context"
	"iter"
	"strings"
	"testing"

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
func (l compactOnlyLLM) Generate(context.Context, *model.Request) iter.Seq2[*model.Response, error] {
	return func(func(*model.Response, error) bool) {}
}

func TestHandleCompact_ShowsVisibleProgressNotice(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{
		Store: store,
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
		{Message: model.Message{Role: model.RoleUser, Text: strings.Repeat("user context ", 120)}},
		{Message: model.Message{Role: model.RoleAssistant, Text: strings.Repeat("assistant context ", 120)}},
	} {
		if err := store.AppendEvent(context.Background(), sess, ev); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
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
	}

	if _, err := handleCompact(c, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "正在压缩上下文") {
		t.Fatalf("expected visible compact progress notice, got %q", got)
	}
	if !strings.Contains(got, "compact: success") {
		t.Fatalf("expected compact success output, got %q", got)
	}
}
