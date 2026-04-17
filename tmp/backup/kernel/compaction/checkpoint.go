package compaction

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

const CheckpointVersion = 2

type Checkpoint struct {
	Version               int      `json:"version,omitempty"`
	Objective             string   `json:"objective,omitempty"`
	UserConstraints       []string `json:"user_constraints,omitempty"`
	DurableDecisions      []string `json:"durable_decisions,omitempty"`
	VerifiedFacts         []string `json:"verified_facts,omitempty"`
	CurrentProgress       []string `json:"current_progress,omitempty"`
	OpenQuestionsAndRisks []string `json:"open_questions_and_risks,omitempty"`
	NextActions           []string `json:"next_actions,omitempty"`
	ActiveTasks           []string `json:"active_tasks,omitempty"`
	LatestBlockers        []string `json:"latest_blockers,omitempty"`
}

func (c Checkpoint) HasContent() bool {
	return strings.TrimSpace(c.Objective) != "" ||
		len(c.UserConstraints) > 0 ||
		len(c.DurableDecisions) > 0 ||
		len(c.VerifiedFacts) > 0 ||
		len(c.CurrentProgress) > 0 ||
		len(c.OpenQuestionsAndRisks) > 0 ||
		len(c.NextActions) > 0 ||
		len(c.ActiveTasks) > 0 ||
		len(c.LatestBlockers) > 0
}

func NormalizeCheckpoint(cp Checkpoint) Checkpoint {
	cp.Version = max(cp.Version, CheckpointVersion)
	cp.Objective = normalizeCheckpointScalar(cp.Objective, 280)
	cp.UserConstraints = normalizeCheckpointList(cp.UserConstraints, 8, 240)
	cp.DurableDecisions = normalizeCheckpointList(cp.DurableDecisions, 8, 240)
	cp.VerifiedFacts = normalizeCheckpointList(cp.VerifiedFacts, 10, 260)
	cp.CurrentProgress = normalizeCheckpointList(cp.CurrentProgress, 8, 240)
	cp.OpenQuestionsAndRisks = normalizeCheckpointList(cp.OpenQuestionsAndRisks, 8, 240)
	cp.NextActions = normalizeCheckpointList(cp.NextActions, 6, 220)
	cp.ActiveTasks = normalizeCheckpointList(cp.ActiveTasks, 6, 220)
	cp.LatestBlockers = normalizeCheckpointList(cp.LatestBlockers, 6, 220)
	return cp
}

func MergeCheckpoints(base, update Checkpoint, runtimeState RuntimeState) Checkpoint {
	base = NormalizeCheckpoint(base)
	update = NormalizeCheckpoint(update)
	out := base
	if update.Objective != "" {
		out.Objective = update.Objective
	}
	out.UserConstraints = mergeCheckpointLists(base.UserConstraints, update.UserConstraints)
	out.DurableDecisions = mergeCheckpointLists(base.DurableDecisions, update.DurableDecisions)
	out.VerifiedFacts = mergeCheckpointLists(base.VerifiedFacts, update.VerifiedFacts)
	out.ActiveTasks = mergeCheckpointLists(update.ActiveTasks, runtimeStateLines(runtimeState.ActiveTasksSummary), base.ActiveTasks)
	out.LatestBlockers = mergeCheckpointLists(update.LatestBlockers, runtimeStateLines(runtimeState.LatestBlockerSummary), base.LatestBlockers)
	out.CurrentProgress = mergeCheckpointLists(update.CurrentProgress, runtimeStateLines(runtimeState.ProgressSummary), base.CurrentProgress)
	if len(out.ActiveTasks) > 0 {
		out.CurrentProgress = mergeCheckpointLists(prefixCheckpointItems("Active task: ", out.ActiveTasks), out.CurrentProgress)
	}
	out.OpenQuestionsAndRisks = mergeCheckpointLists(update.OpenQuestionsAndRisks, base.OpenQuestionsAndRisks)
	if len(out.LatestBlockers) > 0 {
		out.OpenQuestionsAndRisks = mergeCheckpointLists(prefixCheckpointItems("Blocker: ", out.LatestBlockers), out.OpenQuestionsAndRisks)
	}
	out.NextActions = mergeCheckpointLists(update.NextActions, runtimeStateLines(runtimeState.PlanSummary), out.ActiveTasks, base.NextActions)
	if len(out.NextActions) == 0 {
		out.NextActions = defaultNextActions(out)
	}
	return NormalizeCheckpoint(out)
}

