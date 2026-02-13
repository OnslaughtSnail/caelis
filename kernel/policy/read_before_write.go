package policy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

const (
	defaultReadToolName = "READ"
)

type ReadBeforeWriteConfig struct {
	ReadToolName string
}

type readBeforeWriteHook struct {
	name         string
	readToolName string
}

func RequireReadBeforeWrite(cfg ReadBeforeWriteConfig) Hook {
	name := "require_read_before_write"
	readToolName := strings.TrimSpace(cfg.ReadToolName)
	if readToolName == "" {
		readToolName = defaultReadToolName
	}
	return readBeforeWriteHook{
		name:         name,
		readToolName: readToolName,
	}
}

func (h readBeforeWriteHook) Name() string {
	return h.name
}

func (h readBeforeWriteHook) BeforeModel(ctx context.Context, in ModelInput) (ModelInput, error) {
	_ = ctx
	return in, nil
}

func (h readBeforeWriteHook) BeforeTool(ctx context.Context, in ToolInput) (ToolInput, error) {
	if !in.Capability.HasOperation(toolcap.OperationFileWrite) {
		return in, nil
	}
	targetPath := pathArgFromToolCall(in.Call.Args)
	if targetPath == "" {
		return in, fmt.Errorf("policy: write tool %q requires path arg", in.Call.Name)
	}
	if hasReadEvidence(ctx, h.readToolName, targetPath) {
		return in, nil
	}
	return in, fmt.Errorf("policy: write tool %q requires prior READ of %q", in.Call.Name, targetPath)
}

func (h readBeforeWriteHook) AfterTool(ctx context.Context, out ToolOutput) (ToolOutput, error) {
	_ = ctx
	return out, nil
}

func (h readBeforeWriteHook) BeforeOutput(ctx context.Context, out Output) (Output, error) {
	_ = ctx
	return out, nil
}

func pathArgFromToolCall(args map[string]any) string {
	if args == nil {
		return ""
	}
	value, ok := args["path"].(string)
	if !ok {
		return ""
	}
	return normalizePathForComparison(value)
}

func hasReadEvidence(ctx context.Context, readToolName string, targetPath string) bool {
	type historyReader interface {
		History() []*session.Event
	}
	h, ok := ctx.(historyReader)
	if !ok {
		return false
	}
	for _, ev := range h.History() {
		if ev == nil || ev.Message.ToolResponse == nil {
			continue
		}
		resp := ev.Message.ToolResponse
		if strings.TrimSpace(resp.Name) != readToolName {
			continue
		}
		if resp.Result == nil {
			continue
		}
		readPathRaw, ok := resp.Result["path"].(string)
		if !ok {
			continue
		}
		if normalizePathForComparison(readPathRaw) == targetPath {
			return true
		}
	}
	return false
}

func normalizePathForComparison(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err == nil {
			path = filepath.Join(cwd, path)
		}
	}
	return filepath.Clean(path)
}
