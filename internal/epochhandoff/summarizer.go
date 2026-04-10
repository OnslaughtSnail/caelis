package epochhandoff

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	coreacpmeta "github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

// ────────────────────────────────────────────────────────────────────────────
// EpochSummarizer — checkpoint generation via rules (Mode A).
// ────────────────────────────────────────────────────────────────────────────

// BuildCheckpoint generates a canonical EpochCheckpoint from session events
// using deterministic rule extraction (Mode A). It does NOT require an LLM
// call. The checkpoint captures the current epoch's execution results in the
// two-tier schema (SystemFields + LLMFields).
//
// Parameters:
//   - events: all session events (the summarizer will extract checkpoint and
//     recent history from these)
//   - epoch: the current controller epoch being summarized
//   - checkpointIDFunc: generates a unique checkpoint ID (pass nil to use default)
func BuildCheckpoint(events []*session.Event, epoch coreacpmeta.ControllerEpoch, checkpointIDFunc func() string) EpochCheckpoint {
	if checkpointIDFunc == nil {
		checkpointIDFunc = defaultCheckpointID
	}

	cp := EpochCheckpoint{
		System: SystemFields{
			CheckpointID:   checkpointIDFunc(),
			EpochID:        epoch.EpochID,
			ControllerKind: epoch.ControllerKind,
			ControllerID:   epoch.ControllerID,
			CreatedAt:      time.Now(),
			CreatedBy:      "rule",
			Mode:           CheckpointModeFull,
			Version:        1,
			SchemaVersion:  SchemaVersion,
		},
	}

	if len(events) == 0 {
		cp.ComputeHash()
		return cp
	}

	// Set source event range.
	if first := firstNonNilEvent(events); first != nil {
		cp.System.SourceEventStart = eventIDOrTime(first)
	}
	if last := lastNonNilEvent(events); last != nil {
		cp.System.SourceEventEnd = eventIDOrTime(last)
		cp.System.WatermarkEventID = eventIDOrTime(last)
	}

	// Extract existing compaction checkpoint and recent events.
	existingCP, recent := extractCheckpointAndRecent(events)

	// Populate LLM fields from compaction checkpoint.
	if existingCP.Objective != "" {
		cp.LLM.Objective = existingCP.Objective
	}
	cp.LLM.DurableConstraints = cloneStrings(existingCP.UserConstraints)
	cp.LLM.CurrentStatus = cloneStrings(existingCP.CurrentProgress)
	cp.LLM.Decisions = cloneStrings(existingCP.DurableDecisions)
	cp.LLM.OpenTasks = cloneStrings(existingCP.NextActions)
	cp.LLM.RisksOrUnknowns = cloneStrings(existingCP.OpenQuestionsAndRisks)

	// Extract recent user requests.
	cp.LLM.RecentUserRequests = extractUserRequests(recent, 5)

	// Extract file changes as neutral descriptions.
	cp.LLM.ArtifactsChanged = extractNeutralFileChanges(recent)

	cp.ComputeHash()
	return cp
}

// ────────────────────────────────────────────────────────────────────────────
// Compaction checkpoint extraction (reused from kernel's compaction model).
// ────────────────────────────────────────────────────────────────────────────

// compactionCheckpoint is a local representation of the fields we extract from
// kernel compaction events. We do NOT import kernel/compaction directly to
// maintain the boundary; instead we parse the JSON ourselves.
type compactionCheckpoint struct {
	Objective             string   `json:"objective,omitempty"`
	UserConstraints       []string `json:"user_constraints,omitempty"`
	DurableDecisions      []string `json:"durable_decisions,omitempty"`
	VerifiedFacts         []string `json:"verified_facts,omitempty"`
	CurrentProgress       []string `json:"current_progress,omitempty"`
	OpenQuestionsAndRisks []string `json:"open_questions_and_risks,omitempty"`
	NextActions           []string `json:"next_actions,omitempty"`
}

func (c compactionCheckpoint) hasContent() bool {
	return c.Objective != "" ||
		len(c.UserConstraints) > 0 ||
		len(c.DurableDecisions) > 0 ||
		len(c.VerifiedFacts) > 0 ||
		len(c.CurrentProgress) > 0 ||
		len(c.OpenQuestionsAndRisks) > 0 ||
		len(c.NextActions) > 0
}

func extractCheckpointAndRecent(events []*session.Event) (compactionCheckpoint, []*session.Event) {
	var cp compactionCheckpoint
	recent := make([]*session.Event, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if isCompactionEvent(ev) {
			if parsed, ok := parseCompactionCheckpoint(ev); ok {
				cp = mergeCompactionCheckpoints(cp, parsed)
				recent = recent[:0]
			}
			continue
		}
		if !isCanonicalHistoryEvent(ev) {
			continue
		}
		recent = append(recent, ev)
	}
	return cp, recent
}

func isCompactionEvent(ev *session.Event) bool {
	if ev == nil {
		return false
	}
	return session.EventTypeOf(ev) == session.EventTypeCompaction
}

func isCanonicalHistoryEvent(ev *session.Event) bool {
	return session.IsCanonicalHistoryEvent(ev)
}

func parseCompactionCheckpoint(ev *session.Event) (compactionCheckpoint, bool) {
	if ev == nil {
		return compactionCheckpoint{}, false
	}
	text := ev.Message.TextContent()
	if text == "" {
		return compactionCheckpoint{}, false
	}
	var cp compactionCheckpoint
	if err := json.Unmarshal([]byte(text), &cp); err == nil && cp.hasContent() {
		return cp, true
	}
	// Try parsing from markdown sections (the kernel stores checkpoints as markdown).
	return parseCheckpointMarkdown(text), true
}

