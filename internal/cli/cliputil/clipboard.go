package cliputil

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/atotto/clipboard"
)

// IsWSL reports whether the current Linux process is running under WSL.
func IsWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
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

// ReadText reads text from the system clipboard.
func ReadText() (string, error) {
	if IsWSL() {
		return readWSLText()
	}
	return clipboard.ReadAll()
}

// WriteText writes text into the system clipboard.
func WriteText(text string) error {
	if IsWSL() {
		return writeWSLText(text)
	}
	return clipboard.WriteAll(text)
}

func readWSLText() (string, error) {
	powershellPath, err := lookPathAny("powershell.exe", "pwsh.exe", "pwsh", "powershell")
	if err != nil {
		return "", fmt.Errorf("windows clipboard reader unavailable in WSL: %w", err)
	}
	out, err := exec.Command(powershellPath, "-NoProfile", "-Command", wslClipboardReadScript()).Output()
	if err != nil {
		return "", fmt.Errorf("windows clipboard read failed: %w", err)
	}
	return decodeWSLClipboardText(out)
}

func writeWSLText(text string) error {
	powershellPath, err := lookPathAny("powershell.exe", "pwsh.exe", "pwsh", "powershell")
	if err != nil {
		return fmt.Errorf("windows clipboard writer unavailable in WSL: %w", err)
	}
	cmd := exec.Command(powershellPath, "-NoProfile", "-Command", wslClipboardWriteScript())
	cmd.Stdin = strings.NewReader(encodeWSLClipboardText(text))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("windows clipboard write failed: %w", err)
	}
	return nil
}

func wslClipboardReadScript() string {
	return strings.Join([]string{
		"[Console]::OutputEncoding = [System.Text.Encoding]::ASCII",
		"$text = Get-Clipboard -Raw",
		"if ($null -eq $text) { return }",
		"$bytes = [System.Text.Encoding]::UTF8.GetBytes($text)",
		"[Console]::Out.Write([Convert]::ToBase64String($bytes))",
	}, "; ")
}

func wslClipboardWriteScript() string {
	return strings.Join([]string{
		"[Console]::InputEncoding = [System.Text.Encoding]::ASCII",
		"$base64 = [Console]::In.ReadToEnd()",
		"if ([string]::IsNullOrWhiteSpace($base64)) { $text = '' } else { $bytes = [Convert]::FromBase64String($base64); $text = [System.Text.Encoding]::UTF8.GetString($bytes) }",
		"Set-Clipboard -Value $text",
	}, "; ")
}

func encodeWSLClipboardText(text string) string {
	return base64.StdEncoding.EncodeToString([]byte(text))
}

func decodeWSLClipboardText(out []byte) (string, error) {
	encoded := strings.TrimSpace(string(out))
	if encoded == "" {
		return "", nil
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("windows clipboard decode failed: %w", err)
	}
	return string(decoded), nil
}

func lookPathAny(names ...string) (string, error) {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}
