package cmdsafety

import (
	"fmt"
	"path/filepath"
	"strings"
)

// DetectBlockedCommand returns (rule, reason) when the shell command matches
// one high-confidence dangerous pattern that should be blocked preflight.
func DetectBlockedCommand(command string) (string, string) {
	if name, reason := detectInlinePattern(command); name != "" {
		return name, reason
	}
	return detectSegmentedPattern(command, 0)
}

func IsDangerousCommand(command string) bool {
	name, _ := DetectBlockedCommand(command)
	return name != ""
}

func detectInlinePattern(command string) (string, string) {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return "", ""
	}
	compact := compactShellText(lower)
	switch {
	case strings.Contains(compact, ":(){"):
		return "fork_bomb", "fork bomb is blocked by preflight safety policy"
	case strings.Contains(compact, "yes>/dev/null"):
		return "yes", "unbounded output flood is blocked by preflight safety policy"
	case strings.Contains(compact, "/dev/tcp/"):
		return "dev_tcp", "reverse shell /dev/tcp pattern is blocked by preflight safety policy"
	}
	if looksLikePipeToShell(compact) {
		return "remote_shell", "download-and-execute pipeline is blocked by preflight safety policy"
	}
	if looksLikeProcessSubstitutionFetch(compact) {
		return "remote_shell", "download-and-execute process substitution is blocked by preflight safety policy"
	}
	return "", ""
}

func detectSegmentedPattern(command string, depth int) (string, string) {
	if depth > 4 {
		return "", ""
	}
	for _, segment := range shellCommandSegments(command) {
		if name, reason := detectDangerousSegment(shellSegmentTokens(segment), depth); name != "" {
			return name, reason
		}
	}
	return "", ""
}

func detectDangerousSegment(tokens []string, depth int) (string, string) {
	if len(tokens) == 0 {
		return "", ""
	}
	start := firstExecutableIndex(tokens)
	if start >= len(tokens) {
		return "", ""
	}
	base := filepath.Base(strings.ToLower(tokens[start]))
	args := tokens[start+1:]

	switch base {
	case "env", "command", "builtin", "nohup", "time":
		return detectDangerousSegment(unwrapWrapperArgs(base, args), depth+1)
	case "sudo":
		unwrapped := unwrapWrapperArgs(base, args)
		if next := nextExecutable(unwrapped); isInteractiveRootShell(next) {
			return next, fmt.Sprintf("%s root shell escalation is blocked by preflight safety policy", next)
		}
		return detectDangerousSegment(unwrapped, depth+1)
	case "sh", "bash", "zsh", "dash", "ksh", "ash", "fish":
		if nested, ok := wrappedShellCommand(args); ok {
			return detectSegmentedPattern(nested, depth+1)
		}
		if strings.Contains(strings.ToLower(strings.Join(args, " ")), "<(") {
			return base, "process substitution shell execution is blocked by preflight safety policy"
		}
	case "su":
		user := strings.ToLower(strings.TrimSpace(nextExecutable(args)))
		if user == "" || user == "root" {
			return "su", "root shell escalation is blocked by preflight safety policy"
		}
	case "setcap":
		return "setcap", "capability escalation is blocked by preflight safety policy"
	case "wipefs", "shred", "reboot", "shutdown", "halt", "poweroff":
		return base, fmt.Sprintf("%s is blocked by preflight safety policy", base)
	case "mkfs", "mkfs.ext4", "mkfs.xfs", "mkfs.fat", "mke2fs":
		return "mkfs", "filesystem formatting is blocked by preflight safety policy"
	case "dd":
		if hasArgPrefix(args, "of=/dev/") {
			return "dd", "dd writing to block devices is blocked by preflight safety policy"
		}
	case "rm":
		if hasRecursiveFlag(args) && hasDangerousDeleteTarget(filterNonFlagArgs(args)) {
			return "rm", "recursive delete of critical paths is blocked by preflight safety policy"
		}
	case "rmdir":
		if hasDangerousDeleteTarget(filterNonFlagArgs(args)) {
			return "rmdir", "directory delete of critical paths is blocked by preflight safety policy"
		}
	case "find":
		if hasExactArg(args, "-delete") && hasDangerousFindTarget(args) {
			return "find", "find -delete on critical paths is blocked by preflight safety policy"
		}
	case "chmod":
		if hasRecursiveFlag(args) && hasDangerousOwnershipTarget(filterNonFlagArgs(args), 1) {
			return "chmod", "recursive permission changes on critical paths are blocked by preflight safety policy"
		}
	case "chown":
		if hasRecursiveFlag(args) && hasDangerousOwnershipTarget(filterNonFlagArgs(args), 1) {
			return "chown", "recursive ownership changes on critical paths are blocked by preflight safety policy"
		}
	case "init":
		mode := strings.TrimSpace(nextExecutable(args))
		if mode == "0" || mode == "6" {
			return "init", "system shutdown/reboot is blocked by preflight safety policy"
		}
	case "kill":
		if containsExactArg(args, "-9") && containsExactArg(args, "1") {
			return "kill", "killing pid 1 is blocked by preflight safety policy"
		}
	case "nc", "netcat", "ncat":
		if containsNetcatExecArg(args) {
			return base, "reverse shell patterns are blocked by preflight safety policy"
		}
	case "git":
		if len(args) > 0 {
			sub := strings.ToLower(strings.TrimSpace(args[0]))
			switch sub {
			case "clean":
				if gitCleanIsDangerous(args[1:]) {
					return "git clean", "destructive git clean is blocked by preflight safety policy"
				}
			case "reset":
				if containsExactArg(args[1:], "--hard") {
					return "git reset", "git reset --hard is blocked by preflight safety policy"
				}
			case "push":
				if containsExactArg(args[1:], "--force") || containsExactArg(args[1:], "-f") || containsPrefixArg(args[1:], "--force-with-lease") {
					return "git push", "force push is blocked by preflight safety policy"
				}
			}
		}
	}
	return "", ""
}

