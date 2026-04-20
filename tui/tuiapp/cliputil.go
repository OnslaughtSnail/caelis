package tuiapp

// cliputil.go transplants WSL detection and clipboard utilities from the
// legacy internal/cli/cliputil package.

import (
	"os"
	"os/exec"
	"strings"
)

func isWSL() bool {
	if strings.Contains(strings.ToLower(os.Getenv("WSL_DISTRO_NAME")), "wsl") {
		return true
	}
	if strings.Contains(strings.ToLower(os.Getenv("WSL_INTEROP")), "wsl") {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	if err == nil && strings.Contains(strings.ToLower(string(data)), "microsoft") {
		return true
	}
	return false
}

func defaultReadClipboardText() (string, error) {
	if isWSL() {
		return wslReadText()
	}
	return nativeReadText()
}

func defaultWriteClipboardText(text string) error {
	if isWSL() {
		return wslWriteText(text)
	}
	return nativeWriteText(text)
}

func nativeReadText() (string, error) {
	cmd := exec.Command("pbpaste")
	out, err := cmd.Output()
	if err != nil {
		// Fall back to xclip/xsel on Linux
		cmd = exec.Command("xclip", "-selection", "clipboard", "-o")
		out, err = cmd.Output()
		if err != nil {
			cmd = exec.Command("xsel", "--clipboard", "--output")
			out, err = cmd.Output()
		}
	}
	return strings.TrimRight(string(out), "\n"), err
}

func nativeWriteText(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	err := cmd.Run()
	if err != nil {
		cmd = exec.Command("xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
		err = cmd.Run()
	}
	return err
}

func wslReadText() (string, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", "Get-Clipboard")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

func wslWriteText(text string) error {
	cmd := exec.Command("clip.exe")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
