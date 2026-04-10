package epochhandoff

import (
	"github.com/OnslaughtSnail/caelis/kernel/model"
)

// systemCheckpointMessage wraps checkpoint JSON in a system-role message for
// durable session storage. This is a system artifact, not an LLM-visible
// conversation message.
func systemCheckpointMessage(checkpointJSON string) model.Message {
	return model.NewTextMessage(model.RoleSystem, checkpointJSON)
}

// SyntheticHandoffMessage creates a synthetic user message containing the
// rendered LLM view of a handoff bundle. This is the ONLY approved path for
// injecting handoff context into a controller's input.
//
// The message:
//   - Uses model.RoleUser for protocol compatibility
//   - Clearly identifies itself as system-generated
//   - Contains ONLY llm_fields content (no system_fields)
func SyntheticHandoffMessage(bundle HandoffBundle) model.Message {
	view := bundle.RenderLLMView()
	if view == "" {
		return model.Message{}
	}
	return model.NewTextMessage(model.RoleUser, view)
}
