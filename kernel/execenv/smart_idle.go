package execenv

import (
	"bytes"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// IdleDetectionResult contains the result of idle detection analysis.
type IdleDetectionResult struct {
	IsLikelyWaitingForInput bool
	Confidence              float64 // 0.0 to 1.0
	Reason                  string
	PromptPattern           string // The detected prompt pattern, if any
}

// SmartIdleDetector provides intelligent detection of whether a process
// is waiting for user input vs performing a long-running computation.
type SmartIdleDetector struct {
	promptPatterns []*regexp.Regexp
	lastOutput     []byte
	lastCheck      time.Time
}

// Common shell prompt patterns
var defaultPromptPatterns = []*regexp.Regexp{
	// Standard shell prompts
	regexp.MustCompile(`[$#>]\s*$`),                      // $ or # or > at end
	regexp.MustCompile(`^\s*\$\s*$`),                     // Just $
	regexp.MustCompile(`^.*@.*[:~].*[$#]\s*$`),           // user@host:~$
	regexp.MustCompile(`^\[.*\]\s*[$#>]\s*$`),            // [user@host dir]$
	regexp.MustCompile(`^.*\s*%\s*$`),                    // zsh style %
	regexp.MustCompile(`^\(.*\)\s*.*[$#>]\s*$`),          // (venv) user$
	regexp.MustCompile(`^PS\d*>\s*$`),                    // PowerShell PS1>
	regexp.MustCompile(`^>>>\s*$`),                       // Python REPL
	regexp.MustCompile(`^\.{3}\s*$`),                     // Python continuation ...
	regexp.MustCompile(`^In\s*\[\d+\]:\s*$`),             // IPython
	regexp.MustCompile(`^irb.*>\s*$`),                    // Ruby IRB
	regexp.MustCompile(`^>\s*$`),                         // Node.js REPL
	regexp.MustCompile(`^\?\s*$`),                        // R prompt
	regexp.MustCompile(`^sqlite>\s*$`),                   // SQLite
	regexp.MustCompile(`^mysql>\s*$`),                    // MySQL
	regexp.MustCompile(`^postgres.*[=#]>\s*$`),           // PostgreSQL
	regexp.MustCompile(`^=>>\s*$`),                       // Elixir iex
	regexp.MustCompile(`^ghci>\s*$`),                     // Haskell GHCi
	regexp.MustCompile(`^scala>\s*$`),                    // Scala REPL
	regexp.MustCompile(`^groovy:\d+>\s*$`),               // Groovy
	regexp.MustCompile(`^gdb>\s*$`),                      // GDB debugger
	regexp.MustCompile(`^\(Pdb\)\s*$`),                   // Python debugger
	regexp.MustCompile(`^\(gdb\)\s*$`),                   // GDB
	regexp.MustCompile(`^debug>\s*$`),                    // Node debugger
	regexp.MustCompile(`^.*\(yes/no.*\)\s*[?:]\s*$`),     // SSH host key confirmation
	regexp.MustCompile(`^Password:\s*$`),                 // Password prompt
	regexp.MustCompile(`^Enter passphrase.*:\s*$`),       // SSH passphrase
	regexp.MustCompile(`^\[Y/n\]\s*$`),                   // apt-get style
	regexp.MustCompile(`^\[y/N\]\s*$`),                   // Confirmation prompts
	regexp.MustCompile(`^Continue\?.*$`),                 // Various continue prompts
	regexp.MustCompile(`^Press.*to continue.*$`),         // Press key prompts
	regexp.MustCompile(`^--More--\s*$`),                  // Pager
	regexp.MustCompile(`^:\s*$`),                         // less/more pager
	regexp.MustCompile(`^Do you want to continue.*$`),    // Various continue prompts
	regexp.MustCompile(`^Proceed\?.*$`),                  // Proceed prompts
	regexp.MustCompile(`^Are you sure.*\?.*$`),           // Confirmation
	regexp.MustCompile(`^Enter.*:\s*$`),                  // Input prompts
	regexp.MustCompile(`^Type.*:\s*$`),                   // Type prompts
	regexp.MustCompile(`^Input.*:\s*$`),                  // Input prompts
	regexp.MustCompile(`^Please enter.*:\s*$`),           // Please enter prompts
	regexp.MustCompile(`^Overwrite.*\?.*$`),              // Overwrite confirmation
	regexp.MustCompile(`^Replace.*\?.*$`),                // Replace confirmation
	regexp.MustCompile(`^Delete.*\?.*$`),                 // Delete confirmation
}

// NewSmartIdleDetector creates a new smart idle detector.
func NewSmartIdleDetector() *SmartIdleDetector {
	return &SmartIdleDetector{
		promptPatterns: defaultPromptPatterns,
	}
}

// AddPromptPattern adds a custom prompt pattern.
func (d *SmartIdleDetector) AddPromptPattern(pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	d.promptPatterns = append(d.promptPatterns, re)
	return nil
}

// Analyze checks if the process appears to be waiting for input.
func (d *SmartIdleDetector) Analyze(output []byte, pid int, idleDuration time.Duration) IdleDetectionResult {
	result := IdleDetectionResult{}

	// Check for prompt patterns in the last line of output
	if promptMatch, pattern := d.checkPromptPatterns(output); promptMatch {
		result.IsLikelyWaitingForInput = true
		result.Confidence = 0.9
		result.Reason = "detected interactive prompt pattern"
		result.PromptPattern = pattern
		return result
	}

	// Check process state on Linux (lower confidence since it's heuristic)
	if runtime.GOOS == "linux" && pid > 0 {
		if isBlocked, reason := d.checkProcessState(pid); isBlocked {
			result.IsLikelyWaitingForInput = true
			result.Confidence = 0.8
			result.Reason = reason
			return result
		}
	}

	// Check for common "waiting" indicators in output.
	// Confidence is intentionally below the kill threshold (0.7 in host.go)
	// because these text patterns can appear in normal program output
	// (e.g. "Waiting for connections").  They inform but never trigger a kill
	// on their own.
	if waitIndicator := d.checkWaitingIndicators(output); waitIndicator != "" {
		result.IsLikelyWaitingForInput = true
		result.Confidence = 0.6
		result.Reason = waitIndicator
		return result
	}

	// If idle for a long time with no clear indicators, moderate confidence
	if idleDuration > 30*time.Second {
		result.IsLikelyWaitingForInput = false
		result.Confidence = 0.5
		result.Reason = "long idle period but no clear prompt pattern"
	}

	return result
}

func (d *SmartIdleDetector) checkPromptPatterns(output []byte) (bool, string) {
	if len(output) == 0 {
		return false, ""
	}

	// Get the last line (or last few lines for multi-line prompts)
	lines := bytes.Split(output, []byte("\n"))
	var lastLines []byte
	
	// Check last 3 non-empty lines
	count := 0
	for i := len(lines) - 1; i >= 0 && count < 3; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) > 0 {
			if len(lastLines) > 0 {
				lastLines = append([]byte("\n"), lastLines...)
			}
			lastLines = append(line, lastLines...)
			count++
		}
	}

	if len(lastLines) == 0 {
		return false, ""
	}

	lastLineStr := string(lastLines)
	for _, pattern := range d.promptPatterns {
		if pattern.MatchString(lastLineStr) {
			return true, pattern.String()
		}
	}

	return false, ""
}

