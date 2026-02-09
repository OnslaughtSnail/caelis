package runtime

import (
	"context"
	"fmt"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

// UsageRequest defines one context usage inspection request.
type UsageRequest struct {
	AppName             string
	UserID              string
	SessionID           string
	Model               model.LLM
	ContextWindowTokens int
}

// ContextUsage is the estimated token usage snapshot for current session window.
type ContextUsage struct {
	CurrentTokens int
	WindowTokens  int
	InputBudget   int
	Ratio         float64
	EventCount    int
}

// ContextUsage returns current session context usage estimation.
func (r *Runtime) ContextUsage(ctx context.Context, req UsageRequest) (ContextUsage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if req.AppName == "" || req.UserID == "" || req.SessionID == "" {
		return ContextUsage{}, fmt.Errorf("runtime: app_name, user_id and session_id are required")
	}
	sess, err := r.store.GetOrCreate(ctx, &session.Session{AppName: req.AppName, UserID: req.UserID, ID: req.SessionID})
	if err != nil {
		return ContextUsage{}, err
	}
	events, err := r.store.ListEvents(ctx, sess)
	if err != nil {
		return ContextUsage{}, err
	}
	window := contextWindowEvents(events)
	windowTokens := resolveContextWindowTokens(req.ContextWindowTokens, req.Model, r.compaction.DefaultContextWindowTokens)
	inputBudget := windowTokens - r.compaction.ReserveOutputTokens - r.compaction.SafetyMarginTokens
	if inputBudget < 1 {
		inputBudget = windowTokens
	}
	if inputBudget < 1 {
		inputBudget = 1
	}
	current := estimateEventsTokens(window)
	ratio := float64(current) / float64(inputBudget)
	if ratio < 0 {
		ratio = 0
	}
	return ContextUsage{
		CurrentTokens: current,
		WindowTokens:  windowTokens,
		InputBudget:   inputBudget,
		Ratio:         ratio,
		EventCount:    len(window),
	}, nil
}
