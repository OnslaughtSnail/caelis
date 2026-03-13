package slashcmd

import "strings"

type Definition struct {
	Name        string
	Description string
	InputHint   string
}

type Invocation struct {
	Name string
	Args []string
	Raw  string
}

type Registry struct {
	defs map[string]Definition
	list []Definition
}

func New(defs ...Definition) Registry {
	reg := Registry{
		defs: map[string]Definition{},
	}
	for _, item := range defs {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		if name == "" {
			continue
		}
		item.Name = name
		reg.defs[name] = item
		reg.list = append(reg.list, item)
	}
	return reg
}

func (r Registry) Definitions() []Definition {
	out := make([]Definition, 0, len(r.list))
	out = append(out, r.list...)
	return out
}

func (r Registry) Has(name string) bool {
	_, ok := r.defs[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func Parse(line string) (Invocation, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return Invocation{}, false
	}
	parts := strings.Fields(strings.TrimPrefix(line, "/"))
	if len(parts) == 0 {
		return Invocation{}, false
	}
	return Invocation{
		Name: strings.ToLower(strings.TrimSpace(parts[0])),
		Args: append([]string(nil), parts[1:]...),
		Raw:  line,
	}, true
}
