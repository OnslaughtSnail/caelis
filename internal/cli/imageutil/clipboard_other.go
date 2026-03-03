//go:build !darwin && !linux

package imageutil

import "fmt"

// ExtractClipboardImage is not supported on this platform.
func ExtractClipboardImage() ([]byte, string, error) {
	return nil, "", fmt.Errorf("clipboard image extraction not supported on this platform")
}
