package lspclient

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadMessage(t *testing.T) {
	raw := "Content-Length: 17\r\n\r\n{\"jsonrpc\":\"2.0\"}"
	payload, err := readMessage(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(payload) != "{\"jsonrpc\":\"2.0\"}" {
		t.Fatalf("unexpected payload: %s", string(payload))
	}
}

func TestNormalizeID(t *testing.T) {
	if got := normalizeID([]byte(`"42"`)); got != "42" {
		t.Fatalf("unexpected normalize result: %q", got)
	}
	if got := normalizeID([]byte(`7`)); got != "7" {
		t.Fatalf("unexpected normalize result: %q", got)
	}
}
