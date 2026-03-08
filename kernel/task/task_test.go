package task

import (
	"context"
	"testing"
)

func TestWithManager_NilContextReturnsBackground(t *testing.T) {
	ctx := WithManager(nil, nil)
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}
	if ctx != context.Background() {
		t.Fatalf("expected background context, got %#v", ctx)
	}
}
