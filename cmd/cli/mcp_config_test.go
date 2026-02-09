package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMCPToolManager_NotFoundPath(t *testing.T) {
	manager, err := loadMCPToolManager(filepath.Join(t.TempDir(), "mcp_servers.json"))
	if err != nil {
		t.Fatalf("load manager: %v", err)
	}
	if manager != nil {
		t.Fatalf("expected nil manager")
	}
}

func TestLoadMCPToolManager_ParseCommunityFormat(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp.json")
	content := `{
  "cache_ttl_seconds": 60,
  "mcpServers": {
    "demo": {
      "transport": "streamable",
      "url": "http://127.0.0.1:8787/mcp",
      "include_tools": ["search"]
    }
  }
}`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	manager, err := loadMCPToolManager(cfgPath)
	if err != nil {
		t.Fatalf("load manager: %v", err)
	}
	if manager == nil {
		t.Fatalf("expected non-nil manager")
	}
	_ = manager.Close()
}

func TestLoadMCPToolManager_RejectLegacyFormat(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp.json")
	content := `{
  "servers": [
    {"name": "demo", "command": "npx"}
  ]
}`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	_, err := loadMCPToolManager(cfgPath)
	if err == nil {
		t.Fatalf("expected parse error")
	}
	if !strings.Contains(err.Error(), "legacy \"servers\" format is not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}
