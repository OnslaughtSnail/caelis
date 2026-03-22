package acpclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestConnServe_ReturnsContextErrorWhenCanceledDuringRead(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()

	conn := NewConn(reader, &bytes.Buffer{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- conn.Serve(ctx, nil, nil)
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}
}
