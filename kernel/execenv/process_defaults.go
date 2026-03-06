package execenv

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func resolveHostWorkDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return os.Getwd()
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir), nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}
