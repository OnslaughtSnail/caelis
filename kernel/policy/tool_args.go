package policy

func resolveToolInputArgs(in ToolInput) map[string]any {
	if len(in.Args) > 0 {
		return in.Args
	}
	return map[string]any{}
}
