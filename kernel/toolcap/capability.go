package toolcap

import "slices"

// Operation is one normalized tool operation class.
type Operation string

const (
	OperationFileRead  Operation = "file_read"
	OperationFileWrite Operation = "file_write"
	OperationExec      Operation = "exec"
	OperationNetwork   Operation = "network"
)

// RiskLevel is a coarse risk signal for policy decisions.
type RiskLevel string

const (
	RiskUnknown RiskLevel = "unknown"
	RiskLow     RiskLevel = "low"
	RiskMedium  RiskLevel = "medium"
	RiskHigh    RiskLevel = "high"
)

// Capability describes one tool's side-effect profile for policy hooks.
type Capability struct {
	Operations []Operation `json:"operations,omitempty"`
	Risk       RiskLevel   `json:"risk,omitempty"`
}

// HasOperation reports whether one operation is declared.
func (c Capability) HasOperation(op Operation) bool {
	return slices.Contains(c.Operations, op)
}

// Provider allows a value to declare capabilities.
type Provider interface {
	Capability() Capability
}

// Of returns declared capability, or a default unknown profile.
func Of(value any) Capability {
	if value == nil {
		return Capability{Risk: RiskUnknown}
	}
	withCap, ok := value.(Provider)
	if !ok {
		return Capability{Risk: RiskUnknown}
	}
	cap := withCap.Capability()
	if cap.Risk == "" {
		cap.Risk = RiskUnknown
	}
	if len(cap.Operations) == 0 {
		return cap
	}
	seen := map[Operation]struct{}{}
	out := make([]Operation, 0, len(cap.Operations))
	for _, one := range cap.Operations {
		if one == "" {
			continue
		}
		if _, exists := seen[one]; exists {
			continue
		}
		seen[one] = struct{}{}
		out = append(out, one)
	}
	cap.Operations = out
	return cap
}
