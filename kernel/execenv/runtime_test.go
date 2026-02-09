package execenv

import "testing"

func TestNew_DerivePolicyByMode(t *testing.T) {
	noSandbox, err := New(Config{Mode: ModeNoSandbox})
	if err != nil {
		t.Fatal(err)
	}
	if noSandbox.BashPolicy().Strategy != BashStrategyStrict {
		t.Fatalf("expected strict in no_sandbox, got %q", noSandbox.BashPolicy().Strategy)
	}

	sandbox, err := New(Config{Mode: ModeSandbox})
	if err != nil {
		t.Fatal(err)
	}
	if sandbox.BashPolicy().Strategy != BashStrategyAgentDecide {
		t.Fatalf("expected agent_decided in sandbox, got %q", sandbox.BashPolicy().Strategy)
	}
}
