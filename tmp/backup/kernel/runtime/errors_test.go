package runtime

import (
	"fmt"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func TestSessionBusyError_Code(t *testing.T) {
	err := &SessionBusyError{AppName: "app", UserID: "u", SessionID: "s"}
	if !IsSessionBusy(err) {
		t.Fatal("expected IsSessionBusy to detect SessionBusyError")
	}
	if toolexec.ErrorCodeOf(err) != toolexec.ErrorCodeSessionBusy {
		t.Fatalf("expected error code %q, got %q", toolexec.ErrorCodeSessionBusy, toolexec.ErrorCodeOf(err))
	}
}

func TestSessionBusyError_CodeWrapped(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &SessionBusyError{AppName: "app", UserID: "u", SessionID: "s"})
	if !IsSessionBusy(err) {
		t.Fatal("expected IsSessionBusy to detect wrapped SessionBusyError")
	}
	if toolexec.ErrorCodeOf(err) != toolexec.ErrorCodeSessionBusy {
		t.Fatalf("expected wrapped error code %q, got %q", toolexec.ErrorCodeSessionBusy, toolexec.ErrorCodeOf(err))
	}
}
