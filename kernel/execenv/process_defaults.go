package execenv

import (
	"bytes"
	"os/exec"
)

var defaultCommandEnvVars = []string{
	"CI=1",
	"TERM=dumb",
	"GIT_TERMINAL_PROMPT=0",
	"GIT_ASKPASS=/bin/false",
	"SSH_ASKPASS=/bin/false",
	"SSH_ASKPASS_REQUIRE=never",
	"SUDO_ASKPASS=/bin/false",
	"PAGER=cat",
	"NO_COLOR=1",
}

func applyNonInteractiveCommandDefaults(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	setProcessGroup(cmd)
	cmd.Stdin = bytes.NewReader(nil)
}

func defaultDockerEnvArgs() []string {
	args := make([]string, 0, len(defaultCommandEnvVars)*2)
	for _, kv := range defaultCommandEnvVars {
		args = append(args, "-e", kv)
	}
	return args
}
