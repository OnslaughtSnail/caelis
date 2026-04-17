package model

import "testing"

func TestParseToolCallArgs(t *testing.T) {
	t.Run("plain object", func(t *testing.T) {
		got, err := ParseToolCallArgs(`{"path":"a.txt","lines":"ok"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["path"] != "a.txt" {
			t.Fatalf("unexpected path: %#v", got["path"])
		}
	})

	t.Run("quoted json object", func(t *testing.T) {
		got, err := ParseToolCallArgs(`"{\"path\":\"a.txt\",\"lines\":\"ok\"}"`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["lines"] != "ok" {
			t.Fatalf("unexpected lines: %#v", got["lines"])
		}
	})

	t.Run("code fence", func(t *testing.T) {
		got, err := ParseToolCallArgs("```json\n{\"path\":\"a.txt\"}\n```")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["path"] != "a.txt" {
			t.Fatalf("unexpected path: %#v", got["path"])
		}
	})

	t.Run("truncated object returns error", func(t *testing.T) {
		_, err := ParseToolCallArgs(`{"path":"a.txt","lines":"ok"`)
		if err == nil {
			t.Fatal("expected error for truncated object")
		}
	})

	t.Run("invalid non object", func(t *testing.T) {
		_, err := ParseToolCallArgs(`[1,2,3]`)
		if err == nil {
			t.Fatal("expected error for non-object args")
		}
	})
}