func parseCheckpointMarkdown(text string) compactionCheckpoint {
	var cp compactionCheckpoint
	var currentSection string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			currentSection = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")))
			continue
		}
		item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		if item == "" || item == trimmed && !strings.HasPrefix(trimmed, "- ") {
			// Not a list item; could be scalar content under a section.
			if trimmed != "" && currentSection == "active objective" {
				if cp.Objective != "" {
					cp.Objective += " " + trimmed
				} else {
					cp.Objective = trimmed
				}
			}
			continue
		}
		switch currentSection {
		case "active objective":
			if cp.Objective == "" {
				cp.Objective = item
			}
		case "durable constraints":
			cp.UserConstraints = append(cp.UserConstraints, item)
		case "durable decisions":
			cp.DurableDecisions = append(cp.DurableDecisions, item)
		case "verified facts and references":
			cp.VerifiedFacts = append(cp.VerifiedFacts, item)
		case "current progress":
			cp.CurrentProgress = append(cp.CurrentProgress, item)
		case "open questions and risks":
			cp.OpenQuestionsAndRisks = append(cp.OpenQuestionsAndRisks, item)
		case "immediate next actions":
			cp.NextActions = append(cp.NextActions, item)
		}
	}
	return cp
}

func mergeCompactionCheckpoints(base, update compactionCheckpoint) compactionCheckpoint {
	if update.Objective != "" {
		base.Objective = update.Objective
	}
	base.UserConstraints = mergeStringLists(base.UserConstraints, update.UserConstraints)
	base.DurableDecisions = mergeStringLists(base.DurableDecisions, update.DurableDecisions)
	base.VerifiedFacts = mergeStringLists(base.VerifiedFacts, update.VerifiedFacts)
	base.CurrentProgress = mergeStringLists(base.CurrentProgress, update.CurrentProgress)
	base.OpenQuestionsAndRisks = mergeStringLists(base.OpenQuestionsAndRisks, update.OpenQuestionsAndRisks)
	base.NextActions = mergeStringLists(base.NextActions, update.NextActions)
	return base
}

// ────────────────────────────────────────────────────────────────────────────
// Event inspection helpers — neutral, no ACP/kernel dependency.
// ────────────────────────────────────────────────────────────────────────────

func extractUserRequests(events []*session.Event, limit int) []string {
	var requests []string
	for _, ev := range events {
		if ev == nil || ev.Message.Role != model.RoleUser {
			continue
		}
		text := strings.TrimSpace(sessionmode.VisibleText(ev.Message.TextContent()))
		if text == "" {
			continue
		}
		requests = append(requests, text)
	}
	if len(requests) > limit {
		requests = requests[len(requests)-limit:]
	}
	return requests
}

func extractNeutralFileChanges(events []*session.Event) []string {
	seen := map[string]bool{}
	var changes []string
	for _, ev := range events {
		if ev == nil {
			continue
		}
		for _, call := range ev.Message.ToolCalls() {
			path := extractToolFilePath(call)
			if path == "" {
				continue
			}
			desc := neutralToolDescription(call.Name, path)
			if desc == "" || seen[desc] {
				continue
			}
			seen[desc] = true
			changes = append(changes, desc)
		}
	}
	return changes
}

func extractToolFilePath(call model.ToolCall) string {
	if call.Args == "" {
		return ""
	}
	var args map[string]any
	if json.Unmarshal([]byte(call.Args), &args) != nil {
		return ""
	}
	for _, key := range []string{"path", "file", "file_path", "filePath", "filename"} {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// neutralToolDescription produces a controller-neutral description of a file
// operation, stripping any ACP-private tool semantics.
func neutralToolDescription(toolName string, filePath string) string {
	toolName = strings.TrimSpace(toolName)
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return ""
	}
	lower := strings.ToLower(toolName)
	switch {
	case strings.Contains(lower, "write") || strings.Contains(lower, "create") || strings.Contains(lower, "edit"):
		return fmt.Sprintf("Modified: %s", filePath)
	case strings.Contains(lower, "read") || strings.Contains(lower, "view"):
		return fmt.Sprintf("Read: %s", filePath)
	case strings.Contains(lower, "delete") || strings.Contains(lower, "remove"):
		return fmt.Sprintf("Deleted: %s", filePath)
	default:
		return fmt.Sprintf("Touched: %s", filePath)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Utility helpers
// ────────────────────────────────────────────────────────────────────────────

func firstNonNilEvent(events []*session.Event) *session.Event {
	for _, ev := range events {
		if ev != nil {
			return ev
		}
	}
	return nil
}

func lastNonNilEvent(events []*session.Event) *session.Event {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i] != nil {
			return events[i]
		}
	}
	return nil
}

func eventIDOrTime(ev *session.Event) string {
	if ev == nil {
		return ""
	}
	if ev.ID != "" {
		return ev.ID
	}
	return ev.Time.Format(time.RFC3339Nano)
}

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

func mergeStringLists(lists ...[]string) []string {
	seen := map[string]bool{}
	var merged []string
	for _, list := range lists {
		for _, item := range list {
			if !seen[item] {
				seen[item] = true
				merged = append(merged, item)
			}
		}
	}
	return merged
}

var checkpointCounter int64

func defaultCheckpointID() string {
	checkpointCounter++
	return fmt.Sprintf("ckpt-%d-%d", time.Now().UnixMilli(), checkpointCounter)
}
