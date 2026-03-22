package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"os"
	"path/filepath"
	"testing"
)

type traceTestLLM struct{}

func (l *traceTestLLM) Name() string { return "trace-test-model" }

func (l *traceTestLLM) ProviderName() string { return "trace-test-provider" }

func (l *traceTestLLM) Generate(context.Context, *Request) iter.Seq2[*Response, error] {
	return func(yield func(*Response, error) bool) {
		yield(&Response{Message: Message{Role: RoleAssistant, Text: "ok"}, TurnComplete: true}, nil)
	}
}

func TestRequestTraceWrapperWritesOneOutboundRecord(t *testing.T) {
	t.Setenv(RequestTraceEnvVar, "1")
	path := filepath.Join(t.TempDir(), "requests.jsonl")
	ctx := WithRequestTraceContext(context.Background(), RequestTraceContext{
		SessionID: "s-trace",
		RunID:     "r-trace",
		Path:      path,
	})
	llm := WrapRequestTrace(&traceTestLLM{})
	req := &Request{
		Messages: []Message{
			{Role: RoleSystem, Text: "system"},
			{Role: RoleUser, Text: "hello"},
		},
		Stream: true,
		Reasoning: ReasoningConfig{
			Effort: "high",
		},
	}
	for _, err := range llm.Generate(ctx, req) {
		if err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var record RequestTraceRecord
	if err := dec.Decode(&record); err != nil {
		t.Fatal(err)
	}
	if record.SessionID != "s-trace" || record.RunID != "r-trace" {
		t.Fatalf("unexpected trace identity: %+v", record)
	}
	if record.Model != "trace-test-model" || record.Provider != "trace-test-provider" {
		t.Fatalf("unexpected trace model metadata: %+v", record)
	}
	if len(record.Messages) != 2 || record.Messages[1].TextContent() != "hello" {
		t.Fatalf("unexpected traced messages: %+v", record.Messages)
	}
	if !record.Stream || record.Reasoning.Effort != "high" {
		t.Fatalf("unexpected trace request flags: %+v", record)
	}
	var extra RequestTraceRecord
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("expected single request trace record, got extra=%+v err=%v", extra, err)
	}
}
