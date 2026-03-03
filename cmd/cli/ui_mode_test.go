package main

import "testing"

func TestResolveInteractiveUIMode_AutoPrefersTUIOnTTY(t *testing.T) {
	mode, err := resolveInteractiveUIMode("auto", true, true)
	if err != nil {
		t.Fatalf("resolve mode failed: %v", err)
	}
	if mode != uiModeTUI {
		t.Fatalf("expected %q, got %q", uiModeTUI, mode)
	}
}

func TestResolveInteractiveUIMode_AutoWithoutTTYReturnsError(t *testing.T) {
	_, err := resolveInteractiveUIMode("auto", true, false)
	if err == nil {
		t.Fatal("expected error when auto mode is used without tty")
	}
}

func TestResolveInteractiveUIMode_ExplicitTUIRequiresTTY(t *testing.T) {
	_, err := resolveInteractiveUIMode("tui", false, true)
	if err == nil {
		t.Fatal("expected error when tui mode is requested without tty")
	}
}

func TestResolveInteractiveUIMode_ExplicitLineRejected(t *testing.T) {
	_, err := resolveInteractiveUIMode("line", true, true)
	if err == nil {
		t.Fatal("expected explicit line mode to be rejected")
	}
}

func TestResolveInteractiveUIMode_InvalidValue(t *testing.T) {
	_, err := resolveInteractiveUIMode("invalid", true, true)
	if err == nil {
		t.Fatal("expected invalid mode error")
	}
}
