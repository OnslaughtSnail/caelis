package main

import (
	"context"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/OnslaughtSnail/caelis/app/gatewayapp"
	tuiadapterruntime "github.com/OnslaughtSnail/caelis/gateway/adapter/tui/runtime"
	tuiapp "github.com/OnslaughtSnail/caelis/tui/tuiapp"
)

func runTUI(ctx context.Context, stack *gatewayapp.Stack, sessionID string, modelText string, stdin io.Reader, stdout io.Writer) error {
	driver, err := tuiadapterruntime.NewGatewayDriver(ctx, stack, strings.TrimSpace(sessionID), "cli-tui", strings.TrimSpace(modelText))
	if err != nil {
		return err
	}
	sender := &tuiapp.ProgramSender{}
	cfg := tuiapp.ConfigFromDriver(driver, sender, tuiapp.Config{
		AppName:         "CAELIS",
		Version:         envOr("CAELIS_VERSION", "dev"),
		Workspace:       stack.Workspace.CWD,
		ModelAlias:      modelText,
		ShowWelcomeCard: true,
		Commands:        tuiapp.DefaultCommands(),
		Wizards:         tuiapp.DefaultWizards(),
		ModeLabel: func() string {
			status, err := driver.Status(context.Background())
			if err != nil {
				return ""
			}
			return strings.TrimSpace(status.ModeLabel)
		},
	})
	model := tuiapp.NewModel(cfg)
	program := tea.NewProgram(model, tea.WithInput(stdin), tea.WithOutput(stdout))
	sender.Send = program.Send
	_, err = program.Run()
	return err
}
