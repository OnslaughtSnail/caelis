package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolveSingleShotInput_FromPromptFlag(t *testing.T) {
	got, singleShot, err := resolveSingleShotInput("hello", "", strings.NewReader(""), true, true)
	if err != nil {
		t.Fatalf("resolve input failed: %v", err)
	}
	if !singleShot {
		t.Fatal("expected single-shot mode for -p input")
	}
	if got != "hello" {
		t.Fatalf("expected prompt text, got %q", got)
	}
}

func TestResolveSingleShotInput_FromPipedStdin(t *testing.T) {
	got, singleShot, err := resolveSingleShotInput("", "", strings.NewReader("from pipe\n"), false, false)
	if err != nil {
		t.Fatalf("resolve input failed: %v", err)
	}
	if !singleShot {
		t.Fatal("expected single-shot mode for piped stdin")
	}
	if got != "from pipe" {
		t.Fatalf("unexpected piped prompt: %q", got)
	}
}

func TestResolveSingleShotInput_RejectsMissingPromptWhenStdoutNonTTY(t *testing.T) {
	_, _, err := resolveSingleShotInput("", "", strings.NewReader(""), true, false)
	if err == nil {
		t.Fatal("expected error for non-interactive output without input")
	}
}

func TestParseHeadlessOutputFormat(t *testing.T) {
	got, err := parseHeadlessOutputFormat("json")
	if err != nil {
		t.Fatalf("parse format failed: %v", err)
	}
	if got != headlessFormatJSON {
		t.Fatalf("expected json format, got %q", got)
	}
	if _, err := parseHeadlessOutputFormat("xml"); err == nil {
		t.Fatal("expected invalid format error")
	}
}

func TestWriteHeadlessResult_JSON(t *testing.T) {
	var buf bytes.Buffer
	err := writeHeadlessResult(&buf, headlessFormatJSON, headlessRunResult{
		SessionID:    "s-1",
		Output:       "ok",
		PromptTokens: 12,
	})
	if err != nil {
		t.Fatalf("write json failed: %v", err)
	}
	text := strings.TrimSpace(buf.String())
	if !strings.Contains(text, `"session_id":"s-1"`) || !strings.Contains(text, `"output":"ok"`) {
		t.Fatalf("unexpected json output: %q", text)
	}
}

func TestWriteHeadlessResult_Text(t *testing.T) {
	var buf bytes.Buffer
	err := writeHeadlessResult(&buf, headlessFormatText, headlessRunResult{
		Output: "hello",
	})
	if err != nil {
		t.Fatalf("write text failed: %v", err)
	}
	if got := buf.String(); got != "hello\n" {
		t.Fatalf("unexpected text output %q", got)
	}
}