func looksLikePipeToShell(compact string) bool {
	return (strings.Contains(compact, "curl") || strings.Contains(compact, "wget")) &&
		(strings.Contains(compact, "|bash") || strings.Contains(compact, "|sh"))
}

func looksLikeProcessSubstitutionFetch(compact string) bool {
	return (strings.Contains(compact, "bash<(") || strings.Contains(compact, "source<(")) &&
		(strings.Contains(compact, "curl") || strings.Contains(compact, "wget"))
}

func compactShellText(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), "")
}

func firstExecutableIndex(tokens []string) int {
	for i, token := range tokens {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "=") && !strings.HasPrefix(trimmed, "=") {
			continue
		}
		return i
	}
	return len(tokens)
}

func nextExecutable(tokens []string) string {
	for _, token := range tokens {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			continue
		}
		return trimmed
	}
	return ""
}

func unwrapWrapperArgs(base string, args []string) []string {
	switch strings.ToLower(strings.TrimSpace(base)) {
	case "env":
		return unwrapEnvArgs(args)
	case "sudo":
		return unwrapSudoArgs(args)
	case "time":
		return unwrapTimeArgs(args)
	case "command":
		return unwrapCommandArgs(args)
	case "builtin":
		return unwrapBuiltinArgs(args)
	case "nohup":
		return unwrapNoHupArgs(args)
	default:
		return args
	}
}

func unwrapEnvArgs(args []string) []string {
	for i := 0; i < len(args); {
		token := strings.TrimSpace(args[i])
		if token == "" {
			i++
			continue
		}
		switch {
		case token == "--":
			if i+1 < len(args) {
				return args[i+1:]
			}
			return nil
		case strings.Contains(token, "=") && !strings.HasPrefix(token, "="):
			i++
		case token == "-u" || token == "--unset" || token == "-C" || token == "--chdir" || token == "--argv0" || token == "-S" || token == "--split-string":
			if i+1 >= len(args) {
				return nil
			}
			i += 2
		case strings.HasPrefix(token, "--unset=") || strings.HasPrefix(token, "--chdir=") || strings.HasPrefix(token, "--argv0="):
			i++
		case token == "-i" || token == "--ignore-environment" || token == "-0" || token == "--null":
			i++
		case strings.HasPrefix(token, "-u") && token != "-u":
			i++
		case strings.HasPrefix(token, "-"):
			i++
		default:
			return args[i:]
		}
	}
	return nil
}

