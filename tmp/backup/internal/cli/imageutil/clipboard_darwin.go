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
	candidates := []struct {
		classCode string
		ext       string
		mime      string
	}{
		{classCode: "PNGf", ext: ".png", mime: "image/png"},
		{classCode: "TIFF", ext: ".tiff", mime: "image/tiff"},
	}
	for _, candidate := range candidates {
		data, mime, ok, err := extractClipboardImageCandidate(candidate.classCode, candidate.ext, candidate.mime)
		if err != nil {
			return nil, "", err
		}
		if ok {
			return data, mime, nil
		}
	}
	return nil, "", nil
}

func extractClipboardImageCandidate(classCode string, ext string, mime string) ([]byte, string, bool, error) {
	checkScript := fmt.Sprintf(`try
	the clipboard as «class %s»
	return "has_image"
on error
	return ""
end try`, classCode)
	out, err := exec.Command("osascript", "-e", checkScript).Output()
	if err != nil {
		return nil, "", false, fmt.Errorf("clipboard check failed: %w", err)
	}
	if strings.TrimSpace(string(out)) != "has_image" {
		return nil, "", false, nil
	}

	// Write clipboard PNG to a temporary file.
	tmpDir, err := os.MkdirTemp("", "caelis-clipboard-*")
	if err != nil {
		return nil, "", false, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, "clipboard"+ext)

	writeScript := fmt.Sprintf(`try
	set theData to the clipboard as «class %s»
	set theFile to POSIX file %q
	set theRef to open for access theFile with write permission
	write theData to theRef
	close access theRef
	return "ok"
on error errMsg
	return "error: " & errMsg
end try`, classCode, tmpFile)

	out, err = exec.Command("osascript", "-e", writeScript).Output()
	if err != nil {
		return nil, "", false, fmt.Errorf("clipboard extract failed: %w", err)
	}
	result := strings.TrimSpace(string(out))
	if strings.HasPrefix(result, "error:") {
		return nil, "", false, fmt.Errorf("clipboard extract: %s", result)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return nil, "", false, fmt.Errorf("read clipboard image: %w", err)
	}
	if len(data) == 0 {
		return nil, "", false, nil
	}
	return data, mime, true, nil
}
