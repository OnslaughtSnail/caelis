package main

import "testing"

func TestDefaultNoAnimation(t *testing.T) {
	t.Setenv("CAELIS_NO_ANIMATION", "1")
	if !defaultNoAnimation() {
		t.Fatal("expected CAELIS_NO_ANIMATION=1 to enable no-animation")
	}

	t.Setenv("CAELIS_NO_ANIMATION", "false")
	if defaultNoAnimation() {
		t.Fatal("expected CAELIS_NO_ANIMATION=false to leave animations enabled")
	}
}
