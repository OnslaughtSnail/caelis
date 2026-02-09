package launcher

import "context"

// RunWithArgs is the reusable launcher execution function shape.
type RunWithArgs func(context.Context, []string) error

// Launcher routes args and starts a selected mode.
type Launcher interface {
	Execute(context.Context, []string) error
	CommandLineSyntax() string
}

// SubLauncher is one runnable mode (console/api/web...).
type SubLauncher interface {
	Keyword() string
	Parse([]string) ([]string, error)
	CommandLineSyntax() string
	SimpleDescription() string
	Run(context.Context) error
}
