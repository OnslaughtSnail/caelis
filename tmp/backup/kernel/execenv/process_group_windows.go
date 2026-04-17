//go:build windows

package execenv

import (
	"errors"
	"os/exec"
)

func setProcessGroup(cmd *exec.Cmd) {
	// No-op on Windows. We fall back to killing the direct process.
}

func killProcessGroup(pid int) error {
	return errors.New("process groups are not supported on windows")
}
