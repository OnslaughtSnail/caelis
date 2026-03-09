package full

import (
	"github.com/OnslaughtSnail/caelis/cmd/launcher"
	launcheracp "github.com/OnslaughtSnail/caelis/cmd/launcher/acp"
	launcherapi "github.com/OnslaughtSnail/caelis/cmd/launcher/api"
	launcherconsole "github.com/OnslaughtSnail/caelis/cmd/launcher/console"
	"github.com/OnslaughtSnail/caelis/cmd/launcher/universal"
	launcherweb "github.com/OnslaughtSnail/caelis/cmd/launcher/web"
)

func NewLauncher(consoleRun, acpRun, apiRun, webRun launcher.RunWithArgs) launcher.Launcher {
	return universal.NewLauncher(
		launcherconsole.NewLauncher(consoleRun),
		launcheracp.NewLauncher(acpRun),
		launcherapi.NewLauncher(apiRun),
		launcherweb.NewLauncher(webRun),
	)
}
