package cliputil

import (
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
	script := strings.Join([]string{
		"[Console]::OutputEncoding = [System.Text.Encoding]::UTF8",
		"$text = Get-Clipboard -Raw",
		"if ($null -ne $text) { [Console]::Out.Write($text) }",
	}, "; ")
	out, err := exec.Command(powershellPath, "-NoProfile", "-Command", script).Output()
	if err != nil {
		return "", fmt.Errorf("windows clipboard read failed: %w", err)
	}
	return string(out), nil
}

func writeWSLText(text string) error {
	if clipPath, err := lookPathAny("clip.exe"); err == nil {
		cmd := exec.Command(clipPath)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("windows clipboard write failed: %w", err)
		}
		return nil
	}

	powershellPath, err := lookPathAny("powershell.exe", "pwsh.exe", "pwsh", "powershell")
	if err != nil {
		return fmt.Errorf("windows clipboard writer unavailable in WSL: %w", err)
	}
	script := strings.Join([]string{
		"[Console]::InputEncoding = [System.Text.Encoding]::UTF8",
		"$text = [Console]::In.ReadToEnd()",
		"Set-Clipboard -Value $text",
	}, "; ")
	cmd := exec.Command(powershellPath, "-NoProfile", "-Command", script)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("windows clipboard write failed: %w", err)
	}
	return nil
}

func lookPathAny(names ...string) (string, error) {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}
