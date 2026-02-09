package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

type workspaceContext struct {
	CWD string
	Key string
}

func resolveWorkspaceContext() (workspaceContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return workspaceContext{}, fmt.Errorf("cli: resolve cwd: %w", err)
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return workspaceContext{}, fmt.Errorf("cli: resolve absolute cwd: %w", err)
	}
	return workspaceContext{
		CWD: abs,
		Key: workspaceKey(abs),
	}, nil
}

func workspaceKey(path string) string {
	sum := sha1.Sum([]byte(filepath.Clean(path)))
	short := hex.EncodeToString(sum[:8])
	base := sanitizeAppName(filepath.Base(path))
	if base == "" {
		base = "workspace"
	}
	return base + "-" + short
}
