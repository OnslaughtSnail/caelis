package web

import (
	"context"
	"fmt"

	"github.com/OnslaughtSnail/caelis/cmd/launcher"
)

type webLauncher struct {
	run  launcher.RunWithArgs
	args []string
}

func NewLauncher(run launcher.RunWithArgs) launcher.SubLauncher {
	return &webLauncher{run: run}
}

func (l *webLauncher) Keyword() string {
	return "web"
}

func (l *webLauncher) Parse(args []string) ([]string, error) {
	l.args = append([]string(nil), args...)
	return nil, nil
}

func (l *webLauncher) CommandLineSyntax() string {
	return "  web [flags] (reserved for Web frontend launcher)"
}

func (l *webLauncher) SimpleDescription() string {
	return "start Web frontend (placeholder)"
}

func (l *webLauncher) Run(ctx context.Context) error {
	if l.run == nil {
		return fmt.Errorf("launcher(web): not implemented")
	}
	return l.run(ctx, l.args)
}
