package policy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

const (
	defaultReadToolName               = "READ"
	readBeforeWriteStateKey           = "policy.read_before_write.read_paths"
	readBeforeWriteIndexReadyStateKey = "policy.read_before_write.index_ready"
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
	args := resolveToolInputArgs(in)
	targetPath := pathArgFromToolCall(args)
	if targetPath == "" {
		in.Decision = Decision{
			Effect: DecisionEffectDeny,
			Reason: fmt.Sprintf("write tool %q requires path arg", in.Call.Name),
		}
		return in, nil
	}
	protectedTarget, statErr := requiresPriorRead(targetPath)
	if statErr != nil {
		// Let the tool itself surface filesystem errors instead of hard-stopping policy chain.
		return in, nil
	}
	if !protectedTarget {
		return in, nil
	}
	if hasReadEvidence(ctx, h.readToolName, targetPath) {
		return in, nil
	}
	in.Decision = Decision{
		Effect: DecisionEffectDeny,
		Reason: fmt.Sprintf("write tool %q requires prior READ of %q", in.Call.Name, targetPath),
	}
	return in, nil
}

func (h readBeforeWriteHook) AfterTool(ctx context.Context, out ToolOutput) (ToolOutput, error) {
	if strings.TrimSpace(out.Call.Name) != h.readToolName {
		return out, nil
	}
	if out.Err != nil || out.Result == nil {
		return out, nil
	}
	readPathRaw, _ := out.Result["path"].(string)
	readPath := normalizePathForComparison(readPathRaw)
	if readPath == "" {
		return out, nil
	}
	_ = persistReadEvidence(ctx, readPath)
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
	if hasReadEvidenceInEvents(ctx, readToolName, targetPath) {
		return true
	}
	if known, allowed := hasReadEvidenceInState(readonlyState(ctx), targetPath); known {
		return allowed
	}
	if known, allowed := hasReadEvidenceInPersistedState(ctx, targetPath); known {
		return allowed
	}
	return hasReadEvidenceViaBackfill(ctx, readToolName, targetPath)
}

