// Package envfile loads simple KEY=VALUE pairs from a .env file into
// the process environment, so local development doesn't require
// exporting Vertex AI credentials by hand. Real environment variables
// always win — this only fills in what isn't already set.
package envfile

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Load reads path (typically ".env") and calls os.Setenv for every
// KEY=VALUE line whose KEY isn't already set in the environment. A
// missing file is not an error — it just means there's nothing to load
// (e.g. in production, where real env vars are set directly).
func Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("envfile: open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}
		if _, alreadySet := os.LookupEnv(key); alreadySet {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("envfile: set %s: %w", key, err)
		}
	}
	return scanner.Err()
}
