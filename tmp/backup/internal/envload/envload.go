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

// LoadFileIfExists loads one .env file when the file exists.
// It returns true when file was loaded, false when file does not exist.
func LoadFileIfExists(path string) (bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return false, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := loadFile(path); err != nil {
		return false, err
	}
	return true, nil
}

// LoadFilesIfExists loads .env files in order and returns loaded file paths.
// Missing files are ignored. Existing environment variables are never overridden.
func LoadFilesIfExists(paths []string) ([]string, error) {
	loaded := make([]string, 0, len(paths))
	for _, one := range paths {
		path := strings.TrimSpace(one)
		if path == "" {
			continue
		}
		ok, err := LoadFileIfExists(path)
		if err != nil {
			return loaded, err
		}
		if ok {
			loaded = append(loaded, path)
		}
	}
	return loaded, nil
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
