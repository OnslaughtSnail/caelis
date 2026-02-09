package lspbroker

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

// ActivateRequest describes one LSP toolset activation request.
type ActivateRequest struct {
	Language     string
	Capabilities []string
	Workspace    string
}

// ActivateResult describes one activation result for current session.
type ActivateResult struct {
	Language       string
	ToolsetID      string
	Activated      bool
	AddedTools     []string
	ActiveToolsets []string
}

// ActivationController is implemented by runtime invocation context so
// activation tools can mutate available toolsets in-session.
type ActivationController interface {
	ActivateLSP(context.Context, ActivateRequest) (ActivateResult, error)
	ActivatedToolsets() []string
	AvailableLSP() []string
}

// ToolSet is one dynamic tool bundle for one activated capability set.
type ToolSet struct {
	ID       string
	Language string
	Tools    []tool.Tool
}

// Adapter builds language-specific toolsets.
type Adapter interface {
	Language() string
	BuildToolSet(context.Context, ActivateRequest) (*ToolSet, error)
}

// Broker resolves language requests to concrete toolsets.
type Broker struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

// New returns one empty broker.
func New() *Broker {
	return &Broker{adapters: map[string]Adapter{}}
}

// RegisterAdapter registers one language adapter.
func (b *Broker) RegisterAdapter(adapter Adapter) error {
	if b == nil {
		return fmt.Errorf("lspbroker: broker is nil")
	}
	if adapter == nil {
		return fmt.Errorf("lspbroker: adapter is nil")
	}
	lang := strings.ToLower(strings.TrimSpace(adapter.Language()))
	if lang == "" {
		return fmt.Errorf("lspbroker: adapter language is empty")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.adapters[lang]; exists {
		return fmt.Errorf("lspbroker: duplicated adapter for language %q", lang)
	}
	b.adapters[lang] = adapter
	return nil
}

// AvailableLanguages lists registered languages.
func (b *Broker) AvailableLanguages() []string {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, 0, len(b.adapters))
	for lang := range b.adapters {
		out = append(out, lang)
	}
	sort.Strings(out)
	return out
}

// Resolve resolves one activation request to a dynamic toolset.
func (b *Broker) Resolve(ctx context.Context, req ActivateRequest) (*ToolSet, error) {
	if b == nil {
		return nil, fmt.Errorf("lspbroker: broker is nil")
	}
	language := strings.ToLower(strings.TrimSpace(req.Language))
	if language == "" {
		return nil, fmt.Errorf("lspbroker: language is required")
	}
	b.mu.RLock()
	adapter, ok := b.adapters[language]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("lspbroker: unsupported language %q", language)
	}
	req.Language = language
	toolset, err := adapter.BuildToolSet(ctx, req)
	if err != nil {
		return nil, err
	}
	if toolset == nil {
		return nil, fmt.Errorf("lspbroker: adapter returned nil toolset for language %q", language)
	}
	if toolset.ID == "" {
		toolset.ID = "lsp:" + language
	}
	if toolset.Language == "" {
		toolset.Language = language
	}
	return toolset, nil
}
