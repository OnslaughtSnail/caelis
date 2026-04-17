package policy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/tool/capability"
)

const (
	defaultReadToolName               = "READ"
	readBeforeWriteStateKey           = "policy.read_before_write.read_paths"
	readBeforeWriteIndexReadyStateKey = "policy.read_before_write.index_ready"
	readBeforeWriteSafeWriteStateKey  = "policy.read_before_write.safe_write_paths"
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
	readToolName := strings.ToUpper(strings.TrimSpace(cfg.ReadToolName))
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
	if !in.Capability.HasOperation(capability.OperationFileWrite) {
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
	if hasSafeWriteEvidence(ctx, targetPath) {
		return in, nil
	}
	in.Decision = Decision{
		Effect: DecisionEffectDeny,
		Reason: fmt.Sprintf("write tool %q requires prior READ of %q", in.Call.Name, targetPath),
	}
	return in, nil
}

func (h readBeforeWriteHook) AfterTool(ctx context.Context, out ToolOutput) (ToolOutput, error) {
	callName := strings.ToUpper(strings.TrimSpace(out.Call.Name))
	if callName == h.readToolName {
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
	if out.Err != nil || out.Result == nil || !isSafeWriteBootstrapResult(callName, out.Result) {
		return out, nil
	}
	writePathRaw, _ := out.Result["path"].(string)
	writePath := normalizePathForComparison(writePathRaw)
	if writePath == "" {
		return out, nil
	}
	_ = persistSafeWriteEvidence(ctx, writePath)
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

func hasSafeWriteEvidence(ctx context.Context, targetPath string) bool {
	if hasSafeWriteEvidenceInEvents(ctx, targetPath) {
		return true
	}
	if known, allowed := hasSafeWriteEvidenceInState(readonlyState(ctx), targetPath); known {
		return allowed
	}
	if known, allowed := hasSafeWriteEvidenceInPersistedState(ctx, targetPath); known {
		return allowed
	}
	return hasSafeWriteEvidenceViaBackfill(ctx, targetPath)
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
		if ev == nil {
			continue
		}
		resp := ev.Message.ToolResponse()
		if resp == nil {
			continue
		}
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

func hasSafeWriteEvidenceInEvents(ctx context.Context, targetPath string) bool {
	type eventReader interface {
		Events() session.Events
	}
	h, ok := ctx.(eventReader)
	if !ok {
		return false
	}
	for ev := range h.Events().All() {
		if ev == nil {
			continue
		}
		resp := ev.Message.ToolResponse()
		if resp == nil || !isSafeWriteBootstrapResult(strings.ToUpper(strings.TrimSpace(resp.Name)), resp.Result) {
			continue
		}
		writePathRaw, _ := resp.Result["path"].(string)
		if normalizePathForComparison(writePathRaw) == targetPath {
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
	values, err := stateCtx.StateStore.SnapshotState(ctx, stateCtx.Session)
	if err != nil {
		return false, false
	}
	if !readPathIndexReadyValues(values) {
		return false, false
	}
	return true, readPathIndexContains(values[readBeforeWriteStateKey], targetPath)
}

func hasSafeWriteEvidenceInState(state session.ReadonlyState, targetPath string) (bool, bool) {
	if state == nil || targetPath == "" {
		return false, false
	}
	value, ok := state.Get(readBeforeWriteSafeWriteStateKey)
	if !ok {
		return false, false
	}
	return true, readPathIndexContains(value, targetPath)
}

func hasSafeWriteEvidenceInPersistedState(ctx context.Context, targetPath string) (bool, bool) {
	stateCtx, ok := session.StateContextFromContext(ctx)
	if !ok || targetPath == "" {
		return false, false
	}
	values, err := stateCtx.StateStore.SnapshotState(ctx, stateCtx.Session)
	if err != nil {
		return false, false
	}
	value, ok := values[readBeforeWriteSafeWriteStateKey]
	if !ok {
		return false, false
	}
	return true, readPathIndexContains(value, targetPath)
}

func hasReadEvidenceViaBackfill(ctx context.Context, readToolName string, targetPath string) bool {
	stateCtx, ok := session.StateContextFromContext(ctx)
	if !ok || targetPath == "" {
		return false
	}
	if stateCtx.LogStore == nil {
		return false
	}
	events, err := stateCtx.LogStore.ListEvents(ctx, stateCtx.Session)
	if err != nil {
		return false
	}
	paths := collectReadPaths(events, readToolName)
	_ = persistReadPathIndex(ctx, paths)
	return slices.Contains(paths, targetPath)
}

func hasSafeWriteEvidenceViaBackfill(ctx context.Context, targetPath string) bool {
	stateCtx, ok := session.StateContextFromContext(ctx)
	if !ok || targetPath == "" {
		return false
	}
	if stateCtx.LogStore == nil {
		return false
	}
	events, err := stateCtx.LogStore.ListEvents(ctx, stateCtx.Session)
	if err != nil {
		return false
	}
	paths := collectSafeWritePaths(events)
	_ = persistSafeWritePathIndex(ctx, paths)
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
		if ev == nil {
			continue
		}
		resp := ev.Message.ToolResponse()
		if resp == nil {
			continue
		}
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

func collectSafeWritePaths(events []*session.Event) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		resp := ev.Message.ToolResponse()
		if resp == nil || !isSafeWriteBootstrapResult(strings.ToUpper(strings.TrimSpace(resp.Name)), resp.Result) {
			continue
		}
		writePathRaw, _ := resp.Result["path"].(string)
		writePath := normalizePathForComparison(writePathRaw)
		if writePath == "" {
			continue
		}
		if _, exists := seen[writePath]; exists {
			continue
		}
		seen[writePath] = struct{}{}
		out = append(out, writePath)
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

func persistSafeWriteEvidence(ctx context.Context, targetPath string) error {
	if targetPath == "" {
		return nil
	}
	return persistSafeWritePathIndex(ctx, []string{targetPath})
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
	if stateCtx.StateUpdater != nil {
		return stateCtx.StateUpdater.UpdateState(ctx, stateCtx.Session, func(existing map[string]any) (map[string]any, error) {
			if existing == nil {
				existing = map[string]any{}
			}
			existing[readBeforeWriteStateKey] = mergeReadPathIndex(existing[readBeforeWriteStateKey], normalized)
			existing[readBeforeWriteIndexReadyStateKey] = true
			return existing, nil
		})
	}
	values, err := stateCtx.StateStore.SnapshotState(ctx, stateCtx.Session)
	if err != nil {
		return err
	}
	if values == nil {
		values = map[string]any{}
	}
	values[readBeforeWriteStateKey] = mergeReadPathIndex(values[readBeforeWriteStateKey], normalized)
	values[readBeforeWriteIndexReadyStateKey] = true
	return stateCtx.StateStore.ReplaceState(ctx, stateCtx.Session, values)
}

func persistSafeWritePathIndex(ctx context.Context, paths []string) error {
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
	if stateCtx.StateUpdater != nil {
		return stateCtx.StateUpdater.UpdateState(ctx, stateCtx.Session, func(existing map[string]any) (map[string]any, error) {
			if existing == nil {
				existing = map[string]any{}
			}
			existing[readBeforeWriteSafeWriteStateKey] = mergeReadPathIndex(existing[readBeforeWriteSafeWriteStateKey], normalized)
			return existing, nil
		})
	}
	values, err := stateCtx.StateStore.SnapshotState(ctx, stateCtx.Session)
	if err != nil {
		return err
	}
	if values == nil {
		values = map[string]any{}
	}
	values[readBeforeWriteSafeWriteStateKey] = mergeReadPathIndex(values[readBeforeWriteSafeWriteStateKey], normalized)
	return stateCtx.StateStore.ReplaceState(ctx, stateCtx.Session, values)
}

func isSafeWriteBootstrapResult(toolName string, result map[string]any) bool {
	if len(result) == 0 {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(toolName)) {
	case "WRITE", "PATCH":
	default:
		return false
	}
	created, _ := result["created"].(bool)
	if created {
		return true
	}
	previousEmpty, _ := result["previous_empty"].(bool)
	return previousEmpty
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
