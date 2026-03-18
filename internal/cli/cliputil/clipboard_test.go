package cliputil

import (
	"strings"
	"testing"
)

func TestEncodeDecodeWSLClipboardTextRoundTrip(t *testing.T) {
	original := "\u4e2d\u6587\u6d4b\u8bd5ABC\nsecond line"

	encoded := encodeWSLClipboardText(original)
	decoded, err := decodeWSLClipboardText([]byte(encoded))
	if err != nil {
		t.Fatalf("decodeWSLClipboardText returned error: %v", err)
	}
	if decoded != original {
		t.Fatalf("decodeWSLClipboardText() = %q, want %q", decoded, original)
	}
}

func TestDecodeWSLClipboardTextTrimsWhitespace(t *testing.T) {
	decoded, err := decodeWSLClipboardText([]byte("5Lit5paH\n"))
	if err != nil {
		t.Fatalf("decodeWSLClipboardText returned error: %v", err)
	}
	if decoded != "\u4e2d\u6587" {
		t.Fatalf("decodeWSLClipboardText() = %q, want %q", decoded, "\u4e2d\u6587")
	}
}

func TestDecodeWSLClipboardTextRejectsInvalidBase64(t *testing.T) {
	if _, err := decodeWSLClipboardText([]byte("%%%")); err == nil {
		t.Fatal("expected invalid base64 error")
	}
}

func TestWSLClipboardReadScriptUsesASCIIBase64Bridge(t *testing.T) {
	script := wslClipboardReadScript()
	for _, want := range []string{
		"[Console]::OutputEncoding = [System.Text.Encoding]::ASCII",
		"Get-Clipboard -Raw",
		"[System.Text.Encoding]::UTF8.GetBytes($text)",
		"[Convert]::ToBase64String($bytes)",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("read script missing %q: %s", want, script)
		}
	}
}

func TestWSLClipboardWriteScriptUsesASCIIBase64Bridge(t *testing.T) {
	script := wslClipboardWriteScript()
	for _, want := range []string{
		"[Console]::InputEncoding = [System.Text.Encoding]::ASCII",
		"[Console]::In.ReadToEnd()",
		"[Convert]::FromBase64String($base64)",
		"[System.Text.Encoding]::UTF8.GetString($bytes)",
		"Set-Clipboard -Value $text",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("write script missing %q: %s", want, script)
		}
	}
}
