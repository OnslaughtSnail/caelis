package agents

import "testing"

func TestNewRegistryHasSelf(t *testing.T) {
	r := NewRegistry()
	d, ok := r.Lookup("self")
	if !ok {
		t.Fatal("expected self agent to be registered")
	}
	if d.ID != "self" {
		t.Fatalf("expected id=self, got %q", d.ID)
	}
	if d.Transport != TransportSelf {
		t.Fatalf("expected transport=self, got %q", d.Transport)
	}
	if !d.Builtin {
		t.Fatal("expected builtin=true")
	}
}

func TestRegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	err := r.Register(Descriptor{
		ID:        "codex",
		Name:      "Codex Agent",
		Transport: TransportACP,
		Endpoint:  "http://localhost:9000",
	})
	if err != nil {
		t.Fatal(err)
	}
	d, ok := r.Lookup("codex")
	if !ok {
		t.Fatal("expected codex agent to be registered")
	}
	if d.Name != "Codex Agent" {
		t.Fatalf("expected name=Codex Agent, got %q", d.Name)
	}
}

func TestRegisterSelfFails(t *testing.T) {
	r := NewRegistry()
	err := r.Register(Descriptor{ID: "self", Transport: TransportSelf})
	if err == nil {
		t.Fatal("expected error when registering self")
	}
}

func TestRegisterEmptyIDFails(t *testing.T) {
	r := NewRegistry()
	err := r.Register(Descriptor{ID: "", Transport: TransportSelf})
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestValidate(t *testing.T) {
	r := NewRegistry()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateACPMissingEndpoint(t *testing.T) {
	r := NewRegistry(Descriptor{
		ID:        "remote",
		Transport: TransportACP,
	})
	if err := r.Validate(); err == nil {
		t.Fatal("expected validation error for ACP agent without endpoint or command")
	}
}

func TestValidateACPWithCommand(t *testing.T) {
	r := NewRegistry(Descriptor{
		ID:        "local-agent",
		Transport: TransportACP,
		Command:   "/usr/bin/my-agent",
		Args:      []string{"--stdio"},
	})
	if err := r.Validate(); err != nil {
		t.Fatalf("ACP agent with command should be valid: %v", err)
	}
}

func TestNewRegistryWithExtra(t *testing.T) {
	r := NewRegistry(
		Descriptor{ID: "codex", Name: "Codex", Transport: TransportACP, Endpoint: "http://x"},
		Descriptor{ID: "gemini", Name: "Gemini", Transport: TransportACP, Endpoint: "http://y"},
	)
	ids := r.IDs()
	if len(ids) != 3 {
		t.Fatalf("expected 3 agents (self+2 extra), got %d", len(ids))
	}
}

func TestIDsSorted(t *testing.T) {
	r := NewRegistry(
		Descriptor{ID: "zeta", Name: "Zeta", Transport: TransportACP, Endpoint: "http://z"},
		Descriptor{ID: "alpha", Name: "Alpha", Transport: TransportACP, Endpoint: "http://a"},
		Descriptor{ID: "mid", Name: "Mid", Transport: TransportACP, Endpoint: "http://m"},
	)
	ids := r.IDs()
	expected := []string{"alpha", "mid", "self", "zeta"}
	if len(ids) != len(expected) {
		t.Fatalf("expected %d ids, got %d", len(expected), len(ids))
	}
	for i, want := range expected {
		if ids[i] != want {
			t.Fatalf("ids[%d] = %q, want %q", i, ids[i], want)
		}
	}
}

func TestListSorted(t *testing.T) {
	r := NewRegistry(
		Descriptor{ID: "zeta", Name: "Zeta", Transport: TransportACP, Endpoint: "http://z"},
		Descriptor{ID: "alpha", Name: "Alpha", Transport: TransportACP, Endpoint: "http://a"},
	)
	list := r.List()
	expected := []string{"alpha", "self", "zeta"}
	if len(list) != len(expected) {
		t.Fatalf("expected %d descriptors, got %d", len(expected), len(list))
	}
	for i, want := range expected {
		if list[i].ID != want {
			t.Fatalf("list[%d].ID = %q, want %q", i, list[i].ID, want)
		}
	}
}

func TestList(t *testing.T) {
	r := NewRegistry()
	list := r.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(list))
	}
	if list[0].ID != "self" {
		t.Fatalf("expected self, got %q", list[0].ID)
	}
}