func unwrapSudoArgs(args []string) []string {
	for i := 0; i < len(args); {
		token := strings.TrimSpace(args[i])
		if token == "" {
			i++
			continue
		}
		switch {
		case token == "--":
			if i+1 < len(args) {
				return args[i+1:]
			}
			return nil
		case token == "-u" || token == "-g" || token == "-h" || token == "-p" || token == "-r" || token == "-t" || token == "-C" || token == "-T" || token == "-D" || token == "-R" ||
			token == "--user" || token == "--group" || token == "--host" || token == "--prompt" || token == "--role" || token == "--type" || token == "--close-from" || token == "--chdir" || token == "--chroot":
			if i+1 >= len(args) {
				return nil
			}
			i += 2
		case strings.HasPrefix(token, "--user=") || strings.HasPrefix(token, "--group=") || strings.HasPrefix(token, "--host=") || strings.HasPrefix(token, "--prompt=") ||
			strings.HasPrefix(token, "--role=") || strings.HasPrefix(token, "--type=") || strings.HasPrefix(token, "--close-from=") || strings.HasPrefix(token, "--chdir=") || strings.HasPrefix(token, "--chroot="):
			i++
		case strings.HasPrefix(token, "-u") && token != "-u",
			strings.HasPrefix(token, "-g") && token != "-g",
			strings.HasPrefix(token, "-h") && token != "-h",
			strings.HasPrefix(token, "-p") && token != "-p",
			strings.HasPrefix(token, "-r") && token != "-r",
			strings.HasPrefix(token, "-t") && token != "-t",
			strings.HasPrefix(token, "-C") && token != "-C",
			strings.HasPrefix(token, "-T") && token != "-T",
			strings.HasPrefix(token, "-D") && token != "-D",
			strings.HasPrefix(token, "-R") && token != "-R":
			i++
		case strings.HasPrefix(token, "-"):
			i++
		default:
			return args[i:]
		}
	}
	return nil
}

func unwrapTimeArgs(args []string) []string {
	for i := 0; i < len(args); i++ {
		token := strings.TrimSpace(args[i])
		if token == "" {
			continue
		}
		if token == "--" {
			if i+1 < len(args) {
				return args[i+1:]
			}
			return nil
		}
		if strings.HasPrefix(token, "-") {
			continue
		}
		return args[i:]
	}
	return nil
}

func unwrapCommandArgs(args []string) []string {
	for i := 0; i < len(args); i++ {
		token := strings.TrimSpace(args[i])
		if token == "" {
			continue
		}
		if token == "--" {
			if i+1 < len(args) {
				return args[i+1:]
			}
			return nil
		}
		if token == "-v" || token == "-V" {
			return nil
		}
		if token == "-p" {
			continue
		}
		if strings.HasPrefix(token, "-") {
			continue
		}
		return args[i:]
	}
	return nil
}

func unwrapBuiltinArgs(args []string) []string {
	for i := 0; i < len(args); i++ {
		token := strings.TrimSpace(args[i])
		if token == "" {
			continue
		}
		if token == "--" {
			if i+1 < len(args) {
				return args[i+1:]
			}
			return nil
		}
		if strings.HasPrefix(token, "-") {
			continue
		}
		return args[i:]
	}
	return nil
}

func unwrapNoHupArgs(args []string) []string {
	for i := 0; i < len(args); i++ {
		token := strings.TrimSpace(args[i])
		if token == "" {
			continue
		}
		if token == "--" {
			if i+1 < len(args) {
				return args[i+1:]
			}
			return nil
		}
		if strings.HasPrefix(token, "-") {
			return nil
		}
		return args[i:]
	}
	return nil
}

func isInteractiveRootShell(name string) bool {
	switch strings.ToLower(strings.TrimSpace(filepath.Base(name))) {
	case "su", "bash", "sh", "zsh", "dash", "ksh", "ash", "fish":
		return true
	default:
		return false
	}
}

func hasRecursiveFlag(args []string) bool {
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "--recursive" {
			return true
		}
		if len(trimmed) >= 2 && strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "--") {
			flags := strings.ToLower(trimmed[1:])
			if strings.Contains(flags, "r") {
				return true
			}
		}
	}
	return false
}

func filterNonFlagArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" || strings.HasPrefix(trimmed, "-") {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func hasDangerousDeleteTarget(targets []string) bool {
	for _, target := range targets {
		if isDangerousPathTarget(target) {
			return true
		}
	}
	return false
}

func hasDangerousOwnershipTarget(targets []string, skip int) bool {
	if skip >= len(targets) {
		return false
	}
	for _, target := range targets[skip:] {
		if isDangerousPathTarget(target) {
			return true
		}
	}
	return false
}

