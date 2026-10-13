package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUpdateConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "config.json")
	if err := UpdateConfigFile(path, map[string]any{"serverUrl": "https://x", "serverToken": "t"}, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	// Existing keys survive; set overwrites; remove drops.
	_ = os.WriteFile(path, []byte(`{"mongoUrl":"mongodb://x","port":8085,"serverUrl":"https://old","serverToken":"old","disabledEnrichers":["a"]}`), 0o600)
	if err := UpdateConfigFile(path, map[string]any{"serverUrl": "https://new"}, []string{"serverToken", "missing"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["mongoUrl"] != "mongodb://x" || doc["port"] != float64(8085) || doc["serverUrl"] != "https://new" || doc["serverToken"] != nil || len(doc["disabledEnrichers"].([]any)) != 1 {
		t.Fatalf("doc = %v", doc)
	}
	cfg := LoadConfig(path)
	if cfg.ServerURL != "https://new" || cfg.ServerToken != "" {
		t.Fatalf("LoadConfig after update = %+v", cfg)
	}
	// A file that is not an object is left alone.
	_ = os.WriteFile(path, []byte(`[1,2]`), 0o600)
	if err := UpdateConfigFile(path, map[string]any{"a": 1}, nil); err == nil {
		t.Fatal("array config accepted")
	}
	if raw, _ := os.ReadFile(path); string(raw) != `[1,2]` {
		t.Fatal("bad config was overwritten")
	}
}
