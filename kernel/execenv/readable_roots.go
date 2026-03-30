package execenv

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func hasExplicitReadableRoots(policy SandboxPolicy) bool {
	return len(normalizeStringList(policy.ReadableRoots)) > 0
}

func shellReadableRoots(policy SandboxPolicy, workDir string) []string {
	if !hasExplicitReadableRoots(policy) {
		return nil
	}
	roots := make([]string, 0, len(policy.ReadableRoots)+len(policy.WritableRoots)+16)
	for _, one := range policy.ReadableRoots {
		if resolved := resolveSandboxPath(workDir, one); resolved != "" {
			roots = append(roots, sandboxPathVariants(resolved)...)
		}
	}
	for _, one := range policy.WritableRoots {
		if resolved := resolveSandboxPath(workDir, one); resolved != "" {
			roots = append(roots, sandboxPathVariants(resolved)...)
		}
	}
	roots = append(roots, scratchReadableRoots()...)
	roots = append(roots, platformReadableRoots(runtime.GOOS)...)
	return normalizeStringList(filterExistingPaths(roots))
}

func scratchReadableRoots() []string {
	roots := []string{"/tmp", "/var/tmp", "/private/tmp"}
	if tmp := strings.TrimSpace(os.TempDir()); tmp != "" {
		roots = append(roots, tmp)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots, filepath.Join(home, ".cache"))
		roots = append(roots, filepath.Join(home, "Library", "Caches"))
	}
	return normalizeStringList(expandSandboxPathVariants(roots))
}

func platformReadableRoots(goos string) []string {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "darwin":
		return normalizeStringList(expandSandboxPathVariants([]string{
			"/System",
			"/usr",
			"/bin",
			"/sbin",
			"/Library",
			"/Applications",
			"/opt",
			"/private/etc",
			"/private/var/db/timezone",
			"/dev",
		}))
	default:
		return normalizeStringList(expandSandboxPathVariants([]string{
			"/bin",
			"/usr",
			"/lib",
			"/lib64",
			"/etc",
			"/dev",
			"/proc",
			"/sys",
			"/run",
			"/var",
			"/opt",
		}))
	}
}

func expandSandboxPathVariants(paths []string) []string {
	values := make([]string, 0, len(paths)*2)
	for _, one := range paths {
		values = append(values, sandboxPathVariants(one)...)
	}
	return values
}

func sandboxPathVariants(path string) []string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" || cleaned == "." {
		return nil
	}
	variants := []string{cleaned}
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil && strings.TrimSpace(resolved) != "" {
		variants = append(variants, filepath.Clean(resolved))
	}
	return normalizeStringList(variants)
}

func resolveSandboxPath(baseDir, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	if strings.TrimSpace(baseDir) == "" {
		return ""
	}
	return filepath.Clean(filepath.Join(baseDir, trimmed))
}
