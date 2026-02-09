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
		next, err := h.BeforeTool(ctx, out)
		if err != nil {
			return ToolInput{}, err
		}
		out = next
	}
	return out, nil
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
