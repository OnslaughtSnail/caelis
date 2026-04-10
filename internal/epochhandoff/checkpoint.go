// Package epochhandoff implements the Epoch Handoff Layer — an independent
// orchestration layer that sits between the kernel (self) and ACP controllers.
// It owns epoch boundary detection, checkpoint generation, handoff bundle
// assembly, full/incremental handoff rules and remote sync waterline management.
//
// This package deliberately does NOT depend on kernel internals or ACP protocol
// details. It operates on session events and session state only.
package epochhandoff

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SchemaVersion is the current checkpoint schema version. Bump this when the
// layout of EpochCheckpoint changes in incompatible ways.
const SchemaVersion = 1

// CheckpointMode indicates how the checkpoint was computed.
type CheckpointMode string

const (
	CheckpointModeFull        CheckpointMode = "full"
	CheckpointModeIncremental CheckpointMode = "incremental"
)

// ────────────────────────────────────────────────────────────────────────────
// System fields — control/sync/replay only, NEVER exposed to LLM.
// ────────────────────────────────────────────────────────────────────────────

// SystemFields holds metadata used exclusively for system control, sync
// tracking, replay, and audit. These fields must NOT appear in any content
// sent to an LLM.
type SystemFields struct {
	CheckpointID           string         `json:"checkpoint_id"`
	EpochID                string         `json:"epoch_id"`
	ControllerKind         string         `json:"controller_kind"`
	ControllerID           string         `json:"controller_id"`
	SourceEventStart       string         `json:"source_event_start"`
	SourceEventEnd         string         `json:"source_event_end"`
	CreatedAt              time.Time      `json:"created_at"`
	CreatedBy              string         `json:"created_by"` // "rule" or "summarize_turn"
	Mode                   CheckpointMode `json:"mode"`
	WatermarkEventID       string         `json:"watermark_event_id"`
	SyncTargetControllerID string         `json:"sync_target_controller_id,omitempty"`
	Version                int            `json:"version"`
	SchemaVersion          int            `json:"schema_version"`
	Hash                   string         `json:"hash"`
}

// ────────────────────────────────────────────────────────────────────────────
// LLM fields — task handoff context only, injected into next controller.
// ────────────────────────────────────────────────────────────────────────────

// LLMFields holds the structured information that the next controller/LLM
// needs to resume work. It must contain ONLY task-relevant handoff data:
// no internal IDs, replay offsets, sync hashes, provider metadata, or
// ACP-private tool protocol details.
type LLMFields struct {
	Objective          string   `json:"objective,omitempty"`
	DurableConstraints []string `json:"durable_constraints,omitempty"`
	CurrentStatus      []string `json:"current_status,omitempty"`
	CompletedWork      []string `json:"completed_work,omitempty"`
	ArtifactsChanged   []string `json:"artifacts_changed,omitempty"`
	ImportantResults   []string `json:"important_results,omitempty"`
	Decisions          []string `json:"decisions,omitempty"`
	OpenTasks          []string `json:"open_tasks,omitempty"`
	RisksOrUnknowns    []string `json:"risks_or_unknowns,omitempty"`
	RecentUserRequests []string `json:"recent_user_requests,omitempty"`
	HandoffNotes       string   `json:"handoff_notes,omitempty"`
}

// HasContent reports whether the LLM fields carry any meaningful data.
func (f LLMFields) HasContent() bool {
	return f.Objective != "" ||
		len(f.DurableConstraints) > 0 ||
		len(f.CurrentStatus) > 0 ||
		len(f.CompletedWork) > 0 ||
		len(f.ArtifactsChanged) > 0 ||
		len(f.ImportantResults) > 0 ||
		len(f.Decisions) > 0 ||
		len(f.OpenTasks) > 0 ||
		len(f.RisksOrUnknowns) > 0 ||
		len(f.RecentUserRequests) > 0 ||
		f.HandoffNotes != ""
}

// ────────────────────────────────────────────────────────────────────────────
// EpochCheckpoint — the canonical checkpoint for one epoch.
// ────────────────────────────────────────────────────────────────────────────

// EpochCheckpoint is the single canonical checkpoint for one controller epoch.
// Each epoch may produce at most one canonical checkpoint.
type EpochCheckpoint struct {
	System SystemFields `json:"system"`
	LLM    LLMFields    `json:"llm"`
}

// ComputeHash computes a content hash over the LLM fields and stores it in
// System.Hash. Returns the computed hash.
func (cp *EpochCheckpoint) ComputeHash() string {
	raw, _ := json.Marshal(cp.LLM)
	sum := sha256.Sum256(raw)
	h := fmt.Sprintf("%x", sum[:12])
	cp.System.Hash = h
	return h
}

// IsEmpty reports whether the checkpoint carries no meaningful content.
func (cp EpochCheckpoint) IsEmpty() bool {
	return !cp.LLM.HasContent()
}

// MarshalJSON implements json.Marshaler.
func (cp EpochCheckpoint) MarshalJSON() ([]byte, error) {
	type alias EpochCheckpoint
	return json.Marshal(alias(cp))
}

// UnmarshalJSON implements json.Unmarshaler.
func (cp *EpochCheckpoint) UnmarshalJSON(data []byte) error {
	type alias EpochCheckpoint
	return json.Unmarshal(data, (*alias)(cp))
}

// ────────────────────────────────────────────────────────────────────────────
// Rendering — convert LLMFields into text for injection.
// ────────────────────────────────────────────────────────────────────────────

// RenderLLMView converts the LLM fields into a structured Markdown text block
// suitable for injection as a synthetic user message.
func RenderLLMView(fields LLMFields) string {
	if !fields.HasContent() {
		return ""
	}
	var b strings.Builder
	if fields.Objective != "" {
		fmt.Fprintf(&b, "## Active Objective\n\n%s\n\n", fields.Objective)
	}
	writeSection(&b, "## Durable Constraints", fields.DurableConstraints)
	writeSection(&b, "## Current Status", fields.CurrentStatus)
	writeSection(&b, "## Completed Work", fields.CompletedWork)
	writeSection(&b, "## Artifacts Changed", fields.ArtifactsChanged)
	writeSection(&b, "## Important Results", fields.ImportantResults)
	writeSection(&b, "## Decisions", fields.Decisions)
	writeSection(&b, "## Open Tasks", fields.OpenTasks)
	writeSection(&b, "## Risks or Unknowns", fields.RisksOrUnknowns)
	writeSection(&b, "## Recent User Requests", fields.RecentUserRequests)
	if fields.HandoffNotes != "" {
		fmt.Fprintf(&b, "## Handoff Notes\n\n%s\n\n", fields.HandoffNotes)
	}
	return strings.TrimSpace(b.String())
}

func writeSection(b *strings.Builder, heading string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "%s\n\n", heading)
	for _, item := range items {
		fmt.Fprintf(b, "- %s\n", item)
	}
	b.WriteString("\n")
}
