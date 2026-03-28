package acpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
)

func TestClientNewSessionMatchesACPXRequestShape(t *testing.T) {
	client, requests, respond, cleanup := newTestRPCClient()
	defer cleanup()
	meta := map[string]any{
		"caelis": map[string]any{
			"selfSpawnDepth": 1,
		},
	}

	done := make(chan error, 1)
	go func() {
		msg := <-requests
		if msg.Method != MethodSessionNew {
			done <- fmt.Errorf("unexpected method %q", msg.Method)
			return
		}
		params := decodeParamsMap(t, msg)
		if got := params["cwd"]; got != "/workspace/project" {
			done <- fmt.Errorf("unexpected cwd %#v", got)
			return
		}
		rawServers, ok := params["mcpServers"]
		if !ok {
			done <- fmt.Errorf("expected mcpServers in params: %#v", params)
			return
		}
		servers, ok := rawServers.([]any)
		if !ok || len(servers) != 0 {
			done <- fmt.Errorf("expected empty mcpServers array, got %#v", rawServers)
			return
		}
		if _, ok := params["sessionId"]; ok {
			done <- fmt.Errorf("session/new should not send sessionId: %#v", params)
			return
		}
		if !reflect.DeepEqual(params["_meta"], map[string]any{
			"caelis": map[string]any{
				"selfSpawnDepth": float64(1),
			},
		}) {
			done <- fmt.Errorf("unexpected _meta %#v", params["_meta"])
			return
		}
		done <- respond(msg.ID, map[string]any{"sessionId": "child-1"})
	}()

	resp, err := client.NewSession(context.Background(), "/workspace/project", meta)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if resp.SessionID != "child-1" {
		t.Fatalf("unexpected session id %q", resp.SessionID)
	}
}

