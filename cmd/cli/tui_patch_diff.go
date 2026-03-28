package main

import (
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
)

const richDiffMaxLines = 800

func buildToolCallDiffBlockMsg(runtime toolexec.Runtime, toolName string, callArgs map[string]any) (tuievents.DiffBlockMsg, bool, bool) {
	render, err := buildMutationPreviewRender(runtime, toolName, callArgs)
	if err != nil {
		return tuievents.DiffBlockMsg{}, false, false
	}
	if !render.HasChanges {
		return tuievents.DiffBlockMsg{}, false, false
	}
	if render.TooLarge {
		return tuievents.DiffBlockMsg{}, true, true
	}
	return render.DiffMsg, false, true
}
