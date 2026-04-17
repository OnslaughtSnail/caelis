package epochhandoff

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	coreacpmeta "github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

// ────────────────────────────────────────────────────────────────────────────
// Test: each epoch produces at most one canonical checkpoint
// ────────────────────────────────────────────────────────────────────────────

func TestBuildCheckpoint_OnlyOnePerEpoch(t *testing.T) {
	epoch := coreacpmeta.ControllerEpoch{
		EpochID:        "1",
		ControllerKind: coreacpmeta.ControllerKindSelf,
	}
	events := []*session.Event{
		{ID: "ev-1", Time: time.Now(), Message: model.NewTextMessage(model.RoleUser, "Hello")},
		{ID: "ev-2", Time: time.Now(), Message: model.NewTextMessage(model.RoleAssistant, "Hi there")},
	}
	cp1 := BuildCheckpoint(events, epoch, func() string { return "ckpt-1" })
	cp2 := BuildCheckpoint(events, epoch, func() string { return "ckpt-2" })

	// Both produce valid checkpoints but with different IDs — the coordinator
	// ensures only the first one is persisted (idempotent).
	if cp1.System.EpochID != "1" || cp2.System.EpochID != "1" {
		t.Fatalf("epoch ID mismatch: %q / %q", cp1.System.EpochID, cp2.System.EpochID)
	}
	if cp1.System.CheckpointID == cp2.System.CheckpointID {
		t.Fatal("two separate builds should have distinct checkpoint IDs")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test: summarize process not persisted, only checkpoint result
// ────────────────────────────────────────────────────────────────────────────

func TestBuildCheckpoint_IsEphemeral(t *testing.T) {
	// BuildCheckpoint returns an EpochCheckpoint in memory.
	// It does NOT interact with any store — the coordinator calls
	// PersistCheckpoint separately. This test verifies BuildCheckpoint
	// doesn't require or produce side effects.
	cp := BuildCheckpoint(nil, coreacpmeta.ControllerEpoch{EpochID: "1"}, nil)
	if cp.System.EpochID != "1" {
		t.Fatalf("expected epoch 1, got %q", cp.System.EpochID)
	}
	if cp.System.CreatedBy != "rule" {
		t.Fatalf("expected created_by=rule, got %q", cp.System.CreatedBy)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test: system_fields do not enter LLM injection content
// ────────────────────────────────────────────────────────────────────────────

func TestSystemFieldsNotInLLMInjection(t *testing.T) {
	cp := EpochCheckpoint{
		System: SystemFields{
			CheckpointID:   "ckpt-test-123",
			EpochID:        "42",
			ControllerKind: "acp",
			ControllerID:   "copilot",
			Hash:           "deadbeef",
		},
		LLM: LLMFields{
			Objective:     "Implement feature X",
			CurrentStatus: []string{"Step 1 done"},
			OpenTasks:     []string{"Step 2"},
		},
	}

	view := RenderLLMView(cp.LLM)

	// system_fields must NOT appear in the rendered LLM view.
	for _, forbidden := range []string{
		"ckpt-test-123",
		"deadbeef",
		"controller_kind",
		"controller_id",
		"checkpoint_id",
	} {
		if strings.Contains(view, forbidden) {
			t.Errorf("system field %q leaked into LLM view: %s", forbidden, view)
		}
	}

	// LLM fields MUST appear.
	if !strings.Contains(view, "Implement feature X") {
		t.Error("objective missing from LLM view")
	}
	if !strings.Contains(view, "Step 1 done") {
		t.Error("current status missing from LLM view")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test: llm_fields correctly render as synthetic user message
// ────────────────────────────────────────────────────────────────────────────

func TestRenderLLMView_SyntheticUserMessage(t *testing.T) {
	fields := LLMFields{
		Objective:          "Build search API",
		DurableConstraints: []string{"Use Go", "No external deps"},
		CurrentStatus:      []string{"Search index created"},
		CompletedWork:      []string{"Index schema designed"},
		ArtifactsChanged:   []string{"Modified: search/index.go"},
		Decisions:          []string{"Use BM25 ranking"},
		OpenTasks:          []string{"Add pagination"},
		RisksOrUnknowns:    []string{"Performance on large datasets"},
		RecentUserRequests: []string{"Add search API"},
		HandoffNotes:       "Watch for memory usage",
	}

	text := RenderLLMView(fields)
	if text == "" {
		t.Fatal("expected non-empty rendered text")
	}

	// All LLM fields should appear.
	for _, expected := range []string{
		"Build search API",
		"Use Go",
		"No external deps",
		"Search index created",
		"Index schema designed",
		"Modified: search/index.go",
		"Use BM25 ranking",
		"Add pagination",
		"Performance on large datasets",
		"Add search API",
		"Watch for memory usage",
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("missing expected content: %q", expected)
		}
	}
}

func TestRenderLLMView_EmptyFieldsReturnsEmpty(t *testing.T) {
	if text := RenderLLMView(LLMFields{}); text != "" {
		t.Errorf("expected empty, got %q", text)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test: same controller same remote session → incremental handoff
// ────────────────────────────────────────────────────────────────────────────

func TestComputeIncrementalRange_SameSession(t *testing.T) {
	events := []*session.Event{
		{ID: "ev-1"},
		{ID: "ev-2"},
		{ID: "ev-3"},
		{ID: "ev-4"},
	}
	syncState := coreacpmeta.RemoteSyncState{
		LastHandoffEventID: "ev-2",
	}
	mode, start := ComputeIncrementalRange(events, syncState)
	if mode != HandoffBundleModeIncremental {
		t.Fatalf("expected incremental, got %s", mode)
	}
	if start != 2 {
		t.Fatalf("expected start=2, got %d", start)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test: new controller / new remote session → full handoff
// ────────────────────────────────────────────────────────────────────────────

func TestComputeIncrementalRange_NewSession(t *testing.T) {
	events := []*session.Event{
		{ID: "ev-1"},
		{ID: "ev-2"},
	}
	// Empty sync state = new controller.
	mode, start := ComputeIncrementalRange(events, coreacpmeta.RemoteSyncState{})
	if mode != HandoffBundleModeFull {
		t.Fatalf("expected full, got %s", mode)
	}
	if start != 0 {
		t.Fatalf("expected start=0, got %d", start)
	}
}

func TestComputeIncrementalRange_WaterlineNotFound(t *testing.T) {
	events := []*session.Event{
		{ID: "ev-1"},
		{ID: "ev-2"},
	}
	syncState := coreacpmeta.RemoteSyncState{
		LastHandoffEventID: "ev-missing",
	}
	mode, _ := ComputeIncrementalRange(events, syncState)
	if mode != HandoffBundleModeFull {
		t.Fatalf("expected full fallback, got %s", mode)
	}
}

func TestComputeIncrementalRange_NoNewEvents(t *testing.T) {
	events := []*session.Event{
		{ID: "ev-1"},
		{ID: "ev-2"},
	}
	syncState := coreacpmeta.RemoteSyncState{
		LastHandoffEventID: "ev-2",
	}
	mode, start := ComputeIncrementalRange(events, syncState)
	if mode != HandoffBundleModeIncremental {
		t.Fatalf("expected incremental, got %s", mode)
	}
	if start != len(events) {
		t.Fatalf("expected start=%d (no new events), got %d", len(events), start)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test: ACP→self: self context doesn't contain ACP private tool semantics
// ────────────────────────────────────────────────────────────────────────────

func TestBuildCheckpoint_ACPToSelf_NeutralToolDescriptions(t *testing.T) {
	// Simulate ACP tool calls with provider-specific tool names.
	events := []*session.Event{
		{
			ID:   "ev-1",
			Time: time.Now(),
			Message: model.Message{
				Role: model.RoleAssistant,
			},
		},
	}

	// Create an event with ACP-specific tool calls.
	toolCallJSON := `{"path": "/src/main.go"}`
	acpEvent := &session.Event{
		ID:   "ev-2",
		Time: time.Now(),
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{
			{ID: "tc-1", Name: "copilot_edit_file", Args: toolCallJSON},
		}, ""),
	}
	events = append(events, acpEvent)

	epoch := coreacpmeta.ControllerEpoch{
		EpochID:        "2",
		ControllerKind: coreacpmeta.ControllerKindACP,
		ControllerID:   "copilot",
	}

	cp := BuildCheckpoint(events, epoch, nil)

	// The ACP-private tool name "copilot_edit_file" should NOT appear in
	// any LLM field — only neutral descriptions like "Modified: /src/main.go".
	llmJSON, _ := json.Marshal(cp.LLM)
	llmText := string(llmJSON)

	if strings.Contains(llmText, "copilot_edit_file") {
		t.Errorf("ACP private tool name leaked into LLM fields: %s", llmText)
	}
	if strings.Contains(llmText, "copilot") {
		t.Errorf("ACP controller ID leaked into LLM fields: %s", llmText)
	}

	// But the file change should be captured neutrally.
	found := false
	for _, item := range cp.LLM.ArtifactsChanged {
		if strings.Contains(item, "/src/main.go") {
			found = true
			break
		}
	}
	if !found {
		t.Error("neutral file change description not found in ArtifactsChanged")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test: HandoffBundle.RenderLLMView includes handoff header
// ────────────────────────────────────────────────────────────────────────────

func TestHandoffBundle_RenderLLMView_FullMode(t *testing.T) {
	bundle := HandoffBundle{
		Mode: HandoffBundleModeFull,
		Checkpoints: []EpochCheckpoint{
			{
				LLM: LLMFields{
					Objective:     "Test objective",
					CurrentStatus: []string{"In progress"},
				},
			},
		},
	}
	view := bundle.RenderLLMView()
	if !strings.Contains(view, "[System-generated handoff checkpoint]") {
		t.Error("full handoff header missing")
	}
	if !strings.Contains(view, "Test objective") {
		t.Error("objective missing from full handoff view")
	}
	if !strings.Contains(view, "not as a new user request") {
		t.Error("trust framing missing from full handoff view")
	}
}

func TestHandoffBundle_RenderLLMView_IncrementalMode(t *testing.T) {
	bundle := HandoffBundle{
		Mode: HandoffBundleModeIncremental,
		Checkpoints: []EpochCheckpoint{
			{
				LLM: LLMFields{
					CurrentStatus: []string{"Step 3 done"},
				},
			},
		},
	}
	view := bundle.RenderLLMView()
	if !strings.Contains(view, "[System-generated incremental handoff checkpoint]") {
		t.Error("incremental handoff header missing")
	}
	if !strings.Contains(view, "Merge this") {
		t.Error("merge instruction missing from incremental handoff view")
	}
}

func TestHandoffBundle_RenderLLMView_Empty(t *testing.T) {
	bundle := HandoffBundle{Mode: HandoffBundleModeFull}
	if view := bundle.RenderLLMView(); view != "" {
		t.Errorf("expected empty, got %q", view)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test: resume only depends on local checkpoint/session
// ────────────────────────────────────────────────────────────────────────────

func TestCheckpointRoundTrip_JSON(t *testing.T) {
	// Verify checkpoint can be serialized, stored as event text, and recovered.
	cp := EpochCheckpoint{
		System: SystemFields{
			CheckpointID:   "ckpt-42",
			EpochID:        "7",
			ControllerKind: "self",
			SchemaVersion:  SchemaVersion,
		},
		LLM: LLMFields{
			Objective: "Complete refactoring",
			OpenTasks: []string{"Update tests"},
		},
	}
	cp.ComputeHash()

	raw, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate storing as event and parsing back.
	ev := &session.Event{
		ID:   "sys-1",
		Time: time.Now(),
		Meta: map[string]any{
			"event_type":      checkpointEventType,
			checkpointMetaKey: true,
			"epoch_id":        "7",
		},
		Message: model.NewTextMessage(model.RoleSystem, string(raw)),
	}

	recovered, ok := parseCheckpointEvent(ev)
	if !ok {
		t.Fatal("failed to parse checkpoint event")
	}
	if recovered.System.CheckpointID != "ckpt-42" {
		t.Errorf("checkpoint ID mismatch: %q", recovered.System.CheckpointID)
	}
	if recovered.LLM.Objective != "Complete refactoring" {
		t.Errorf("objective mismatch: %q", recovered.LLM.Objective)
	}
	if recovered.System.Hash != cp.System.Hash {
		t.Errorf("hash mismatch: %q vs %q", recovered.System.Hash, cp.System.Hash)
	}
}

func TestPersistCheckpoint_MarksMirrorArtifact(t *testing.T) {
	t.Parallel()

	store := inmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "sess-1"}
	if _, err := store.GetOrCreate(t.Context(), sess); err != nil {
		t.Fatal(err)
	}

	coordinator := NewHandoffCoordinator(store)
	cp := EpochCheckpoint{
		System: SystemFields{
			CheckpointID:  "ckpt-1",
			EpochID:       "1",
			CreatedAt:     time.Now(),
			CreatedBy:     "rule",
			SchemaVersion: SchemaVersion,
		},
		LLM: LLMFields{Objective: "handoff"},
	}
	cp.ComputeHash()
	if err := coordinator.PersistCheckpoint(t.Context(), sess, cp); err != nil {
		t.Fatal(err)
	}

	events, err := store.ListEvents(t.Context(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 checkpoint event, got %d", len(events))
	}
	if !session.IsMirror(events[0]) {
		t.Fatalf("expected persisted checkpoint to be a mirror event, got %+v", events[0].Meta)
	}
	if session.IsCanonicalHistoryEvent(events[0]) {
		t.Fatal("checkpoint artifact must not re-enter canonical history")
	}
	if got, _ := events[0].Meta["event_type"].(string); got != checkpointEventType {
		t.Fatalf("expected persisted checkpoint event_type %q, got %q", checkpointEventType, got)
	}

	checkpoints, err := coordinator.LoadCheckpointState(t.Context(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 1 || checkpoints[0].System.CheckpointID != "ckpt-1" {
		t.Fatalf("expected persisted checkpoint to round-trip, got %+v", checkpoints)
	}
}

func TestBuildCheckpoint_IgnoresPersistedCheckpointArtifacts(t *testing.T) {
	t.Parallel()

	checkpointJSON := `{"system":{"checkpoint_id":"old","epoch_id":"1"},"llm":{"objective":"stale"}}`
	events := []*session.Event{
		session.MarkMirror(&session.Event{
			ID:   "ckpt-ev",
			Time: time.Now(),
			Meta: map[string]any{
				"event_type":      checkpointEventType,
				checkpointMetaKey: true,
				"epoch_id":        "1",
			},
			Message: model.NewTextMessage(model.RoleSystem, checkpointJSON),
		}),
		{
			ID:      "user-1",
			Time:    time.Now(),
			Message: model.NewTextMessage(model.RoleUser, "继续修复搜索功能"),
		},
	}

	cp := BuildCheckpoint(events, coreacpmeta.ControllerEpoch{
		EpochID:        "2",
		ControllerKind: coreacpmeta.ControllerKindSelf,
		ControllerID:   "self",
	}, nil)

	if cp.LLM.Objective == "stale" {
		t.Fatal("persisted checkpoint artifact leaked back into new checkpoint objective")
	}
	if len(cp.LLM.RecentUserRequests) != 1 || cp.LLM.RecentUserRequests[0] != "继续修复搜索功能" {
		t.Fatalf("unexpected recent user requests: %v", cp.LLM.RecentUserRequests)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test: BuildCheckpoint extracts compaction checkpoint data
// ────────────────────────────────────────────────────────────────────────────

func TestBuildCheckpoint_ExtractsFromCompactionEvent(t *testing.T) {
	compactionText := `## Active Objective

Build a search API

## Durable Constraints

- Use Go
- No external deps

## Current Progress

- Schema designed`

	events := []*session.Event{
		{
			ID:      "ev-compact",
			Time:    time.Now(),
			Meta:    map[string]any{"event_type": "compaction"},
			Message: model.NewTextMessage(model.RoleAssistant, compactionText),
		},
		{
			ID:      "ev-user",
			Time:    time.Now(),
			Message: model.NewTextMessage(model.RoleUser, "Add pagination support"),
		},
	}

	epoch := coreacpmeta.ControllerEpoch{
		EpochID:        "3",
		ControllerKind: coreacpmeta.ControllerKindSelf,
	}

	cp := BuildCheckpoint(events, epoch, nil)
	if cp.LLM.Objective != "Build a search API" {
		t.Errorf("unexpected objective: %q", cp.LLM.Objective)
	}
	if len(cp.LLM.DurableConstraints) != 2 {
		t.Errorf("expected 2 constraints, got %d", len(cp.LLM.DurableConstraints))
	}
	if len(cp.LLM.RecentUserRequests) != 1 || cp.LLM.RecentUserRequests[0] != "Add pagination support" {
		t.Errorf("unexpected user requests: %v", cp.LLM.RecentUserRequests)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Test: SyntheticHandoffMessage
// ────────────────────────────────────────────────────────────────────────────

func TestSyntheticHandoffMessage_UsesUserRole(t *testing.T) {
	bundle := HandoffBundle{
		Mode: HandoffBundleModeFull,
		Checkpoints: []EpochCheckpoint{
			{LLM: LLMFields{Objective: "Test"}},
		},
	}
	msg := SyntheticHandoffMessage(bundle)
	if msg.Role != model.RoleUser {
		t.Errorf("expected user role, got %s", msg.Role)
	}
	if !strings.Contains(msg.TextContent(), "[System-generated handoff checkpoint]") {
		t.Error("handoff header missing from synthetic message")
	}
}

func TestSyntheticHandoffMessage_EmptyBundleReturnsEmptyMessage(t *testing.T) {
	msg := SyntheticHandoffMessage(HandoffBundle{})
	if msg.TextContent() != "" {
		t.Errorf("expected empty message, got %q", msg.TextContent())
	}
}
