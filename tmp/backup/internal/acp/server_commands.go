package acp

import "github.com/OnslaughtSnail/caelis/internal/slashcmd"

var defaultACPCommands = slashcmd.New(
	slashcmd.Definition{
		Name:        "help",
		Description: "Show the slash commands available in this ACP session.",
		InputHint:   "/help",
	},
	slashcmd.Definition{
		Name:        "status",
		Description: "Summarize the current ACP session state, model, and mode.",
		InputHint:   "/status",
	},
	slashcmd.Definition{
		Name:        "compact",
		Description: "Manually compact session history. Optionally include a short note.",
		InputHint:   "/compact [note]",
	},
)

func DefaultAvailableCommands() []AvailableCommand {
	defs := defaultACPCommands.Definitions()
	out := make([]AvailableCommand, 0, len(defs))
	for _, item := range defs {
		out = append(out, AvailableCommand{
			Name:        item.Name,
			Description: item.Description,
			Input:       AvailableCommandInput{Hint: item.InputHint},
		})
	}
	return out
}
