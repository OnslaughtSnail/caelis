//go:build linux

package imageutil

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// ExtractClipboardImage checks the Linux clipboard for image data using xclip.
// Returns (data, mimeType, nil) on success, (nil, "", nil) if no image,
// or (nil, "", err) on failure.
func ExtractClipboardImage() ([]byte, string, error) {
	// Check if xclip is available.
	if _, err := exec.LookPath("xclip"); err != nil {
		return nil, "", fmt.Errorf("xclip not found: install xclip to use clipboard image paste")
	}

	// List available clipboard targets.
	out, err := exec.Command("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o").Output()
	if err != nil {
		return nil, "", nil // clipboard may be empty or inaccessible
	}

	targets := strings.Split(string(out), "\n")
	hasImage := false
	for _, t := range targets {
		if strings.TrimSpace(t) == "image/png" {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return nil, "", nil // no image in clipboard
	}

	// Extract PNG data.
	var buf bytes.Buffer
	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("clipboard extract failed: %w", err)
	}
	data := buf.Bytes()
	if len(data) == 0 {
		return nil, "", nil
	}
	return data, "image/png", nil
}
