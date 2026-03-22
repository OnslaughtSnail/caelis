package llmagent

import (
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestAgentOutboundTraceMatchesSanitizedMessages(t *testing.T) {
	t.Setenv(model.RequestTraceEnvVar, "1")
	tracePath := filepath.Join(t.TempDir(), model.RequestTraceFileName)
	history := []*session.Event{
		{Message: model.Message{Role: model.RoleUser, Text: "first question"}},
		{Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   "call-1",
				Name: "READ",
				Args: `{"path":"README.md"}`,
			}},
		}},
		{Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:   "call-1",
				Name: "READ",
				Result: map[string]any{
					"snippet":        "hello",
					"_ui_hidden_key": "ignore me",
				},
			},
		}},
	}
	llm := model.WrapRequestTrace(newTestLLM("trace-llm", func(req *model.Request) (*model.Response, error) {
		return &model.Response{
			Message: model.Message{
				Role: model.RoleAssistant,
				Text: "answer",
			},
			TurnComplete: true,
		}, nil
	}))
	ctx := &testCtx{
		Context: model.WithRequestTraceContext(context.Background(), model.RequestTraceContext{
			SessionID: "s-trace",
			RunID:     "r-trace",
			Path:      tracePath,
		}),
		session: &session.Session{AppName: "app", UserID: "u", ID: "s-trace"},
		history: history,
		llm:     llm,
	}
	ag, err := New(Config{
		Name:         "trace-agent",
		SystemPrompt: "system prompt",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, runErr := range collectRunErrors(ag.Run(ctx)) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	records := readLLMAgentTraceRecords(t, tracePath)
	if len(records) != 1 {
		t.Fatalf("expected 1 request trace record, got %d", len(records))
	}
	expected := toMessagesWithSanitizer(history, "system prompt", defaultSanitizeToolResultForModel)
	if !reflect.DeepEqual(expected, records[0].Messages) {
		t.Fatalf("unexpected outbound messages\nexpected=%#v\ngot=%#v", expected, records[0].Messages)
	}
}

func readLLMAgentTraceRecords(t *testing.T, path string) []model.RequestTraceRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var out []model.RequestTraceRecord
	for {
		var record model.RequestTraceRecord
		if err := dec.Decode(&record); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatal(err)
		}
		out = append(out, record)
	}
	return out
}

func collectRunErrors(seq iter.Seq2[*session.Event, error]) []error {
	var errs []error
	for _, err := range seq {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