func hasReadEvidenceInEvents(ctx context.Context, readToolName string, targetPath string) bool {
	type eventReader interface {
		Events() session.Events
	}
	h, ok := ctx.(eventReader)
	if !ok {
		return false
	}
	for ev := range h.Events().All() {
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

func hasReadEvidenceInState(state session.ReadonlyState, targetPath string) (bool, bool) {
	if state == nil || targetPath == "" {
		return false, false
	}
	if !readPathIndexReady(state) {
		return false, false
	}
	value, ok := state.Get(readBeforeWriteStateKey)
	if !ok {
		return true, false
	}
	return true, readPathIndexContains(value, targetPath)
}

func hasReadEvidenceInPersistedState(ctx context.Context, targetPath string) (bool, bool) {
	stateCtx, ok := session.StateContextFromContext(ctx)
	if !ok || targetPath == "" {
		return false, false
	}
	values, err := stateCtx.Store.SnapshotState(ctx, stateCtx.Session)
	if err != nil {
		return false, false
	}
	if !readPathIndexReadyValues(values) {
		return false, false
	}
	return true, readPathIndexContains(values[readBeforeWriteStateKey], targetPath)
}

func hasReadEvidenceViaBackfill(ctx context.Context, readToolName string, targetPath string) bool {
	stateCtx, ok := session.StateContextFromContext(ctx)
	if !ok || targetPath == "" {
		return false
	}
	events, err := stateCtx.Store.ListEvents(ctx, stateCtx.Session)
	if err != nil {
		return false
	}
	paths := collectReadPaths(events, readToolName)
	_ = persistReadPathIndex(ctx, paths)
	return slices.Contains(paths, targetPath)
}

func readPathIndexReady(state session.ReadonlyState) bool {
	if state == nil {
		return false
	}
	value, ok := state.Get(readBeforeWriteIndexReadyStateKey)
	if !ok {
		return false
	}
	ready, _ := value.(bool)
	return ready
}

func readPathIndexReadyValues(values map[string]any) bool {
	if len(values) == 0 {
		return false
	}
	ready, _ := values[readBeforeWriteIndexReadyStateKey].(bool)
	return ready
}

func readonlyState(ctx context.Context) session.ReadonlyState {
	type stateReader interface {
		ReadonlyState() session.ReadonlyState
	}
	h, ok := ctx.(stateReader)
	if !ok {
		return nil
	}
	return h.ReadonlyState()
}

func collectReadPaths(events []*session.Event, readToolName string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(events))
	for _, ev := range events {
		if ev == nil || ev.Message.ToolResponse == nil {
			continue
		}
		resp := ev.Message.ToolResponse
		if strings.TrimSpace(resp.Name) != readToolName || resp.Result == nil {
			continue
		}
		readPathRaw, _ := resp.Result["path"].(string)
		readPath := normalizePathForComparison(readPathRaw)
		if readPath == "" {
			continue
		}
		if _, exists := seen[readPath]; exists {
			continue
		}
		seen[readPath] = struct{}{}
		out = append(out, readPath)
	}
	slices.Sort(out)
	return out
}

func persistReadEvidence(ctx context.Context, targetPath string) error {
	if targetPath == "" {
		return nil
	}
	return persistReadPathIndex(ctx, []string{targetPath})
}

func persistReadPathIndex(ctx context.Context, paths []string) error {
	stateCtx, ok := session.StateContextFromContext(ctx)
	if !ok {
		return nil
	}
	normalized := make([]string, 0, len(paths))
	for _, one := range paths {
		one = normalizePathForComparison(one)
		if one != "" {
			normalized = append(normalized, one)
		}
	}
	slices.Sort(normalized)
	normalized = slices.Compact(normalized)
	if updater, ok := stateCtx.Store.(session.StateUpdateStore); ok {
		return updater.UpdateState(ctx, stateCtx.Session, func(existing map[string]any) (map[string]any, error) {
			if existing == nil {
				existing = map[string]any{}
			}
			existing[readBeforeWriteStateKey] = mergeReadPathIndex(existing[readBeforeWriteStateKey], normalized)
			existing[readBeforeWriteIndexReadyStateKey] = true
			return existing, nil
		})
	}
	values, err := stateCtx.Store.SnapshotState(ctx, stateCtx.Session)
	if err != nil {
		return err
	}
	if values == nil {
		values = map[string]any{}
	}
	values[readBeforeWriteStateKey] = mergeReadPathIndex(values[readBeforeWriteStateKey], normalized)
	values[readBeforeWriteIndexReadyStateKey] = true
	return stateCtx.Store.ReplaceState(ctx, stateCtx.Session, values)
}

func mergeReadPathIndex(existing any, additions []string) []string {
	all := readPathIndexValues(existing)
	all = append(all, additions...)
	slices.Sort(all)
	return slices.Compact(all)
}

func readPathIndexContains(value any, targetPath string) bool {
	if targetPath == "" {
		return false
	}
	values := readPathIndexValues(value)
	return slices.Contains(values, targetPath)
}

func readPathIndexValues(value any) []string {
	switch typed := value.(type) {
	case []string:
		out := append([]string(nil), typed...)
		slices.Sort(out)
		return slices.Compact(out)
	case []any:
		out := make([]string, 0, len(typed))
		for _, one := range typed {
			text, _ := one.(string)
			text = normalizePathForComparison(text)
			if text != "" {
				out = append(out, text)
			}
		}
		slices.Sort(out)
		return slices.Compact(out)
	default:
		return nil
	}
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

func requiresPriorRead(targetPath string) (bool, error) {
	info, err := os.Stat(targetPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, nil
	}
	return info.Size() > 0, nil
}