func RenderCheckpointMarkdown(cp Checkpoint) string {
	cp = NormalizeCheckpoint(cp)
	var b strings.Builder
	writeScalarSection(&b, "## Active Objective", cp.Objective, "unknown")
	writeListSection(&b, "## Durable Constraints", cp.UserConstraints, "unknown")
	writeListSection(&b, "## Durable Decisions", cp.DurableDecisions, "none retained")
	writeListSection(&b, "## Verified Facts And References", cp.VerifiedFacts, "unknown")
	writeListSection(&b, "## Current Progress", cp.CurrentProgress, "unknown")
	writeListSection(&b, "## Open Questions And Risks", cp.OpenQuestionsAndRisks, "none")
	writeNumberedSection(&b, "## Immediate Next Actions", cp.NextActions, defaultNextActions(cp))
	return strings.TrimSpace(b.String())
}

func ParseCheckpointMarkdown(text string) Checkpoint {
	text = normalizeMultilineText(text)
	if text == "" {
		return Checkpoint{Version: CheckpointVersion}
	}
	sections := map[string][]string{}
	var current string
	inRuntimeState := false
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.EqualFold(line, "<runtime_state>") {
			inRuntimeState = true
			continue
		}
		if strings.EqualFold(line, "</runtime_state>") {
			inRuntimeState = false
			continue
		}
		if inRuntimeState {
			continue
		}
		if heading, ok := checkpointHeading(line); ok {
			current = heading
			continue
		}
		if current == "" {
			continue
		}
		sections[current] = append(sections[current], line)
	}
	cp := Checkpoint{
		Version:               CheckpointVersion,
		Objective:             checkpointScalar(sections["objective"]),
		UserConstraints:       checkpointList(sections["constraints"]),
		DurableDecisions:      checkpointList(sections["decisions"]),
		VerifiedFacts:         checkpointList(sections["facts"]),
		CurrentProgress:       checkpointList(sections["progress"]),
		OpenQuestionsAndRisks: checkpointList(sections["risks"]),
		NextActions:           checkpointList(sections["next_actions"]),
	}
	return NormalizeCheckpoint(cp)
}

func CheckpointMeta(cp Checkpoint) map[string]any {
	cp = NormalizeCheckpoint(cp)
	return map[string]any{
		"version":                  cp.Version,
		"objective":                cp.Objective,
		"user_constraints":         append([]string(nil), cp.UserConstraints...),
		"durable_decisions":        append([]string(nil), cp.DurableDecisions...),
		"verified_facts":           append([]string(nil), cp.VerifiedFacts...),
		"current_progress":         append([]string(nil), cp.CurrentProgress...),
		"open_questions_and_risks": append([]string(nil), cp.OpenQuestionsAndRisks...),
		"next_actions":             append([]string(nil), cp.NextActions...),
		"active_tasks":             append([]string(nil), cp.ActiveTasks...),
		"latest_blockers":          append([]string(nil), cp.LatestBlockers...),
	}
}

func CheckpointFromMeta(raw any) (Checkpoint, bool) {
	switch typed := raw.(type) {
	case map[string]any:
		cp := Checkpoint{
			Version:               intFromAny(typed["version"]),
			Objective:             stringFromAny(typed["objective"]),
			UserConstraints:       stringsFromAny(typed["user_constraints"]),
			DurableDecisions:      stringsFromAny(typed["durable_decisions"]),
			VerifiedFacts:         stringsFromAny(typed["verified_facts"]),
			CurrentProgress:       stringsFromAny(typed["current_progress"]),
			OpenQuestionsAndRisks: stringsFromAny(typed["open_questions_and_risks"]),
			NextActions:           stringsFromAny(typed["next_actions"]),
			ActiveTasks:           stringsFromAny(typed["active_tasks"]),
			LatestBlockers:        stringsFromAny(typed["latest_blockers"]),
		}
		cp = NormalizeCheckpoint(cp)
		return cp, cp.HasContent()
	default:
		return Checkpoint{}, false
	}
}