func TestClientLoadSessionIncludesMCPServers(t *testing.T) {
	client, requests, respond, cleanup := newTestRPCClient()
	defer cleanup()
	meta := map[string]any{
		"caelis": map[string]any{
			"selfSpawnDepth": 1,
			"trace":          "load",
		},
	}

	done := make(chan error, 1)
	go func() {
		msg := <-requests
		if msg.Method != MethodSessionLoad {
			done <- fmt.Errorf("unexpected method %q", msg.Method)
			return
		}
		params := decodeParamsMap(t, msg)
		if got := params["sessionId"]; got != "child-1" {
			done <- fmt.Errorf("unexpected sessionId %#v", got)
			return
		}
		rawServers, ok := params["mcpServers"]
		if !ok {
			done <- fmt.Errorf("expected mcpServers in params: %#v", params)
			return
		}
		servers, ok := rawServers.([]any)
		if !ok || len(servers) != 0 {
			done <- fmt.Errorf("expected empty mcpServers array, got %#v", rawServers)
			return
		}
		if !reflect.DeepEqual(params["_meta"], map[string]any{
			"caelis": map[string]any{
				"selfSpawnDepth": float64(1),
				"trace":          "load",
			},
		}) {
			done <- fmt.Errorf("unexpected _meta %#v", params["_meta"])
			return
		}
		done <- respond(msg.ID, map[string]any{})
	}()

	if _, err := client.LoadSession(context.Background(), "child-1", "/workspace/project", meta); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientPromptUsesTextContentBlocks(t *testing.T) {
	client, requests, respond, cleanup := newTestRPCClient()
	defer cleanup()
	meta := map[string]any{
		"request": "prompt-1",
	}

	done := make(chan error, 1)
	go func() {
		msg := <-requests
		if msg.Method != MethodSessionPrompt {
			done <- fmt.Errorf("unexpected method %q", msg.Method)
			return
		}
		params := decodeParamsMap(t, msg)
		if got := params["sessionId"]; got != "child-1" {
			done <- fmt.Errorf("unexpected sessionId %#v", got)
			return
		}
		prompt, ok := params["prompt"].([]any)
		if !ok || len(prompt) != 1 {
			done <- fmt.Errorf("expected single prompt block, got %#v", params["prompt"])
			return
		}
		block, ok := prompt[0].(map[string]any)
		if !ok || block["type"] != "text" || block["text"] != "hello" {
			done <- fmt.Errorf("unexpected prompt block %#v", prompt[0])
			return
		}
		if !reflect.DeepEqual(params["_meta"], map[string]any{
			"request": "prompt-1",
		}) {
			done <- fmt.Errorf("unexpected _meta %#v", params["_meta"])
			return
		}
		done <- respond(msg.ID, map[string]any{"stopReason": "end_turn"})
	}()

	if _, err := client.Prompt(context.Background(), "child-1", "hello", meta); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDecodeUpdate_IgnoresUnknownExtensionUpdate(t *testing.T) {
	update, err := decodeUpdate(json.RawMessage(`{"sessionUpdate":"vendor_extension","value":1}`))
	if !errors.Is(err, errUnknownSessionUpdate) {
		t.Fatalf("expected unknown session update error, got %v", err)
	}
	if update != nil {
		t.Fatalf("expected unknown update to be ignored, got %#v", update)
	}
}

func TestDecodeUpdate_DecodesSessionInfoUpdate(t *testing.T) {
	update, err := decodeUpdate(json.RawMessage(`{"sessionUpdate":"session_info_update","title":"child","updatedAt":"2026-03-24T00:00:00Z"}`))
	if err != nil {
		t.Fatalf("decodeUpdate: %v", err)
	}
	info, ok := update.(SessionInfoUpdate)
	if !ok {
		t.Fatalf("expected SessionInfoUpdate, got %#v", update)
	}
	if info.Title == nil || *info.Title != "child" {
		t.Fatalf("unexpected title %+v", info)
	}
	if info.UpdatedAt == nil || *info.UpdatedAt != "2026-03-24T00:00:00Z" {
		t.Fatalf("unexpected updatedAt %+v", info)
	}
}

func TestSliceTextByLines(t *testing.T) {
	line := 2
	limit := 2
	got := sliceTextByLines("one\ntwo\nthree\nfour", &line, &limit)
	if got != "two\nthree" {
		t.Fatalf("unexpected slice %q", got)
	}
}

func TestTruncateFromStartAtRuneBoundary(t *testing.T) {
	got, truncated := truncateFromStartAtRuneBoundary("ab世界", 5)
	if !truncated {
		t.Fatal("expected output to be truncated")
	}
	if got != "界" {
		t.Fatalf("unexpected truncation result %q", got)
	}
}

func newTestRPCClient() (*Client, <-chan Message, func(any, any) error, func()) {
	reqReader, reqWriter := io.Pipe()
	respReader, respWriter := io.Pipe()
	serveCtx, cancel := context.WithCancel(context.Background())
	client := &Client{
		conn: NewConn(respReader, reqWriter),
		done: make(chan error, 1),
	}
	go func() {
		client.done <- client.conn.Serve(serveCtx, nil, nil)
	}()
	requests := make(chan Message, 1)
	go func() {
		reader := bufio.NewReader(reqReader)
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			return
		}
		requests <- msg
	}()
	respond := func(id any, result any) error {
		return json.NewEncoder(respWriter).Encode(Message{
			JSONRPC: JSONRPCVersion,
			ID:      id,
			Result:  result,
		})
	}
	cleanup := func() {
		cancel()
		_ = reqReader.Close()
		_ = reqWriter.Close()
		_ = respReader.Close()
		_ = respWriter.Close()
		select {
		case <-client.done:
		default:
		}
	}
	return client, requests, respond, cleanup
}

func decodeParamsMap(t *testing.T, msg Message) map[string]any {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	return params
}

func TestSessionInfoUpdateRoundTripShape(t *testing.T) {
	title := "demo"
	updatedAt := "2026-03-24T00:00:00Z"
	raw, err := json.Marshal(SessionInfoUpdate{
		SessionUpdate: UpdateSessionInfo,
		Title:         &title,
		UpdatedAt:     &updatedAt,
	})
	if err != nil {
		t.Fatalf("marshal session info update: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal session info update: %v", err)
	}
	want := map[string]any{
		"sessionUpdate": "session_info_update",
		"title":         "demo",
		"updatedAt":     "2026-03-24T00:00:00Z",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected session info update shape: got %#v want %#v", got, want)
	}
}
