package main

import (
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	toolfs "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
)

const richDiffMaxLines = 800

func buildToolCallDiffBlockMsg(runtime toolexec.Runtime, toolName string, callArgs map[string]any) (tuievents.DiffBlockMsg, bool, bool) {
	preview, err := toolfs.BuildMutationPreview(runtime, toolName, callArgs)
	if err != nil {
		return tuievents.DiffBlockMsg{}, false, false
	}
	counts := mutationChangeCountsForTool(strings.ToUpper(strings.TrimSpace(toolName)), preview, callArgs)
	if shouldSkipRichDiff(preview, counts) {
		return tuievents.DiffBlockMsg{}, true, true
	}
	msg := tuievents.DiffBlockMsg{
		Tool:      strings.ToUpper(strings.TrimSpace(preview.Tool)),
		Path:      displayFileName(preview.Path),
		Created:   preview.Created,
		Hunk:      preview.Hunk,
		Old:       preview.Old,
		New:       preview.New,
		Preview:   preview.Preview,
		Truncated: strings.Contains(preview.Preview, "... (preview truncated)"),
	}
	if msg.Tool == "" {
		msg.Tool = strings.ToUpper(strings.TrimSpace(toolName))
	}
	return msg, false, true
}
