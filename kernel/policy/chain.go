package policy

import "context"

func ApplyBeforeModel(ctx context.Context, hooks []Hook, in ModelInput) (ModelInput, error) {
	out := in
	for _, h := range hooks {
		if h == nil {
			continue
		}
		next, err := h.BeforeModel(ctx, out)
		if err != nil {
			return ModelInput{}, err
		}
		out = next
	}
	return out, nil
}

func ApplyBeforeTool(ctx context.Context, hooks []Hook, in ToolInput) (ToolInput, error) {
	out := in
	for _, h := range hooks {
		if h == nil {
			continue
		}
		prev := out.Decision
		next, err := h.BeforeTool(ctx, out)
		if err != nil {
			return ToolInput{}, err
		}
		// Enforce "most restrictive wins": a hook cannot relax a prior
		// deny or require_approval decision.
		next.Decision = mostRestrictiveDecision(prev, next.Decision)
		out = next
	}
	return out, nil
}

// decisionStrictness returns a numeric strictness level for a decision effect.
// Higher values are more restrictive.
func decisionStrictness(effect DecisionEffect) int {
	switch effect {
	case DecisionEffectDeny:
		return 2
	case DecisionEffectRequireApproval:
		return 1
	default:
		return 0
	}
}

// mostRestrictiveDecision returns the more restrictive of two decisions. If a
// prior decision was deny or require_approval, a later hook cannot relax it.
func mostRestrictiveDecision(prev, next Decision) Decision {
	if decisionStrictness(prev.Effect) > decisionStrictness(next.Effect) {
		return prev
	}
	return next
}

func ApplyAfterTool(ctx context.Context, hooks []Hook, out ToolOutput) (ToolOutput, error) {
	cur := out
	for _, h := range hooks {
		if h == nil {
			continue
		}
		next, err := h.AfterTool(ctx, cur)
		if err != nil {
			return ToolOutput{}, err
		}
		cur = next
	}
	return cur, nil
}

func ApplyBeforeOutput(ctx context.Context, hooks []Hook, out Output) (Output, error) {
	cur := out
	for _, h := range hooks {
		if h == nil {
			continue
		}
		next, err := h.BeforeOutput(ctx, cur)
		if err != nil {
			return Output{}, err
		}
		cur = next
	}
	return cur, nil
}
