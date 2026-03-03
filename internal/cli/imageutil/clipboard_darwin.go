//go:build darwin

package imageutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExtractClipboardImage checks the macOS clipboard for image data.
// Returns (data, mimeType, nil) on success, (nil, "", nil) if no image,
// or (nil, "", err) on failure.
func ExtractClipboardImage() ([]byte, string, error) {
	// Check if clipboard contains image data by querying for PNG class.
	checkScript := `try
	the clipboard as «class PNGf»
	return "has_image"
on error
	return ""
end try`
	out, err := exec.Command("osascript", "-e", checkScript).Output()
	if err != nil {
		return nil, "", fmt.Errorf("clipboard check failed: %w", err)
	}
	if strings.TrimSpace(string(out)) != "has_image" {
		return nil, "", nil // no image in clipboard
	}

	// Write clipboard PNG to a temporary file.
	tmpDir, err := os.MkdirTemp("", "caelis-clipboard-*")
	if err != nil {
		return nil, "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, "clipboard.png")

	writeScript := fmt.Sprintf(`try
	set theData to the clipboard as «class PNGf»
	set theFile to POSIX file %q
	set theRef to open for access theFile with write permission
	write theData to theRef
	close access theRef
	return "ok"
on error errMsg
	return "error: " & errMsg
end try`, tmpFile)

	out, err = exec.Command("osascript", "-e", writeScript).Output()
	if err != nil {
		return nil, "", fmt.Errorf("clipboard extract failed: %w", err)
	}
	result := strings.TrimSpace(string(out))
	if strings.HasPrefix(result, "error:") {
		return nil, "", fmt.Errorf("clipboard extract: %s", result)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return nil, "", fmt.Errorf("read clipboard image: %w", err)
	}
	if len(data) == 0 {
		return nil, "", nil
	}
	return data, "image/png", nil
}
