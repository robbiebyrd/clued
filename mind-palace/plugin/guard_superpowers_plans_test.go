package plugin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runGuard(t *testing.T, env []string, tool, filePath string) result {
	t.Helper()
	input := map[string]any{"tool_name": tool, "tool_input": map[string]any{}}
	if filePath != "" {
		input["tool_input"] = map[string]any{"file_path": filePath}
	}
	b, _ := json.Marshal(input)
	return runScript(t, filepath.Join(repoRoot(t), "hooks/guard-superpowers-plans"), string(b), env)
}

func guardEnv() []string { return append(os.Environ(), "LC_ALL=C") }

func TestGuardBlocksWriteUnderPlansAndNamesMCPTools(t *testing.T) {
	requireJq(t)
	r := runGuard(t, guardEnv(), "Write", "/repo/docs/superpowers/plans/2026-10-09-feature.md")
	if r.code != 2 {
		t.Errorf("code = %d, want 2", r.code)
	}
	if !strings.Contains(r.stderr, "plan_create") || !strings.Contains(r.stderr, "plan_update") {
		t.Errorf("stderr misses MCP tool names: %q", r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q", r.stdout)
	}
}

func TestGuardBlocksEditUnderPlans(t *testing.T) {
	requireJq(t)
	if r := runGuard(t, guardEnv(), "Edit", "/repo/docs/superpowers/plans/2026-10-09-feature.md"); r.code != 2 {
		t.Errorf("code = %d, want 2", r.code)
	}
}

func TestGuardAllowsWriteUnderSpecs(t *testing.T) {
	requireJq(t)
	r := runGuard(t, guardEnv(), "Write", "/repo/docs/superpowers/specs/2026-10-09-feature-design.md")
	if r.code != 0 || r.stdout != "" || r.stderr != "" {
		t.Errorf("got %+v, want silent success", r)
	}
}

func TestGuardAllowsUnrelatedPath(t *testing.T) {
	requireJq(t)
	if r := runGuard(t, guardEnv(), "Write", "/repo/src/index.ts"); r.code != 0 {
		t.Errorf("code = %d, want 0", r.code)
	}
}

func TestGuardAllowsInputWithoutFilePath(t *testing.T) {
	requireJq(t)
	r := runGuard(t, guardEnv(), "Write", "")
	if r.code != 0 || r.stderr != "" {
		t.Errorf("got %+v, want silent success", r)
	}
}

func TestGuardReportsInactiveWhenJqMissing(t *testing.T) {
	r := runGuard(t, []string{"PATH=/var/empty"}, "Write", "/repo/docs/superpowers/plans/x.md")
	if r.code != 1 {
		t.Errorf("code = %d, want 1 (non-blocking error)", r.code)
	}
	if !strings.Contains(r.stderr, "jq not found") || !strings.Contains(r.stderr, "plan guard inactive") {
		t.Errorf("stderr = %q", r.stderr)
	}
}
