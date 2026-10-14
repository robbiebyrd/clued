package plugin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/ops"
)

// TestPluginServesTheCoreToolTier: the plugin starts the plan and story
// server with the core tier, and every plan or story tool its skills and
// commands tell the model to call is in that tier.
func TestPluginServesTheCoreToolTier(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var servers map[string]struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(raw, &servers); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(servers["mind-palace"].Args, " ")
	if !strings.Contains(args, "--tools core") {
		t.Fatalf(".mcp.json starts mind-palace with %q, want --tools core", args)
	}

	regs := ops.All()
	toolRe := regexp.MustCompile(`\b(plan|story)_([a-zA-Z]+)\b`)
	for _, dir := range []string{"skills", "commands"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range toolRe.FindAllStringSubmatch(string(body), -1) {
				reg := regs[m[1]]
				op, ok := reg.Get(m[2])
				if !ok {
					// Not a tool name (for example plan_storage in prose).
					continue
				}
				if !op.Core() {
					t.Errorf("%s names %s, which is not in the core tier", strings.TrimPrefix(path, root+"/"), m[0])
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