func (d *SmartIdleDetector) checkProcessState(pid int) (bool, string) {
	// Read /proc/[pid]/stat to check process state
	statPath := "/proc/" + strconv.Itoa(pid) + "/stat"
	data, err := os.ReadFile(statPath)
	if err != nil {
		return false, ""
	}

	// Parse stat file: fields are space-separated, but comm field can contain spaces
	// Format: pid (comm) state ppid pgrp session tty_nr tpgid ...
	statStr := string(data)
	
	// Find the closing parenthesis of comm field
	commEnd := strings.LastIndex(statStr, ")")
	if commEnd < 0 || commEnd+2 >= len(statStr) {
		return false, ""
	}

	// State is the first field after ")"
	fields := strings.Fields(statStr[commEnd+2:])
	if len(fields) < 1 {
		return false, ""
	}

	state := fields[0]
	// Only consider stopped (T) processes as definitively waiting.
	// Sleeping (S) is too broad – compilers, network tools, sleep all use it.
	if state == "T" {
		return true, "process is stopped (state T)"
	}

	// Check /proc/[pid]/wchan for wait channels that specifically indicate
	// the process is blocked reading from a terminal (tty_read) or pipe
	// connected to stdin.  We intentionally exclude generic channels like
	// "poll_schedule", "pipe_read", and "wait_woken" because they match
	// normal programs doing network I/O, subprocess management, etc.
	wchanPath := "/proc/" + strconv.Itoa(pid) + "/wchan"
	wchanData, err := os.ReadFile(wchanPath)
	if err == nil {
		wchan := strings.TrimSpace(string(wchanData))
		if wchan == "tty_read" || wchan == "read_chan" {
			return true, "process is blocked reading terminal (wchan: " + wchan + ")"
		}
	}

	return false, ""
}

func (d *SmartIdleDetector) checkWaitingIndicators(output []byte) string {
	if len(output) == 0 {
		return ""
	}

	// Get the last 500 bytes for analysis
	lastPart := output
	if len(output) > 500 {
		lastPart = output[len(output)-500:]
	}

	lower := bytes.ToLower(lastPart)

	// Check for common waiting indicators
	indicators := []struct {
		pattern string
		reason  string
	}{
		{"waiting for", "output indicates waiting state"},
		{"press any key", "waiting for key press"},
		{"press enter", "waiting for enter key"},
		{"hit enter", "waiting for enter key"},
		{"type 'yes'", "waiting for confirmation input"},
		{"enter password", "waiting for password"},
		{"authentication required", "waiting for authentication"},
		{"login:", "waiting for login"},
		{"username:", "waiting for username"},
		{"passphrase", "waiting for passphrase"},
		{"confirm", "waiting for confirmation"},
		{"(y/n)", "waiting for yes/no input"},
		{"[y/n]", "waiting for yes/no input"},
		{"continue?", "waiting for continue confirmation"},
		{"proceed?", "waiting for proceed confirmation"},
	}

	for _, ind := range indicators {
		if bytes.Contains(lower, []byte(ind.pattern)) {
			return ind.reason
		}
	}

	return ""
}

// SmartIdleConfig configures smart idle detection.
type SmartIdleConfig struct {
	Enabled           bool          // Whether to use smart detection
	MinIdleDuration   time.Duration // Minimum idle time before analysis
	CheckInterval     time.Duration // How often to check
	FallbackTimeout   time.Duration // Maximum time before forced termination
}

// DefaultSmartIdleConfig returns default smart idle configuration.
func DefaultSmartIdleConfig() SmartIdleConfig {
	return SmartIdleConfig{
		Enabled:         true,
		MinIdleDuration: 10 * time.Second,
		CheckInterval:   1 * time.Second,
		FallbackTimeout: 5 * time.Minute,
	}
}
