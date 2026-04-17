package taskstream

import (
	"context"
	"testing"
)

func TestWithStreamer_NilContextReturnsBackground(t *testing.T) {
	var nilCtx context.Context
	ctx := WithStreamer(nilCtx, nil)
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}
	if ctx != context.Background() {
		t.Fatalf("expected background context, got %#v", ctx)
	}
}
