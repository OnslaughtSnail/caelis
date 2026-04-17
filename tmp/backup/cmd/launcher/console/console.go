package console

import (
	"context"
	"fmt"

	"github.com/OnslaughtSnail/caelis/cmd/launcher"
)

type consoleLauncher struct {
	run  launcher.RunWithArgs
	args []string
}

func NewLauncher(run launcher.RunWithArgs) launcher.SubLauncher {
	return &consoleLauncher{run: run}
}

func (l *consoleLauncher) Keyword() string {
	return "console"
}

func (l *consoleLauncher) Parse(args []string) ([]string, error) {
	l.args = append([]string(nil), args...)
	return nil, nil
}

func (l *consoleLauncher) CommandLineSyntax() string {
	return "  [console] [all existing CLI flags]\n  Example: console -model deepseek-chat -stream=true"
}

func (l *consoleLauncher) SimpleDescription() string {
	return "start interactive console frontend"
}

func (l *consoleLauncher) Run(ctx context.Context) error {
	if l.run == nil {
		return fmt.Errorf("launcher(console): run function is nil")
	}
	return l.run(ctx, l.args)
}
