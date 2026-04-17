package agents

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Transport string

const (
	TransportSelf Transport = "self"
	TransportACP  Transport = "acp"
)

const (
	StabilityStable       = "stable"
	StabilityExperimental = "experimental"
)

type Descriptor struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Stability   string            `json:"stability,omitempty"`
	Transport   Transport         `json:"transport"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	WorkDir     string            `json:"workDir,omitempty"`
	Builtin     bool              `json:"builtin,omitempty"`
}

const selfAgentID = "self"

func SelfDescriptor() Descriptor {
	return Descriptor{
		ID:          selfAgentID,
		Name:        "self",
		Description: "Built-in local child session.",
		Stability:   StabilityStable,
		Transport:   TransportSelf,
		Builtin:     true,
	}
}

type Registry struct {
	mu     sync.RWMutex
	agents map[string]Descriptor
}

func NewRegistry(extra ...Descriptor) *Registry {
	r := &Registry{agents: map[string]Descriptor{}}
	self := SelfDescriptor()
	r.agents[self.ID] = self
	for _, d := range extra {
		d = normalizeDescriptor(d)
		id := strings.TrimSpace(d.ID)
		if id == "" || id == selfAgentID {
			continue
		}
		d.ID = id
		r.agents[id] = d
	}
	return r
}

func (r *Registry) Lookup(id string) (Descriptor, bool) {
	if r == nil {
		return Descriptor{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.agents[strings.TrimSpace(id)]
	return d, ok
}

func (r *Registry) Register(d Descriptor) error {
	if r == nil {
		return fmt.Errorf("agents: registry is nil")
	}
	id := strings.TrimSpace(d.ID)
	if id == "" {
		return fmt.Errorf("agents: descriptor id is required")
	}
	if id == selfAgentID {
		return fmt.Errorf("agents: %q is a reserved builtin agent", selfAgentID)
	}
	d = normalizeDescriptor(d)
	d.ID = id
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[id] = d
	return nil
}

func (r *Registry) List() []Descriptor {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Descriptor, 0, len(r.agents))
	for _, d := range r.agents {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) IDs() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.agents))
	for id := range r.agents {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) Validate() error {
	if r == nil {
		return fmt.Errorf("agents: registry is nil")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.agents[selfAgentID]; !ok {
		return fmt.Errorf("agents: builtin %q agent is missing", selfAgentID)
	}
	for id, d := range r.agents {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("agents: empty id in registry")
		}
		if d.Transport == TransportACP && strings.TrimSpace(d.Command) == "" {
			return fmt.Errorf("agents: acp agent %q requires a command", id)
		}
	}
	return nil
}

func normalizeDescriptor(d Descriptor) Descriptor {
	d.ID = strings.TrimSpace(d.ID)
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		d.Name = d.ID
	}
	d.Description = strings.TrimSpace(d.Description)
	d.Stability = NormalizeStability(d.Stability)
	if d.Transport == "" {
		d.Transport = TransportACP
	}
	d.Command = strings.TrimSpace(d.Command)
	d.WorkDir = strings.TrimSpace(d.WorkDir)
	d.Args = append([]string(nil), d.Args...)
	d.Env = cloneStringMap(d.Env)
	return d
}

func NormalizeStability(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", StabilityExperimental:
		return StabilityExperimental
	case StabilityStable:
		return StabilityStable
	default:
		return StabilityExperimental
	}
}
