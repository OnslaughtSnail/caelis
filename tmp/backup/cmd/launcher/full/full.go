package full

import (
	"github.com/OnslaughtSnail/caelis/cmd/launcher"
	launcheracp "github.com/OnslaughtSnail/caelis/cmd/launcher/acp"
	launcherconsole "github.com/OnslaughtSnail/caelis/cmd/launcher/console"
	"github.com/OnslaughtSnail/caelis/cmd/launcher/universal"
)

func NewLauncher(consoleRun, acpRun launcher.RunWithArgs) launcher.Launcher {
	return universal.NewLauncher(
		launcherconsole.NewLauncher(consoleRun),
		launcheracp.NewLauncher(acpRun),
	)
}
