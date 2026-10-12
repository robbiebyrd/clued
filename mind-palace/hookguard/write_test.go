package hookguard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const storyDoc = `---
id: 0001-abc
title: Finished story
purpose: Prove the guard
type: feat
status: in_progress
priority: "2"
created: "2026-10-10T02:27:00.171Z"
updated: "2026-10-10T03:05:44.604Z"
started: "2026-10-10T02:27:00.171Z"
progress:
  "1":
    status: complete
---

# Finished story

## Problem Statement

The guard needs a story to check.

## Steps

### 1: Only step

Do it.

## Acceptance Criteria

- [x] It works
- [ ] It is documented

## Work Log

### 2026-10-10T03:05:44.604Z - Started
`

const planDoc = `---
id: 0002-def
title: A plan
type: impl
status: ready
priority: "2"
created: "2026-10-10T02:27:00.171Z"
updated: "2026-10-10T03:05:44.604Z"
progress:
  "1":
    status: pending
---

# A plan

## Summary

Build the thing.

## Phase 1: Build

### 1.1: Step one

Do it.
`

// guardDir writes a docs/plans and docs/stories tree holding one plan and
// one story and returns the root and the Dirs pointing at it.
func guardDir(t *testing.T) (string, Dirs) {
	t.Helper()
	root := t.TempDir()
	dirs := Dirs{Plans: filepath.Join(root, "docs", "plans"), Stories: filepath.Join(root, "docs", "stories")}
	for _, d := range []string{dirs.Plans, dirs.Stories, filepath.Join(dirs.Plans, "templates"), filepath.Join(dirs.Stories, "archive")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for p, body := range map[string]string{
		filepath.Join(dirs.Stories, "0001-abc-feat-finished-story.md"): storyDoc,
		filepath.Join(dirs.Plans, "0002-def-impl-a-plan.md"):           planDoc,
		filepath.Join(dirs.Plans, "templates", "default.md"):           "# {{title}}\n",
		filepath.Join(dirs.Plans, "README.md"):                         "notes\n",
	} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, dirs
}

func input(tool, cwd string, params map[string]any) WriteInput {
	raw, _ := json.Marshal(params)
	return WriteInput{ToolName: tool, Cwd: cwd, ToolInput: raw}
}

func wantDeny(t *testing.T, d *PreToolDecision, parts ...string) {
	t.Helper()
	if !d.Denied() {
		t.Fatalf("got %+v, want a denial", d)
	}
	for _, p := range parts {
		if !strings.Contains(d.HookSpecificOutput.PermissionDecisionReason, p) {
			t.Errorf("reason %q lacks %q", d.HookSpecificOutput.PermissionDecisionReason, p)
		}
	}
}

func wantAllow(t *testing.T, d *PreToolDecision) {
	t.Helper()
	if d != nil {
		t.Fatalf("got %+v, want no decision", d)
	}
}

func TestWriteGuardIgnoresUnrelatedPathsAndTools(t *testing.T) {
	root, dirs := guardDir(t)
	wantAllow(t, CheckWrite(input("Write", root, map[string]any{"file_path": "src/main.go", "content": "x"}), dirs))
	wantAllow(t, CheckWrite(input("Edit", root, map[string]any{"file_path": "/elsewhere/docs/plans/x.md", "old_string": "a"}), dirs))
	wantAllow(t, CheckWrite(input("Read", root, map[string]any{"file_path": "docs/plans/0002-def-impl-a-plan.md"}), dirs))
	wantAllow(t, CheckWrite(input("Write", root, map[string]any{"file_path": "docs/specs/plans.md"}), dirs))
	wantAllow(t, CheckWrite(input("Write", root, nil), dirs))
	wantAllow(t, CheckWrite(WriteInput{ToolName: "Write", ToolInput: json.RawMessage(`"not an object"`)}, dirs))
}

func TestWriteGuardBlocksSuperpowersPlansEverywhere(t *testing.T) {
	_, dirs := guardDir(t)
	for _, tool := range []string{"Write", "Edit"} {
		d := CheckWrite(input(tool, "/repo", map[string]any{"file_path": "/repo/docs/superpowers/plans/2026-10-09-feature.md", "old_string": "x"}), dirs)
		wantDeny(t, d, "plan_create", "plan_update")
	}
	wantAllow(t, CheckWrite(input("Write", "/repo", map[string]any{"file_path": "/repo/docs/superpowers/specs/2026-10-09-design.md"}), dirs))
	wantDeny(t, CheckWrite(input("Bash", "/repo", map[string]any{"command": "cat > docs/superpowers/plans/x.md <<EOF\nplan\nEOF"}), dirs), "plan_create")
	wantDeny(t, CheckWrite(input("Bash", "/repo", map[string]any{"command": "rm docs/superpowers/plans/x.md"}), dirs), "plan_create")
}

func TestWriteGuardBlocksNewDocumentsAndNamesTheCreateTools(t *testing.T) {
	root, dirs := guardDir(t)
	d := CheckWrite(input("Write", root, map[string]any{"file_path": "docs/plans/0009-zzz-impl-new.md", "content": "---\n"}), dirs)
	wantDeny(t, d, "plan_getTemplate", "plan_getSchema", "plan_create", "mind-palace plan create")
	d = CheckWrite(input("Write", root, map[string]any{"file_path": filepath.Join(dirs.Stories, "my-story.md")}), dirs)
	wantDeny(t, d, "story_create")
	// An Edit of a document that does not exist is a creation too.
	d = CheckWrite(input("Edit", root, map[string]any{"file_path": "docs/stories/0009-zzz-feat-new.md", "old_string": "a", "new_string": "b"}), dirs)
	wantDeny(t, d, "story_create")
}

func TestWriteGuardBlocksOverwritingADocument(t *testing.T) {
	root, dirs := guardDir(t)
	d := CheckWrite(input("Write", root, map[string]any{"file_path": "docs/plans/0002-def-impl-a-plan.md", "content": "new"}), dirs)
	wantDeny(t, d, "plan 0002-def", "plan_update", "plan_setStatus")
	d = CheckWrite(input("NotebookEdit", root, map[string]any{"notebook_path": "docs/stories/0001-abc-feat-finished-story.md"}), dirs)
	wantDeny(t, d, "story_update")
}

func TestWriteGuardBlocksTemplateFiles(t *testing.T) {
	root, dirs := guardDir(t)
	d := CheckWrite(input("Edit", root, map[string]any{"file_path": "docs/plans/templates/default.md", "old_string": "title"}), dirs)
	wantDeny(t, d, "plan_createTemplate", "plan_updateTemplate")
	d = CheckWrite(input("Write", root, map[string]any{"file_path": "docs/stories/templates/mine.md"}), dirs)
	wantDeny(t, d, "story_createTemplate")
}

func TestWriteGuardLeavesExistingNonDocumentsAlone(t *testing.T) {
	root, dirs := guardDir(t)
	wantAllow(t, CheckWrite(input("Edit", root, map[string]any{"file_path": "docs/plans/README.md", "old_string": "notes"}), dirs))
	wantAllow(t, CheckWrite(input("Write", root, map[string]any{"file_path": "docs/plans/README.md", "content": "x"}), dirs))
}

func TestWriteGuardBlocksFrontMatterEdits(t *testing.T) {
	root, dirs := guardDir(t)
	for _, old := range []string{"status: in_progress", "in_progress", "id: 0001-abc", "---\nid:", "started:", "    status: complete\n---\n\n# Finished"} {
		d := CheckWrite(input("Edit", root, map[string]any{"file_path": "docs/stories/0001-abc-feat-finished-story.md", "old_string": old, "new_string": "x"}), dirs)
		wantDeny(t, d, "front matter", "story_setStatus", "story_getTransitions", "story_setTitle")
		if !strings.Contains(d.HookSpecificOutput.PermissionDecisionReason, "started, completed") {
			t.Errorf("old %q: reason does not list the immutable fields: %q", old, d.HookSpecificOutput.PermissionDecisionReason)
		}
	}
	d := CheckWrite(input("Edit", root, map[string]any{"file_path": "docs/plans/0002-def-impl-a-plan.md", "old_string": "status: ready"}), dirs)
	wantDeny(t, d, "plan_setStatus", "plan_setPriority")
	d = CheckWrite(input("Edit", root, map[string]any{"file_path": "docs/plans/0002-def-impl-a-plan.md", "old_string": ""}), dirs)
	wantDeny(t, d, "non-empty old_string")
}

func TestWriteGuardBlocksServiceOwnedSections(t *testing.T) {
	root, dirs := guardDir(t)
	story := "docs/stories/0001-abc-feat-finished-story.md"
	d := CheckWrite(input("Edit", root, map[string]any{"file_path": story, "old_string": "# Finished story"}), dirs)
	wantDeny(t, d, "H1", "story_setTitle")
	d = CheckWrite(input("Edit", root, map[string]any{"file_path": story, "old_string": "- [ ] It is documented", "new_string": "- [x] It is documented"}), dirs)
	wantDeny(t, d, "acceptance criteria", "story_setCriterion", "story_addCriterion", "story_removeCriterion")
	d = CheckWrite(input("Edit", root, map[string]any{"file_path": story, "old_string": "## Work Log\n"}), dirs)
	wantDeny(t, d, "work log", "story_appendWorkLog", "mind-palace story log")
	// A plan has no criteria gate or work log: those words in its body are plain content.
	plan := "docs/plans/0002-def-impl-a-plan.md"
	d = CheckWrite(input("Edit", root, map[string]any{"file_path": plan, "old_string": "# A plan"}), dirs)
	wantDeny(t, d, "plan_setTitle")
}

func TestWriteGuardAllowsBodyEditsWithContext(t *testing.T) {
	root, dirs := guardDir(t)
	d := CheckWrite(input("Edit", root, map[string]any{"file_path": "docs/stories/0001-abc-feat-finished-story.md", "old_string": "The guard needs a story to check.", "new_string": "Changed."}), dirs)
	if d.Denied() {
		t.Fatalf("body edit denied: %+v", d)
	}
	if d == nil || !strings.Contains(d.HookSpecificOutput.AdditionalContext, "story_update") || !strings.Contains(d.HookSpecificOutput.AdditionalContext, "story_setStatus") {
		t.Errorf("context = %+v", d)
	}
	if d.HookSpecificOutput.PermissionDecision != "" {
		t.Errorf("a body edit must not pre-approve the call: %+v", d)
	}
	// Multi-edit: every old_string is checked; one in the front matter blocks the lot.
	d = CheckWrite(input("MultiEdit", root, map[string]any{"file_path": "docs/plans/0002-def-impl-a-plan.md", "edits": []map[string]string{{"old_string": "Build the thing.", "new_string": "Build it."}, {"old_string": "priority: \"2\"", "new_string": "priority: \"1\""}}}), dirs)
	wantDeny(t, d, "front matter")
	d = CheckWrite(input("MultiEdit", root, map[string]any{"file_path": "docs/plans/0002-def-impl-a-plan.md", "edits": []map[string]string{{"old_string": "Build the thing.", "new_string": "Build it."}, {"old_string": "Do it.", "new_string": "Done."}}}), dirs)
	if d.Denied() {
		t.Errorf("multi-edit of the body denied: %+v", d)
	}
}

func TestWriteGuardMatchesCRLFDocuments(t *testing.T) {
	root, dirs := guardDir(t)
	p := filepath.Join(dirs.Plans, "0003-crl-impl-windows.md")
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(planDoc, "\n", "\r\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	d := CheckWrite(input("Edit", root, map[string]any{"file_path": p, "old_string": "status: ready\r\n"}), dirs)
	wantDeny(t, d, "front matter")
	d = CheckWrite(input("Edit", root, map[string]any{"file_path": p, "old_string": "Build the thing.\r\n"}), dirs)
	if d.Denied() {
		t.Errorf("body edit denied: %+v", d)
	}
}

func TestWriteGuardBashRules(t *testing.T) {
	root, dirs := guardDir(t)
	denied := []string{
		"rm docs/plans/0002-def-impl-a-plan.md",
		"rm -rf docs/stories",
		"mv docs/plans/0002-def-impl-a-plan.md docs/plans/archive/",
		"sed -i 's/ready/complete/' docs/plans/0002-def-impl-a-plan.md",
		"sed -i.bak -e s/a/b/ " + filepath.Join(dirs.Plans, "0002-def-impl-a-plan.md"),
		"perl -pi -e 's/a/b/' docs/stories/0001-abc-feat-finished-story.md",
		"echo '- [x] done' >> docs/stories/0001-abc-feat-finished-story.md",
		"cat <<'EOF' > docs/plans/0009-new-impl-x.md\n---\nid: x\n---\nEOF",
		"printf x | tee docs/plans/0002-def-impl-a-plan.md",
		"touch ./docs/stories/0009-zzz-feat-new.md",
		"git mv docs/plans/0002-def-impl-a-plan.md docs/plans/0002-def-dsgn-a-plan.md",
		"mind-palace plan get 0002-def --content > docs/plans/0002-def-impl-a-plan.md",
		"cd docs && rm plans/x.md; rm -r " + filepath.Join(root, "docs", "plans"),
	}
	for _, cmd := range denied {
		d := CheckWrite(input("Bash", root, map[string]any{"command": cmd}), dirs)
		if !d.Denied() {
			t.Errorf("%q: got %+v, want a denial", cmd, d)
			continue
		}
		if r := d.HookSpecificOutput.PermissionDecisionReason; !strings.Contains(r, "_update") || !strings.Contains(r, "_setStatus") {
			t.Errorf("%q: reason names no mind-palace tools: %q", cmd, r)
		}
	}
	allowed := []string{
		"",
		"ls docs/plans",
		"cat docs/plans/0002-def-impl-a-plan.md",
		"sed -n 1,20p docs/plans/0002-def-impl-a-plan.md",
		"grep -rn ready docs/plans 2>/dev/null",
		"rg status docs/stories/ > /tmp/out.txt",
		"git diff -- docs/plans",
		"rm -rf build/ && go test ./...",
		"mind-palace plan set-status 0002-def in_progress",
		"mind-palace plan create --input /tmp/plan.json && rm /tmp/plan.json",
		"rm /tmp/docs-plans-notes.txt",
		"touch docs/plans-notes.md",
	}
	for _, cmd := range allowed {
		if d := CheckWrite(input("Bash", root, map[string]any{"command": cmd}), dirs); d != nil {
			t.Errorf("%q: got %+v, want no decision", cmd, d)
		}
	}
}

func TestWriteGuardBashHonoursConfiguredDirectories(t *testing.T) {
	root := t.TempDir()
	dirs := Dirs{Plans: filepath.Join(root, "planning"), Stories: filepath.Join(root, "tickets")}
	d := CheckWrite(input("Bash", root, map[string]any{"command": "rm planning/0001-abc-impl-x.md"}), dirs)
	wantDeny(t, d, "planning/", "plan_update")
	d = CheckWrite(input("Bash", root, map[string]any{"command": "rm -r tickets"}), dirs)
	wantDeny(t, d, "tickets/", "story_update")
	// The default directories are not special once others are configured.
	wantAllow(t, CheckWrite(input("Bash", root, map[string]any{"command": "rm docs/plans/x.md"}), dirs))
	wantAllow(t, CheckWrite(input("Write", root, map[string]any{"file_path": "docs/stories/0001-abc-feat-x.md"}), dirs))
	// An empty directory disables that kind's rule.
	wantAllow(t, CheckWrite(input("Bash", root, map[string]any{"command": "rm -r tickets"}), Dirs{Plans: dirs.Plans}))
}

func TestWriteGuardBlocksCommitsClosingOpenStories(t *testing.T) {
	root, dirs := guardDir(t)
	for _, msg := range []string{"Closes 0001-abc", "closes #0001-abc", "fixes 0001-abc", "Resolves: #0001-abc", "completes 0001-abc"} {
		cmd := "git commit -m \"Finish the guard\n\n" + msg + "\""
		d := CheckWrite(input("Bash", root, map[string]any{"command": cmd}), dirs)
		wantDeny(t, d, "Story 0001-abc is in_progress", "story_setStatus", "story_getCriteria")
	}
	for _, cmd := range []string{
		"git commit -m \"Work on 0001-abc\"",              // a plain reference is fine
		"git commit -m \"Closes 0009-zzz\"",               // unknown story
		"git log --grep 'closes 0001-abc'",                // not a commit
		"git -c user.name=x commit -m 'fixes 0001-abc' x", // only when the story is complete (see below)
	} {
		d := CheckWrite(input("Bash", root, map[string]any{"command": cmd}), dirs)
		if strings.Contains(cmd, "-c user.name") {
			wantDeny(t, d, "0001-abc")
			continue
		}
		wantAllow(t, d)
	}
	// Once the story is complete (or archived) the closing keyword is fine.
	done := strings.Replace(storyDoc, "status: in_progress", "status: complete", 1)
	if err := os.WriteFile(filepath.Join(dirs.Stories, "0001-abc-feat-finished-story.md"), []byte(done), 0o644); err != nil {
		t.Fatal(err)
	}
	wantAllow(t, CheckWrite(input("Bash", root, map[string]any{"command": "git commit -m 'Closes 0001-abc'"}), dirs))
	// Archived stories are found under archive/.
	archived := strings.Replace(storyDoc, "status: in_progress", "status: archived", 1)
	if err := os.Rename(filepath.Join(dirs.Stories, "0001-abc-feat-finished-story.md"), filepath.Join(dirs.Stories, "archive", "0001-abc-feat-finished-story.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.Stories, "archive", "0001-abc-feat-finished-story.md"), []byte(archived), 0o644); err != nil {
		t.Fatal(err)
	}
	wantAllow(t, CheckWrite(input("Bash", root, map[string]any{"command": "git commit -m 'Closes 0001-abc'"}), dirs))
}

func TestWriteGuardAddsPlanModeContext(t *testing.T) {
	_, dirs := guardDir(t)
	for _, tool := range []string{"EnterPlanMode", "ExitPlanMode"} {
		d := CheckWrite(input(tool, "/repo", nil), dirs)
		if d.Denied() || d == nil || !strings.Contains(d.HookSpecificOutput.AdditionalContext, "plan_create") || !strings.Contains(d.HookSpecificOutput.AdditionalContext, "plan_getTemplate") {
			t.Errorf("%s: %+v", tool, d)
		}
	}
}

func TestCheckWriteJSON(t *testing.T) {
	root, dirs := guardDir(t)
	d, err := CheckWriteJSON([]byte(`{"tool_name":"Write","cwd":"`+root+`","tool_input":{"file_path":"docs/plans/0002-def-impl-a-plan.md","content":"x"}}`), dirs)
	if err != nil || !d.Denied() {
		t.Fatalf("d=%+v err=%v", d, err)
	}
	if d.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("event = %q", d.HookSpecificOutput.HookEventName)
	}
	if _, err := CheckWriteJSON([]byte("nope"), dirs); err == nil {
		t.Error("invalid JSON accepted")
	}
}
