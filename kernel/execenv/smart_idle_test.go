package execenv

import (
	"testing"
	"time"
)

func TestSmartIdleDetector_ShellPrompts(t *testing.T) {
	detector := NewSmartIdleDetector()

	tests := []struct {
		name     string
		output   string
		expected bool
	}{
		{"bash prompt", "user@host:~$ ", true},
		{"zsh prompt", "user@host ~ % ", true},
		{"python repl", ">>> ", true},
		{"ipython", "In [1]: ", true},
		{"node repl", "> ", true},
		{"password prompt", "Password: ", true},
		{"yes/no prompt", "Are you sure? (yes/no): ", true},
		{"confirmation", "Continue? [Y/n] ", true},
		{"ssh key", "Are you sure you want to continue connecting (yes/no)? ", true},
		{"normal output", "Building project...", false},
		{"empty", "", false},
		{"progress", "Processing 50%...", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.Analyze([]byte(tt.output), 0, 30*time.Second)
			if result.IsLikelyWaitingForInput != tt.expected {
				t.Errorf("For %q: expected IsLikelyWaitingForInput=%v, got %v (reason: %s)",
					tt.output, tt.expected, result.IsLikelyWaitingForInput, result.Reason)
			}
		})
	}
}

func TestSmartIdleDetector_WaitingIndicators(t *testing.T) {
	detector := NewSmartIdleDetector()

	tests := []struct {
		name     string
		output   string
		expected bool
	}{
		{"waiting for input", "Waiting for user input...", true},
		{"press enter", "Press Enter to continue", true},
		{"type yes", "Type 'yes' to confirm", true},
		{"normal log", "INFO: Server started on port 8080", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.Analyze([]byte(tt.output), 0, 30*time.Second)
			if result.IsLikelyWaitingForInput != tt.expected {
				t.Errorf("For %q: expected IsLikelyWaitingForInput=%v, got %v",
					tt.output, tt.expected, result.IsLikelyWaitingForInput)
			}
		})
	}
}

func TestSmartIdleDetector_CustomPattern(t *testing.T) {
	detector := NewSmartIdleDetector()

	// Add custom pattern
	err := detector.AddPromptPattern(`^myapp>\s*$`)
	if err != nil {
		t.Fatalf("AddPromptPattern failed: %v", err)
	}

	result := detector.Analyze([]byte("myapp> "), 0, 30*time.Second)
	if !result.IsLikelyWaitingForInput {
		t.Error("Custom pattern should match")
	}
}

func TestSmartIdleDetector_MultilineOutput(t *testing.T) {
	detector := NewSmartIdleDetector()

	output := `Building project...
Compiling module 1/5
Compiling module 2/5
Compiling module 3/5
>>> `

	result := detector.Analyze([]byte(output), 0, 30*time.Second)
	if !result.IsLikelyWaitingForInput {
		t.Error("Should detect Python prompt at end of multiline output")
	}
}

func TestDefaultSmartIdleConfig(t *testing.T) {
	cfg := DefaultSmartIdleConfig()

	if !cfg.Enabled {
		t.Error("Smart idle should be enabled by default")
	}
	if cfg.MinIdleDuration != 10*time.Second {
		t.Errorf("Expected MinIdleDuration=10s, got %v", cfg.MinIdleDuration)
	}
	if cfg.CheckInterval != 1*time.Second {
		t.Errorf("Expected CheckInterval=1s, got %v", cfg.CheckInterval)
	}
	if cfg.FallbackTimeout != 5*time.Minute {
		t.Errorf("Expected FallbackTimeout=5m, got %v", cfg.FallbackTimeout)
	}
}
