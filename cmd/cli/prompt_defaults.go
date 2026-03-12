package main

import "strings"

const (
	defaultExperimentalLSPRoutingPrompt = `Use LSP_DEFINITION and LSP_REFERENCES to navigate by symbol name when the workspace language server is enabled.
Use LSP_SYMBOLS to discover exact symbol names before semantic lookups.
Use SEARCH and GLOB for text-level fallback when semantic lookup is unnecessary or unavailable.`
)

func builtInIdentityPrompt(appName string) string {
	name := strings.TrimSpace(appName)
	if name == "" {
		name = "caelis"
	}
	return "# Agent Identity\n\nYou are " + name + "."
}
