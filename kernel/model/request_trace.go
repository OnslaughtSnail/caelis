package model

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	RequestTraceEnvVar   = "CAELIS_TRACE_REQUESTS"
	RequestTraceFileName = "requests.jsonl"
)

type requestTraceContextKey struct{}

type RequestTraceContext struct {
	SessionID string
	RunID     string
	Path      string
}

type RequestTraceRecord struct {
	SessionID    string          `json:"session_id,omitempty"`
	RunID        string          `json:"run_id,omitempty"`
	Model        string          `json:"model,omitempty"`
	Provider     string          `json:"provider,omitempty"`
	Instructions []Part          `json:"instructions,omitempty"`
	Messages     []Message       `json:"messages,omitempty"`
	Tools        []ToolSpec      `json:"tools,omitempty"`
	Output       *OutputSpec     `json:"output,omitempty"`
	Reasoning    ReasoningConfig `json:"reasoning,omitempty"`
	Time         time.Time       `json:"time"`
}

type requestTraceProvider interface {
	ProviderName() string
}

type requestTraceContextWindow interface {
	ContextWindowTokens() int
}

type requestTraceLLM struct {
	base LLM
}

// WithRequestTraceContext annotates a context with request-trace metadata.
func WithRequestTraceContext(ctx context.Context, info RequestTraceContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestTraceContextKey{}, info)
}

// RequestTraceContextFromContext returns request-trace metadata from context.
func RequestTraceContextFromContext(ctx context.Context) (RequestTraceContext, bool) {
	if ctx == nil {
		return RequestTraceContext{}, false
	}
	info, ok := ctx.Value(requestTraceContextKey{}).(RequestTraceContext)
	if !ok {
		return RequestTraceContext{}, false
	}
	return info, true
}

// RequestTracingEnabled returns true when outbound model-request tracing is enabled.
func RequestTracingEnabled() bool {
	return strings.TrimSpace(os.Getenv(RequestTraceEnvVar)) != ""
}

// WrapRequestTrace adds optional outbound request tracing around an LLM.
func WrapRequestTrace(llm LLM) LLM {
	if llm == nil {
		return nil
	}
	if _, ok := llm.(*requestTraceLLM); ok {
		return llm
	}
	return &requestTraceLLM{base: llm}
}

func (l *requestTraceLLM) Name() string {
	if l == nil || l.base == nil {
		return ""
	}
	return l.base.Name()
}

func (l *requestTraceLLM) ProviderName() string {
	if l == nil || l.base == nil {
		return ""
	}
	if named, ok := l.base.(requestTraceProvider); ok {
		return strings.TrimSpace(named.ProviderName())
	}
	return ""
}

func (l *requestTraceLLM) ContextWindowTokens() int {
	if l == nil || l.base == nil {
		return 0
	}
	if capped, ok := l.base.(requestTraceContextWindow); ok {
		return capped.ContextWindowTokens()
	}
	return 0
}

func (l *requestTraceLLM) Generate(ctx context.Context, req *Request) iter.Seq2[*StreamEvent, error] {
	if l == nil || l.base == nil {
		return func(yield func(*StreamEvent, error) bool) {
			yield(nil, fmt.Errorf("model: request trace wrapper has nil base llm"))
		}
	}
	if RequestTracingEnabled() {
		_ = appendRequestTrace(ctx, l, req)
	}
	return l.base.Generate(ctx, req)
}

func appendRequestTrace(ctx context.Context, llm LLM, req *Request) error {
	info, ok := RequestTraceContextFromContext(ctx)
	if !ok || strings.TrimSpace(info.Path) == "" {
		return nil
	}
	record := RequestTraceRecord{
		SessionID:    strings.TrimSpace(info.SessionID),
		RunID:        strings.TrimSpace(info.RunID),
		Model:        strings.TrimSpace(llm.Name()),
		Instructions: clonePartsForTrace(req),
		Messages:     cloneMessagesForTrace(req),
		Tools:        cloneToolsForTrace(req),
		Time:         time.Now(),
	}
	if named, ok := llm.(requestTraceProvider); ok {
		record.Provider = strings.TrimSpace(named.ProviderName())
	}
	if req != nil {
		record.Reasoning = req.Reasoning
		if req.Output != nil {
			output := *req.Output
			if len(output.JSONSchema) > 0 {
				output.JSONSchema = cloneAnyMap(output.JSONSchema)
			}
			record.Output = &output
		}
	}
	if err := os.MkdirAll(filepath.Dir(info.Path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(info.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = f.Write(append(raw, '\n'))
	return err
}

func cloneMessagesForTrace(req *Request) []Message {
	if req == nil || len(req.Messages) == 0 {
		return nil
	}
	return CloneMessages(req.Messages)
}

func clonePartsForTrace(req *Request) []Part {
	if req == nil || len(req.Instructions) == 0 {
		return nil
	}
	return CloneParts(req.Instructions)
}

func cloneToolsForTrace(req *Request) []ToolSpec {
	if req == nil || len(req.Tools) == 0 {
		return nil
	}
	out := make([]ToolSpec, 0, len(req.Tools))
	for _, def := range req.Tools {
		cp := def
		if def.Function != nil {
			fn := *def.Function
			fn.Parameters = cloneAnyMap(def.Function.Parameters)
			cp.Function = &fn
		}
		if def.ProviderDefined != nil {
			pd := *def.ProviderDefined
			pd.ProviderDetails = cloneRawMessageMap(def.ProviderDefined.ProviderDetails)
			cp.ProviderDefined = &pd
		}
		if def.ProviderExecuted != nil {
			pe := *def.ProviderExecuted
			pe.ProviderDetails = cloneRawMessageMap(def.ProviderExecuted.ProviderDetails)
			cp.ProviderExecuted = &pe
		}
		if def.MCP != nil {
			mcp := *def.MCP
			cp.MCP = &mcp
		}
		out = append(out, cp)
	}
	return out
}
