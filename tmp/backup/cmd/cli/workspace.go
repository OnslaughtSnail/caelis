package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func resolveWorkspaceRoot(cwd string, explicitRoot string) (string, error) {
	if strings.TrimSpace(explicitRoot) != "" {
		abs, err := filepath.Abs(explicitRoot)
		if err != nil {
			return "", fmt.Errorf("cli: resolve workspace root: %w", err)
		}
		return filepath.Clean(abs), nil
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", fmt.Errorf("cli: workspace cwd is required")
	}
	if root := findRepositoryRoot(cwd); root != "" {
		return root, nil
	}
	return filepath.Clean(cwd), nil
}

func findRepositoryRoot(start string) string {
	current := strings.TrimSpace(start)
	if current == "" {
		return ""
	}
	for {
		gitPath := filepath.Join(current, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return filepath.Clean(current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
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