func CheckpointFromEvent(ev *session.Event) (Checkpoint, bool) {
	if ev == nil || ev.Meta == nil {
		return Checkpoint{}, false
	}
	raw, ok := ev.Meta["compaction"]
	if ok {
		meta, _ := raw.(map[string]any)
		if meta != nil {
			if cp, ok := CheckpointFromMeta(meta["checkpoint"]); ok {
				return cp, true
			}
		}
	}
	cp := ParseCheckpointMarkdown(ev.Message.TextContent())
	return cp, cp.HasContent()
}

func HeuristicFallbackCheckpoint(events []*session.Event, prior Checkpoint, runtimeState RuntimeState, inputBudget int) Checkpoint {
	objective := heuristicObjective(events)
	if strings.TrimSpace(objective) == "" {
		objective = strings.TrimSpace(prior.Objective)
	}
	update := Checkpoint{
		Objective:             objective,
		VerifiedFacts:         extractReferenceFacts(events),
		CurrentProgress:       mergeCheckpointLists([]string{heuristicProgress(events)}, runtimeStateLines(runtimeState.ProgressSummary)),
		OpenQuestionsAndRisks: mergeCheckpointLists(heuristicRisks(events), runtimeStateLines(runtimeState.LatestBlockerSummary)),
		ActiveTasks:           runtimeStateLines(runtimeState.ActiveTasksSummary),
		NextActions:           runtimeStateLines(runtimeState.PlanSummary),
	}
	if inputBudget > 0 {
		update.OpenQuestionsAndRisks = mergeCheckpointLists(
			[]string{fmt.Sprintf("Checkpoint rebuilt via heuristic fallback under a %d token summary budget; verify ambiguous details before large mutations.", inputBudget)},
			update.OpenQuestionsAndRisks,
		)
	}
	return MergeCheckpoints(prior, update, runtimeState)
}

func CompactRuntimeStateBlock(state RuntimeState) string {
	return FormatInjectedRuntimeState(state)
}

