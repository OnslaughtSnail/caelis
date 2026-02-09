package api

import (
	"context"
	"fmt"

	"github.com/OnslaughtSnail/caelis/cmd/launcher"
)

type apiLauncher struct {
	run  launcher.RunWithArgs
	args []string
}

func NewLauncher(run launcher.RunWithArgs) launcher.SubLauncher {
	return &apiLauncher{run: run}
}

func (l *apiLauncher) Keyword() string {
	return "api"
}

func (l *apiLauncher) Parse(args []string) ([]string, error) {
	l.args = append([]string(nil), args...)
	return nil, nil
}

func (l *apiLauncher) CommandLineSyntax() string {
	return "  api [flags] (reserved for HTTP API service launcher)"
}

func (l *apiLauncher) SimpleDescription() string {
	return "start API frontend (placeholder)"
}

func (l *apiLauncher) Run(ctx context.Context) error {
	if l.run == nil {
		return fmt.Errorf("launcher(api): not implemented")
	}
	return l.run(ctx, l.args)
}
