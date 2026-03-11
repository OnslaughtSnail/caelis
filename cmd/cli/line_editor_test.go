package main

import "testing"

func TestNewLineEditor(t *testing.T) {
	editor, err := newLineEditor(lineEditorConfig{})
	if err != nil {
		t.Fatalf("expected line editor without error, got %v", err)
	}
	if editor == nil {
		t.Fatal("expected non-nil line editor")
	}
}
