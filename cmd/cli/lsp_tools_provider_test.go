package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

func TestDetectPrimaryLanguage_PrefersRootMarker(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.py"), []byte("print('hi')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	specs := []lspLanguageSpec{
		{
			Language:    "go",
			Priority:    100,
			RootMarkers: []string{"go.mod"},
			Extensions:  []string{".go"},
		},
		{
			Language:    "python",
			Priority:    90,
			RootMarkers: []string{"pyproject.toml"},
			Extensions:  []string{".py"},
		},
	}
	if got := detectPrimaryLanguage(root, specs); got != "go" {
		t.Fatalf("expected go from root marker, got %q", got)
	}
}

func TestDetectPrimaryLanguage_RequiresWorkspaceEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.py"), []byte("print('hi')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	specs := []lspLanguageSpec{
		{
			Language:    "go",
			Priority:    100,
			RootMarkers: []string{"go.mod"},
			Extensions:  []string{".go"},
		},
		{
			Language:    "python",
			Priority:    90,
			RootMarkers: []string{"pyproject.toml"},
			Extensions:  []string{".py"},
		},
	}
	if got := detectPrimaryLanguage(root, specs); got != "python" {
		t.Fatalf("expected python from extension evidence, got %q", got)
	}
}

func TestResolveLSPServerCommand_PrefersWorkspaceNodeBin(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	command := "typescript-language-server"
	if runtime.GOOS == "windows" {
		command += ".cmd"
	}
	path := filepath.Join(binDir, command)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, args, ok := resolveLSPServerCommand(root, []lspServerCandidate{{
		Command: "typescript-language-server",
		Args:    []string{"--stdio"},
	}})
	if !ok {
		t.Fatal("expected local node_modules server to resolve")
	}
	if resolved != path {
		t.Fatalf("expected %q, got %q", path, resolved)
	}
	if len(args) != 1 || args[0] != "--stdio" {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestHasLSPTools(t *testing.T) {
	echoTool, err := tool.NewFunction[struct{}, struct{}]("echo", "echo", func(_ context.Context, _ struct{}) (struct{}, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	lspTool, err := tool.NewFunction[struct{}, struct{}]("LSP_DIAGNOSTICS", "lsp", func(_ context.Context, _ struct{}) (struct{}, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if hasLSPTools([]tool.Tool{echoTool}) {
		t.Fatal("did not expect non-LSP tool set to match")
	}
	if !hasLSPTools([]tool.Tool{echoTool, lspTool}) {
		t.Fatal("expected LSP-prefixed tool to match")
	}
}
