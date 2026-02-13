package execenv

import (
	"strings"
	"testing"
)

func TestErrorCode_ApprovalErrors(t *testing.T) {
	required := &ApprovalRequiredError{Reason: "needs approval"}
	if !IsErrorCode(required, ErrorCodeApprovalRequired) {
		t.Fatalf("expected approval required code, got %q", ErrorCodeOf(required))
	}
	aborted := &ApprovalAbortedError{Reason: "denied"}
	if !IsErrorCode(aborted, ErrorCodeApprovalAborted) {
		t.Fatalf("expected approval aborted code, got %q", ErrorCodeOf(aborted))
	}
}

func TestErrorCode_RuntimeUnsupportedSandboxType(t *testing.T) {
	oldGoos := runtimeGOOS
	runtimeGOOS = "darwin"
	defer func() {
		runtimeGOOS = oldGoos
	}()
	_, err := New(Config{PermissionMode: PermissionModeDefault, SandboxType: "docker"})
	if err == nil {
		t.Fatal("expected unsupported sandbox error")
	}
	if !IsErrorCode(err, ErrorCodeSandboxUnsupported) {
		t.Fatalf("expected sandbox unsupported code, got %q (%v)", ErrorCodeOf(err), err)
	}
	if !strings.Contains(err.Error(), "unsupported on darwin") {
		t.Fatalf("expected descriptive message, got %v", err)
	}
}

func TestErrorCode_SessionBusyFromRuntime(t *testing.T) {
	err := NewCodedError(ErrorCodeSessionBusy, "session busy")
	if !IsErrorCode(err, ErrorCodeSessionBusy) {
		t.Fatalf("expected session busy code, got %q", ErrorCodeOf(err))
	}
}
