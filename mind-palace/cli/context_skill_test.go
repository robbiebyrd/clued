package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestSkillProbesMatchContextCommands keeps the probe table of the
// clued-context skill in step with the subcommands of the context group:
// every probe the binary offers has a row, and every row names a probe.
func TestSkillProbesMatchContextCommands(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "skills", "clued-context", "SKILL.md"))
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	documented := skillProbeNames(t, string(data))

	commands := map[string]bool{}
	for _, c := range New().contextCmd().Commands() {
		commands[c.Name()] = true
	}

	var missing, extra []string
	for name := range commands {
		if !documented[name] {
			missing = append(missing, name)
		}
	}
	for name := range documented {
		if !commands[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("probes without a row in the skill table: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("skill table rows that are not probes: %v", extra)
	}
}

// skillProbeNames parses the "## Probes" table of the skill and returns the
// backticked name in the first cell of every data row, failing on a missing
// section, a malformed row or a duplicate name.
func skillProbeNames(t *testing.T, doc string) map[string]bool {
	t.Helper()
	_, section, ok := strings.Cut(doc, "\n## Probes\n")
	if !ok {
		t.Fatal("skill has no \"## Probes\" section")
	}
	if i := strings.Index(section, "\n## "); i >= 0 {
		section = section[:i]
	}

	names := map[string]bool{}
	rows := 0
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		rows++
		if rows == 1 {
			if line != "| Probe | Arguments | Output |" {
				t.Fatalf("unexpected table header %q", line)
			}
			continue
		}
		if rows == 2 {
			continue // separator
		}
		cell := strings.TrimSpace(strings.SplitN(strings.TrimPrefix(line, "|"), "|", 2)[0])
		name := strings.Trim(cell, "`")
		if name == "" || name == cell || strings.ContainsAny(name, "` ") {
			t.Fatalf("first cell %q of row %q is not a backticked probe name", cell, line)
		}
		if names[name] {
			t.Errorf("duplicate row for %q", name)
		}
		names[name] = true
	}
	if rows < 3 {
		t.Fatal("\"## Probes\" table has no rows")
	}
	return names
}
