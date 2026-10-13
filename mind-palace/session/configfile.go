package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// UpdateConfigFile rewrites the plugin's config.json at path: the file's
// existing keys are kept, set's keys are written (overwriting), remove's
// keys are dropped. A missing file starts from an empty object; a file that
// is not a JSON object is an error rather than being overwritten. The file
// is written with mode 0600 through a temporary file in the same
// directory, since it may hold the server token.
func UpdateConfigFile(path string, set map[string]any, remove []string) error {
	doc := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("clued: read config %s: %w", path, err)
	default:
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("clued: config %s is not a JSON object: %w", path, err)
		}
		if doc == nil {
			doc = map[string]any{}
		}
	}
	for k, v := range set {
		doc[k] = v
	}
	for _, k := range remove {
		delete(doc, k)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("clued: encode config: %w", err)
	}
	out = append(out, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("clued: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return fmt.Errorf("clued: write config: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("clued: write config %s: %w", path, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(out); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("clued: write config %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("clued: replace config %s: %w", path, err)
	}
	return nil
}
