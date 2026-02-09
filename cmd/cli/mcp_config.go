package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	toolmcp "github.com/OnslaughtSnail/caelis/kernel/tool/mcptoolset"
)

type mcpConfigFile struct {
	CacheTTLSeconds int                        `json:"cache_ttl_seconds,omitempty"`
	MCPServers      map[string]mcpServerRecord `json:"mcpServers"`
}

type mcpServerRecord struct {
	Prefix       string            `json:"prefix,omitempty"`
	Transport    string            `json:"transport,omitempty"`
	Command      string            `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	WorkDir      string            `json:"workdir,omitempty"`
	URL          string            `json:"url,omitempty"`
	IncludeTools []string          `json:"include_tools,omitempty"`
	CallTimeout  int               `json:"call_timeout_seconds,omitempty"`
}

const defaultMCPConfigLocation = "~/.agents/mcp_servers.json"

func loadMCPToolManager(path string) (*toolmcp.Manager, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultMCPConfigLocation
	}
	resolved, err := resolvePath(path)
	if err != nil {
		return nil, fmt.Errorf("mcp config: resolve path %q: %w", path, err)
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("mcp config: read %q: %w", resolved, err)
	}
	var cfgFile mcpConfigFile
	if err := json.Unmarshal(raw, &cfgFile); err != nil {
		return nil, fmt.Errorf("mcp config: parse %q: %w", resolved, err)
	}
	if len(cfgFile.MCPServers) == 0 {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(raw, &probe); err == nil {
			if _, ok := probe["servers"]; ok {
				return nil, fmt.Errorf("mcp config: parse %q: legacy \"servers\" format is not supported, use \"mcpServers\"", resolved)
			}
		}
		return nil, nil
	}
	names := make([]string, 0, len(cfgFile.MCPServers))
	for name := range cfgFile.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	cfg := toolmcp.Config{
		Servers: make([]toolmcp.ServerConfig, 0, len(cfgFile.MCPServers)),
	}
	if cfgFile.CacheTTLSeconds > 0 {
		cfg.CacheTTL = time.Duration(cfgFile.CacheTTLSeconds) * time.Second
	}
	for _, name := range names {
		server := cfgFile.MCPServers[name]
		transport := strings.TrimSpace(strings.ToLower(server.Transport))
		if transport == "" {
			switch {
			case strings.TrimSpace(server.Command) != "":
				transport = string(toolmcp.TransportStdio)
			case strings.TrimSpace(server.URL) != "":
				transport = string(toolmcp.TransportStreamable)
			}
		}
		item := toolmcp.ServerConfig{
			Name:         strings.TrimSpace(name),
			Prefix:       strings.TrimSpace(server.Prefix),
			Transport:    toolmcp.TransportType(transport),
			Command:      strings.TrimSpace(server.Command),
			Args:         append([]string(nil), server.Args...),
			Env:          copyStringMap(server.Env),
			WorkDir:      strings.TrimSpace(server.WorkDir),
			URL:          strings.TrimSpace(server.URL),
			IncludeTools: append([]string(nil), server.IncludeTools...),
		}
		if item.Name == "" {
			return nil, fmt.Errorf("mcp config: mcpServers has empty key name")
		}
		if item.WorkDir != "" {
			workDir, err := resolvePath(item.WorkDir)
			if err != nil {
				return nil, fmt.Errorf("mcp config: resolve workdir for mcpServers.%s: %w", item.Name, err)
			}
			item.WorkDir = workDir
		}
		if server.CallTimeout > 0 {
			item.CallTimeout = time.Duration(server.CallTimeout) * time.Second
		}
		cfg.Servers = append(cfg.Servers, item)
	}
	return toolmcp.NewManager(cfg)
}

func defaultMCPConfigPath() string {
	return defaultMCPConfigLocation
}

func resolvePath(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(input, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		input = filepath.Join(home, strings.TrimPrefix(input, "~/"))
	}
	if !filepath.IsAbs(input) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		input = filepath.Join(cwd, input)
	}
	return filepath.Clean(input), nil
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
