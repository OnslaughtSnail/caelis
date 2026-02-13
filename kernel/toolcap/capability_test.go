package toolcap

import "testing"

type capabilityValue struct{}

func (capabilityValue) Capability() Capability {
	return Capability{
		Operations: []Operation{OperationFileRead, OperationFileRead, OperationExec},
		Risk:       RiskMedium,
	}
}

func TestOf_DefaultUnknown(t *testing.T) {
	cap := Of(nil)
	if cap.Risk != RiskUnknown {
		t.Fatalf("expected unknown risk for nil value, got %q", cap.Risk)
	}
}

func TestOf_NormalizesOperations(t *testing.T) {
	cap := Of(capabilityValue{})
	if cap.Risk != RiskMedium {
		t.Fatalf("expected risk=%q, got %q", RiskMedium, cap.Risk)
	}
	if !cap.HasOperation(OperationFileRead) || !cap.HasOperation(OperationExec) {
		t.Fatalf("expected declared operations in capability: %#v", cap.Operations)
	}
	if len(cap.Operations) != 2 {
		t.Fatalf("expected deduped operations length 2, got %d (%#v)", len(cap.Operations), cap.Operations)
	}
}