func hasDangerousFindTarget(args []string) bool {
	paths := make([]string, 0, 2)
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			break
		}
		paths = append(paths, trimmed)
	}
	if len(paths) == 0 {
		return true
	}
	return hasDangerousDeleteTarget(paths)
}

func isDangerousPathTarget(target string) bool {
	trimmed := strings.Trim(strings.TrimSpace(target), `"'`)
	switch trimmed {
	case "/", "/*", "/.", "~", "~/", "~/*", ".", "./", "./*", "..", "../", "../*", "/root", "/root/":
		return true
	}
	return strings.HasPrefix(trimmed, "/root/")
}

func hasArgPrefix(args []string, prefix string) bool {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(arg)), prefix) {
			return true
		}
	}
	return false
}

func hasExactArg(args []string, needle string) bool {
	return containsExactArg(args, needle)
}

func containsExactArg(args []string, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	for _, arg := range args {
		if strings.ToLower(strings.TrimSpace(arg)) == needle {
			return true
		}
	}
	return false
}

func containsPrefixArg(args []string, prefix string) bool {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(arg)), prefix) {
			return true
		}
	}
	return false
}

func containsNetcatExecArg(args []string) bool {
	for _, arg := range args {
		trimmed := strings.ToLower(strings.TrimSpace(arg))
		if trimmed == "-e" || strings.HasPrefix(trimmed, "-e") {
			return true
		}
	}
	return false
}

func gitCleanIsDangerous(args []string) bool {
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if !strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "--") {
			continue
		}
		flags := strings.ToLower(trimmed[1:])
		if strings.Contains(flags, "x") && strings.Contains(flags, "f") && strings.Contains(flags, "d") {
			return true
		}
	}
	return false
}

func wrappedShellCommand(tokens []string) (string, bool) {
	for i := 0; i < len(tokens); i++ {
		token := strings.TrimSpace(tokens[i])
		if token == "" {
			continue
		}
		if token == "--" {
			return "", false
		}
		if token == "-c" {
			if i+1 >= len(tokens) {
				return "", false
			}
			return tokens[i+1], true
		}
		if strings.HasPrefix(token, "-") {
			flags := strings.TrimLeft(token, "-")
			if strings.Contains(flags, "c") {
				if i+1 >= len(tokens) {
					return "", false
				}
				return tokens[i+1], true
			}
			continue
		}
		return "", false
	}
	return "", false
}

func shellCommandSegments(command string) []string {
	var (
		segments []string
		buf      strings.Builder
		squote   bool
		dquote   bool
		escape   bool
	)
	flush := func() {
		part := strings.TrimSpace(buf.String())
		if part != "" {
			segments = append(segments, part)
		}
		buf.Reset()
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escape {
			buf.WriteRune(r)
			escape = false
			continue
		}
		switch r {
		case '\\':
			escape = true
			buf.WriteRune(r)
		case '\'':
			if !dquote {
				squote = !squote
			}
			buf.WriteRune(r)
		case '"':
			if !squote {
				dquote = !dquote
			}
			buf.WriteRune(r)
		case ';':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			flush()
		case '&':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			if i+1 < len(runes) && runes[i+1] == '&' {
				flush()
				i++
				continue
			}
			buf.WriteRune(r)
		case '|':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			flush()
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
		default:
			buf.WriteRune(r)
		}
	}
	flush()
	return segments
}

func shellSegmentTokens(segment string) []string {
	var (
		tokens []string
		buf    strings.Builder
		squote bool
		dquote bool
		escape bool
	)
	flush := func() {
		token := strings.TrimSpace(buf.String())
		if token == "" {
			buf.Reset()
			return
		}
		tokens = append(tokens, token)
		buf.Reset()
	}
	for _, r := range segment {
		if escape {
			buf.WriteRune(r)
			escape = false
			continue
		}
		switch r {
		case '\\':
			escape = true
		case '\'':
			if !dquote {
				squote = !squote
				continue
			}
			buf.WriteRune(r)
		case '"':
			if !squote {
				dquote = !dquote
				continue
			}
			buf.WriteRune(r)
		case ' ', '\t', '\n':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			flush()
		default:
			buf.WriteRune(r)
		}
	}
	flush()
	return tokens
}
