package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

const PlanToolName = "PLAN"

type PlanStatus string

const (
	PlanStatusPending    PlanStatus = "pending"
	PlanStatusInProgress PlanStatus = "in_progress"
	PlanStatusCompleted  PlanStatus = "completed"
)

type PlanEntry struct {
	Content string     `json:"content"`
	Status  PlanStatus `json:"status"`
}

type PlanArgs struct {
	Entries []PlanEntry `json:"entries"`
}

type PlanResult struct {
	Entries []PlanEntry `json:"entries"`
}

func NewPlanTool() (Tool, error) {
	return &planTool{}, nil
}

type planTool struct{}

func (t *planTool) Name() string {
	return PlanToolName
}

func (t *planTool) Description() string {
	return "Replace the current structured execution plan with the latest complete todo list. Use concise step text and statuses pending, in_progress, or completed."
}

func (t *planTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"entries": map[string]any{
					"type":        "array",
					"description": "The complete current plan. Replace the full list each time.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"content": map[string]any{
								"type":        "string",
								"description": "Short standalone step text.",
							},
							"status": map[string]any{
								"type":        "string",
								"description": "One of pending, in_progress, completed.",
								"enum":        []string{string(PlanStatusPending), string(PlanStatusInProgress), string(PlanStatusCompleted)},
							},
						},
						"required": []string{"content", "status"},
					},
				},
			},
			"required": []string{"entries"},
		},
	}
}

func (t *planTool) Capability() toolcap.Capability {
	return toolcap.Capability{Risk: toolcap.RiskLow}
}

func (t *planTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	typed, err := decodePlanArgs(args)
	if err != nil {
		return nil, err
	}
	stateCtx, ok := session.StateContextFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("tool: PLAN state context is unavailable")
	}
	entries, err := normalizePlanEntries(typed.Entries)
	if err != nil {
		return nil, err
	}
	if updater, ok := stateCtx.Store.(session.StateUpdateStore); ok {
		err = updater.UpdateState(ctx, stateCtx.Session, func(values map[string]any) (map[string]any, error) {
			if values == nil {
				values = map[string]any{}
			}
			values["plan"] = map[string]any{
				"version": 1,
				"entries": planEntriesToAny(entries),
			}
			return values, nil
		})
	} else {
		values, snapErr := stateCtx.Store.SnapshotState(ctx, stateCtx.Session)
		if snapErr != nil {
			return nil, snapErr
		}
		if values == nil {
			values = map[string]any{}
		}
		values["plan"] = map[string]any{
			"version": 1,
			"entries": planEntriesToAny(entries),
		}
		err = stateCtx.Store.ReplaceState(ctx, stateCtx.Session, values)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"entries": planEntriesToAny(entries),
	}, nil
}

func decodePlanArgs(args map[string]any) (PlanArgs, error) {
	var typed PlanArgs
	if err := convertViaJSON(args, &typed); err != nil {
		return PlanArgs{}, fmt.Errorf("tool: decode args for %q: %w", PlanToolName, err)
	}
	return typed, nil
}

func normalizePlanEntries(entries []PlanEntry) ([]PlanEntry, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]PlanEntry, 0, len(entries))
	inProgress := 0
	for _, item := range entries {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			return nil, fmt.Errorf("tool: %q entries.content is required", PlanToolName)
		}
		status := normalizePlanStatus(item.Status)
		if status == "" {
			return nil, fmt.Errorf("tool: %q entries.status must be pending, in_progress, or completed", PlanToolName)
		}
		if status == PlanStatusInProgress {
			inProgress++
		}
		out = append(out, PlanEntry{Content: content, Status: status})
	}
	if inProgress > 1 {
		return nil, fmt.Errorf("tool: %q allows at most one in_progress entry", PlanToolName)
	}
	return out, nil
}

func normalizePlanStatus(value PlanStatus) PlanStatus {
	switch strings.TrimSpace(string(value)) {
	case string(PlanStatusPending):
		return PlanStatusPending
	case string(PlanStatusInProgress):
		return PlanStatusInProgress
	case string(PlanStatusCompleted):
		return PlanStatusCompleted
	default:
		return ""
	}
}

func planEntriesToAny(entries []PlanEntry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, item := range entries {
		out = append(out, map[string]any{
			"content": item.Content,
			"status":  string(item.Status),
		})
	}
	return out
}
