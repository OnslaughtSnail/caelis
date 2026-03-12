//go:build linux

package imageutil

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ExtractClipboardImage checks the Linux clipboard for image data.
// It supports Wayland (`wl-paste`), X11 (`xclip`), and WSL via
// `powershell.exe`/`pwsh.exe`.
// Returns (data, mimeType, nil) on success, (nil, "", nil) if no image,
// or (nil, "", err) on failure.
func ExtractClipboardImage() ([]byte, string, error) {
	extractors := []func() ([]byte, string, bool, bool, error){
		extractWSLClipboardImage,
		extractWaylandClipboardImage,
		extractXclipClipboardImage,
	}

	available := false
	for _, extractor := range extractors {
		data, mime, supported, ok, err := extractor()
		if supported {
			available = true
		}
		if err != nil {
			return nil, "", err
		}
		if ok {
			return data, mime, nil
		}
	}
	if available {
		return nil, "", nil
	}
	return nil, "", fmt.Errorf("clipboard image extraction requires wl-paste, xclip, or powershell.exe in WSL")
}

func extractWaylandClipboardImage() ([]byte, string, bool, bool, error) {
	if _, err := exec.LookPath("wl-paste"); err != nil {
		return nil, "", false, false, nil
	}
	out, err := exec.Command("wl-paste", "--list-types").Output()
	if err != nil {
		return nil, "", true, false, nil
	}
	mime := chooseClipboardImageMime(strings.Split(string(out), "\n"))
	if mime == "" {
		return nil, "", true, false, nil
	}
	var buf bytes.Buffer
	cmd := exec.Command("wl-paste", "--type", mime)
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return nil, "", true, false, fmt.Errorf("clipboard extract failed: %w", err)
	}
	data := buf.Bytes()
	if len(data) == 0 {
		return nil, "", true, false, nil
	}
	return data, mime, true, true, nil
}

func extractXclipClipboardImage() ([]byte, string, bool, bool, error) {
	if _, err := exec.LookPath("xclip"); err != nil {
		return nil, "", false, false, nil
	}
	out, err := exec.Command("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o").Output()
	if err != nil {
		return nil, "", true, false, nil
	}
	mime := chooseClipboardImageMime(strings.Split(string(out), "\n"))
	if mime == "" {
		return nil, "", true, false, nil
	}
	var buf bytes.Buffer
	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", mime, "-o")
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return nil, "", true, false, fmt.Errorf("clipboard extract failed: %w", err)
	}
	data := buf.Bytes()
	if len(data) == 0 {
		return nil, "", true, false, nil
	}
	return data, mime, true, true, nil
}

func extractWSLClipboardImage() ([]byte, string, bool, bool, error) {
	if !runningInWSL() {
		return nil, "", false, false, nil
	}
	powershellPath, err := lookPathAny("powershell.exe", "pwsh.exe")
	if err != nil {
		return nil, "", false, false, nil
	}
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"Add-Type -AssemblyName System.Drawing",
		"if (-not [System.Windows.Forms.Clipboard]::ContainsImage()) { return }",
		"$img = [System.Windows.Forms.Clipboard]::GetImage()",
		"$ms = New-Object System.IO.MemoryStream",
		"$img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)",
		"[Console]::Out.Write([Convert]::ToBase64String($ms.ToArray()))",
	}, "; ")
	out, err := exec.Command(powershellPath, "-NoProfile", "-STA", "-Command", script).Output()
	if err != nil {
		return nil, "", true, false, fmt.Errorf("clipboard extract failed: %w", err)
	}
	encoded := strings.TrimSpace(string(out))
	if encoded == "" {
		return nil, "", true, false, nil
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", true, false, fmt.Errorf("clipboard decode failed: %w", err)
	}
	if len(data) == 0 {
		return nil, "", true, false, nil
	}
	return data, "image/png", true, true, nil
}

func chooseClipboardImageMime(targets []string) string {
	priority := []string{
		"image/png",
		"image/jpeg",
		"image/webp",
		"image/gif",
		"image/tiff",
	}
	available := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		target = strings.ToLower(strings.TrimSpace(target))
		if target == "" {
			continue
		}
		available[target] = struct{}{}
	}
	for _, mime := range priority {
		if _, ok := available[mime]; ok {
			return mime
		}
	}
	return ""
}

func runningInWSL() bool {
	if strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME")) != "" || strings.TrimSpace(os.Getenv("WSL_INTEROP")) != "" {
		return true
	}
	version, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	text := strings.ToLower(string(version))
	return strings.Contains(text, "microsoft") || strings.Contains(text, "wsl")
}

func lookPathAny(names ...string) (string, error) {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}
