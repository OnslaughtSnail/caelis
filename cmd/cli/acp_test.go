package main

import (
	"testing"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	toolmcp "github.com/OnslaughtSnail/caelis/kernel/tool/mcptoolset"
)

func TestBuildACPMCPConfig_MergesBaseAndSessionServers(t *testing.T) {
	base := toolmcp.Config{
		CacheTTL: 123,
		Servers: []toolmcp.ServerConfig{{
			Name:      "base",
			Prefix:    "base",
			Transport: toolmcp.TransportStdio,
			Command:   "base-cmd",
		}},
	}

	cfg, err := buildACPMCPConfig(base, []internalacp.MCPServer{{
		Name:    "session",
		Type:    "stdio",
		Command: "session-cmd",
	}})
	if err != nil {
		t.Fatalf("buildACPMCPConfig: %v", err)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].Name != "base" {
		t.Fatalf("expected base server preserved first, got %+v", cfg.Servers)
	}
	if cfg.Servers[1].Name != "session" {
		t.Fatalf("expected session server appended, got %+v", cfg.Servers)
	}
}