func runtimeStateLines(text string) []string {
	text = normalizeMultilineText(text)
	if text == "" {
		return nil
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '|'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = normalizeCheckpointItem(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return normalizeCheckpointList(out, 8, 220)
}

func defaultNextActions(cp Checkpoint) []string {
	fallback := []string{}
	if strings.TrimSpace(cp.Objective) != "" {
		fallback = append(fallback, "Continue the active task against the current objective: "+cp.Objective)
	}
	fallback = append(fallback, "Verify any uncertain state with local files or tools before major edits.")
	return normalizeCheckpointList(fallback, 3, 220)
}

func writeScalarSection(b *strings.Builder, heading string, value string, fallback string) {
	b.WriteString(heading)
	b.WriteString("\n")
	value = normalizeCheckpointScalar(value, 280)
	if value == "" {
		value = fallback
	}
	b.WriteString("- ")
	b.WriteString(value)
	b.WriteString("\n\n")
}

func writeListSection(b *strings.Builder, heading string, items []string, fallback string) {
	b.WriteString(heading)
	b.WriteString("\n")
	items = normalizeCheckpointList(items, 10, 260)
	if len(items) == 0 {
		b.WriteString("- ")
		b.WriteString(fallback)
		b.WriteString("\n\n")
		return
	}
	for _, item := range items {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeNumberedSection(b *strings.Builder, heading string, items []string, fallback []string) {
	b.WriteString(heading)
	b.WriteString("\n")
	items = normalizeCheckpointList(items, 6, 220)
	if len(items) == 0 {
		items = fallback
	}
	for i, item := range items {
		fmt.Fprintf(b, "%d. %s\n", i+1, item)
	}
	b.WriteString("\n")
}

func checkpointHeading(line string) (string, bool) {
	trimmed := strings.TrimSpace(strings.TrimLeft(line, "#"))
	trimmed = strings.ToLower(strings.TrimSpace(trimmed))
	switch trimmed {
	case "active objective", "active objectives", "objective":
		return "objective", true
	case "durable constraints", "constraints", "user constraints", "constraints and preferences":
		return "constraints", true
	case "durable decisions", "decisions", "key decisions":
		return "decisions", true
	case "verified facts and references", "verified facts", "references":
		return "facts", true
	case "current progress", "current status":
		return "progress", true
	case "open questions and risks", "open questions", "risks", "risks and unknowns":
		return "risks", true
	case "immediate next actions", "next actions", "next steps":
		return "next_actions", true
	default:
		return "", false
	}
}

func checkpointScalar(lines []string) string {
	items := checkpointList(lines)
	if len(items) == 0 {
		return ""
	}
	return items[0]
}

func checkpointList(lines []string) []string {
	items := make([]string, 0, len(lines))
	for _, line := range lines {
		line = normalizeCheckpointItem(line)
		if line != "" {
			items = append(items, line)
		}
	}
	return normalizeCheckpointList(items, 10, 260)
}

func normalizeCheckpointList(items []string, maxItems int, maxRunes int) []string {
	if maxItems <= 0 {
		return nil
	}
	out := make([]string, 0, min(len(items), maxItems))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = normalizeCheckpointItem(item)
		if item == "" {
			continue
		}
		if maxRunes > 0 {
			item = ClipText(item, maxRunes)
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

func normalizeCheckpointScalar(text string, maxRunes int) string {
	text = normalizeCheckpointItem(text)
	if text == "" {
		return ""
	}
	if maxRunes > 0 {
		text = ClipText(text, maxRunes)
	}
	return text
}

func normalizeCheckpointItem(text string) string {
	text = normalizeMultilineText(text)
	if text == "" {
		return ""
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, "-"))
	text = strings.TrimSpace(strings.TrimPrefix(text, "*"))
	if idx := strings.Index(text, ". "); idx > 0 {
		if allDigits(text[:idx]) {
			text = strings.TrimSpace(text[idx+2:])
		}
	}
	switch strings.ToLower(text) {
	case "", "unknown", "none", "none retained", "n/a":
		return ""
	default:
		return text
	}
}

func mergeCheckpointLists(groups ...[]string) []string {
	out := []string{}
	for _, group := range groups {
		out = append(out, group...)
	}
	return normalizeCheckpointList(out, 12, 260)
}

func prefixCheckpointItems(prefix string, items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = normalizeCheckpointItem(item)
		if item == "" {
			continue
		}
		out = append(out, prefix+item)
	}
	return out
}

func heuristicObjective(events []*session.Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev == nil || ev.Message.Role != model.RoleUser {
			continue
		}
		if text := normalizeCheckpointScalar(ev.Message.TextContent(), 280); text != "" {
			return text
		}
	}
	return ""
}

func heuristicProgress(events []*session.Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev == nil {
			continue
		}
		if resp := ev.Message.ToolResponse(); resp != nil {
			if text := normalizeCheckpointScalar(stringFromAny(resp.Result["summary"]), 240); text != "" {
				return text
			}
			if text := normalizeCheckpointScalar(stringFromAny(resp.Result["msg"]), 240); text != "" {
				return text
			}
		}
		if ev.Message.Role == model.RoleAssistant {
			if text := normalizeCheckpointScalar(ev.Message.TextContent(), 240); text != "" {
				return text
			}
		}
	}
	return "Continue from the retained recent context."
}

func heuristicRisks(events []*session.Event) []string {
	out := []string{}
	for i := len(events) - 1; i >= 0 && len(out) < 3; i-- {
		ev := events[i]
		if ev == nil {
			continue
		}
		if resp := ev.Message.ToolResponse(); resp != nil {
			if text := normalizeCheckpointScalar(stringFromAny(resp.Result["error"]), 220); text != "" {
				out = append(out, text)
			}
		}
	}
	return normalizeCheckpointList(out, 3, 220)
}

var referencePattern = regexp.MustCompile(`(?i)(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+|[A-Za-z0-9_.-]+\.[A-Za-z0-9_-]+|task-[A-Za-z0-9_-]+|call_[A-Za-z0-9_-]+`)

func extractReferenceFacts(events []*session.Event) []string {
	out := []string{}
	for i := max(len(events)-12, 0); i < len(events); i++ {
		ev := events[i]
		if ev == nil {
			continue
		}
		text := EventToText(ev)
		for _, match := range referencePattern.FindAllString(text, -1) {
			match = normalizeCheckpointItem(match)
			if match != "" {
				out = append(out, match)
			}
		}
	}
	return normalizeCheckpointList(out, 8, 200)
}

func normalizeMultilineText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.TrimSpace(text)
}

func allDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func stringFromAny(raw any) string {
	return strings.TrimSpace(fmt.Sprint(raw))
}

func intFromAny(raw any) int {
	switch typed := raw.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func stringsFromAny(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text := normalizeCheckpointItem(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	case json.RawMessage:
		var out []string
		if err := json.Unmarshal(typed, &out); err == nil {
			return out
		}
		return nil
	default:
		return nil
	}
}
