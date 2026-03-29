package acpclient

import "testing"

func TestResolveApproveAllOnce_PrefersAllowOnceOverAllowAlways(t *testing.T) {
	req := RequestPermissionRequest{
		Options: []PermissionOption{
			{OptionID: "allow_always", Name: "Always allow", Kind: "allow_always"},
			{OptionID: "allow_once", Name: "Allow once", Kind: "allow_once"},
			{OptionID: "reject_once", Name: "Reject once", Kind: "reject_once"},
		},
	}

	got := ResolveApproveAllOnce("full_access", "copilot", req)
	if got.Decision != PermissionDecisionAutoAllowOnce {
		t.Fatalf("expected auto allow once, got %q", got.Decision)
	}
	if got.OptionID != "allow_once" {
		t.Fatalf("expected allow_once, got %q", got.OptionID)
	}
}

func TestResolveApproveAllOnce_UsesKnownAgentAliasWhenKindMissing(t *testing.T) {
	req := RequestPermissionRequest{
		Options: []PermissionOption{
			{OptionID: "approve", Name: "Approve"},
			{OptionID: "deny", Name: "Deny"},
		},
	}

	got := ResolveApproveAllOnce("full_access", "gemini", req)
	if got.Decision != PermissionDecisionAutoAllowOnce {
		t.Fatalf("expected auto allow once, got %q", got.Decision)
	}
	if got.OptionID != "approve" {
		t.Fatalf("expected approve alias, got %q", got.OptionID)
	}
}

func TestResolveApproveAllOnce_DoesNotTreatAllowAlwaysAliasAsAllowOnce(t *testing.T) {
	req := RequestPermissionRequest{
		Options: []PermissionOption{
			{OptionID: "allow", Name: "Allow", Kind: "allow_always"},
			{OptionID: "deny", Name: "Deny", Kind: "reject_once"},
		},
	}

	got := ResolveApproveAllOnce("full_access", "copilot", req)
	if got.Decision != PermissionDecisionAskUser {
		t.Fatalf("expected ask_user fallback, got %q", got.Decision)
	}
	if got.OptionID != "" {
		t.Fatalf("expected empty option id on fallback, got %q", got.OptionID)
	}
}

func TestResolveApproveAllOnce_UnknownAgentFallsBackToUserApproval(t *testing.T) {
	req := RequestPermissionRequest{
		Options: []PermissionOption{
			{OptionID: "approve", Name: "Approve"},
			{OptionID: "deny", Name: "Deny"},
		},
	}

	got := ResolveApproveAllOnce("full_access", "unknown-agent", req)
	if got.Decision != PermissionDecisionAskUser {
		t.Fatalf("expected ask_user fallback, got %q", got.Decision)
	}
	if got.OptionID != "" {
		t.Fatalf("expected empty option id on fallback, got %q", got.OptionID)
	}
}

func TestResolveApproveAllOnce_NonFullAccessDefersToExistingPolicy(t *testing.T) {
	req := RequestPermissionRequest{
		Options: []PermissionOption{
			{OptionID: "allow_once", Name: "Allow once", Kind: "allow_once"},
		},
	}

	got := ResolveApproveAllOnce("default", "copilot", req)
	if got.Decision != PermissionDecisionDeferExistingPolicy {
		t.Fatalf("expected defer_existing_policy, got %q", got.Decision)
	}
}

func TestSelectPermissionOptionID_PrefersSingleUseOptions(t *testing.T) {
	options := []PermissionOption{
		{OptionID: "allow_always", Name: "Always allow", Kind: "allow_always"},
		{OptionID: "allow_once", Name: "Allow once", Kind: "allow_once"},
		{OptionID: "reject_always", Name: "Always reject", Kind: "reject_always"},
		{OptionID: "reject_once", Name: "Reject once", Kind: "reject_once"},
	}

	if got := SelectPermissionOptionID(options, true); got != "allow_once" {
		t.Fatalf("expected allow_once, got %q", got)
	}
	if got := SelectPermissionOptionID(options, false); got != "reject_once" {
		t.Fatalf("expected reject_once, got %q", got)
	}
}

func TestKnownAgentPermissionAliases_CoversSupportedAgents(t *testing.T) {
	for _, agentID := range []string{
		"self",
		"codex",
		"copilot",
		"gemini",
		"claude",
		"openclaw",
		"pi",
		"cursor",
		"droid",
		"iflow",
		"kilocode",
		"kimi",
		"kiro",
		"opencode",
		"qwen",
	} {
		aliases, ok := knownAgentPermissionAliases[agentID]
		if !ok {
			t.Fatalf("expected permission aliases for %q", agentID)
		}
		if len(aliases.AllowOnce) == 0 {
			t.Fatalf("expected allow-once aliases for %q", agentID)
		}
		if len(aliases.RejectOnce) == 0 {
			t.Fatalf("expected reject-once aliases for %q", agentID)
		}
	}
}
