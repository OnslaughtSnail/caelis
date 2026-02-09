package envload

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadNearest loads .env file from cwd and parent directories.
// Returns loaded .env path when found, empty string otherwise.
func LoadNearest() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		path := filepath.Join(dir, ".env")
		if _, err := os.Stat(path); err == nil {
			if err := loadFile(path); err != nil {
				return "", err
			}
			return path, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

func loadFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			continue
		}
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, `"'`)
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("envload: set %q: %w", key, err)
		}
	}
	return scanner.Err()
}
