package acpclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
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

func TestConnServe_ProcessesNotificationsInArrivalOrder(t *testing.T) {
	reader := bytes.NewBufferString(
		"{\"jsonrpc\":\"2.0\",\"method\":\"first\"}\n" +
			"{\"jsonrpc\":\"2.0\",\"method\":\"second\"}\n",
	)
	conn := NewConn(reader, &bytes.Buffer{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{}, 1)
	var (
		mu    sync.Mutex
		order []string
	)

	done := make(chan error, 1)
	go func() {
		done <- conn.Serve(ctx, nil, func(_ context.Context, msg Message) {
			mu.Lock()
			order = append(order, msg.Method)
			mu.Unlock()
			switch msg.Method {
			case "first":
				firstStarted <- struct{}{}
				<-releaseFirst
			case "second":
				secondStarted <- struct{}{}
				cancel()
			}
		})
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first notification did not start")
	}

	select {
	case <-secondStarted:
		t.Fatal("second notification started before first completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)

	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second notification did not run after first completed")
	}

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve returned unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("unexpected notification order: %v", order)
	}
}

func TestConnCall_IncludesRPCErrorData(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	clientConn := NewConn(s2cR, c2sW)
	serverConn := NewConn(c2sR, s2cW)

	go func() {
		_ = serverConn.Serve(ctx, func(_ context.Context, msg Message) (any, *RPCError) {
			if msg.Method != "boom" {
				return nil, &RPCError{Code: -32601, Message: "method not found"}
			}
			return nil, &RPCError{
				Code:    -32603,
				Message: "Internal error",
				Data: map[string]any{
					"statusCode": 900,
					"traceId":    "trace-123",
				},
			}
		}, func(context.Context, Message) {})
	}()
	go func() {
		_ = clientConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}, func(context.Context, Message) {})
	}()

	err := clientConn.Call(context.Background(), "boom", map[string]any{"x": 1}, nil)
	if err == nil {
		t.Fatal("expected rpc error")
	}
	got := err.Error()
	if got != `acp rpc error -32603: Internal error (data: {"statusCode":900,"traceId":"trace-123"})` {
		t.Fatalf("unexpected error text: %q", got)
	}
}
