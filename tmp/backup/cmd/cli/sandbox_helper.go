package main

import (
	"os"
	"path/filepath"
	stdruntime "runtime"

	"github.com/OnslaughtSnail/caelis/internal/sandboxhelper"
)

func resolveSandboxHelperPath() (string, error) {
	if stdruntime.GOOS != "linux" {
		return "", nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(filepath.Dir(exePath), sandboxhelper.BinaryName)
	info, err := os.Stat(candidate)
	if err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
		return candidate, nil
	}
	return exePath, nil
}
