package capability

import "testing"

type capabilityValue struct{}

func (capabilityValue) Capability() Capability {
	return Capability{
		Operations: []Operation{OperationFileRead, OperationFileRead, OperationExec},
		Risk:       RiskMedium,
	}
}

func TestOf_DefaultUnknown(t *testing.T) {
	got := Of(nil)
	if got.Risk != RiskUnknown {
		t.Fatalf("expected unknown risk for nil value, got %q", got.Risk)
	}
}

func TestOf_NormalizesOperations(t *testing.T) {
	got := Of(capabilityValue{})
	if got.Risk != RiskMedium {
		t.Fatalf("expected risk=%q, got %q", RiskMedium, got.Risk)
	}
	if !got.HasOperation(OperationFileRead) || !got.HasOperation(OperationExec) {
		t.Fatalf("expected declared operations in capability: %#v", got.Operations)
	}
	if len(got.Operations) != 2 {
		t.Fatalf("expected deduped operations length 2, got %d (%#v)", len(got.Operations), got.Operations)
	}
}
