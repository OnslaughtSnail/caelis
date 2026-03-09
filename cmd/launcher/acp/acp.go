package acp

import (
	"context"
	"fmt"

	"github.com/OnslaughtSnail/caelis/cmd/launcher"
)

type acpLauncher struct {
	run  launcher.RunWithArgs
	args []string
}

func NewLauncher(run launcher.RunWithArgs) launcher.SubLauncher {
	return &acpLauncher{run: run}
}

func (l *acpLauncher) Keyword() string {
	return "acp"
}

func (l *acpLauncher) Parse(args []string) ([]string, error) {
	l.args = append([]string(nil), args...)
	return nil, nil
}

func (l *acpLauncher) CommandLineSyntax() string {
	return "  acp [shared CLI flags]\n  Example: acp -model deepseek/deepseek-chat"
}

func (l *acpLauncher) SimpleDescription() string {
	return "start ACP stdio frontend"
}

func (l *acpLauncher) Run(ctx context.Context) error {
	if l.run == nil {
		return fmt.Errorf("launcher(acp): run function is nil")
	}
	return l.run(ctx, l.args)
}
