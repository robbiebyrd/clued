package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// execStdin runs the CLI with the given stdin and no plans directory setup.
func execStdin(t *testing.T, stdin string, args ...string) run {
	t.Helper()
	// hook status-review changes into the hook's cwd; keep the test process where it was.
	if wd, err := os.Getwd(); err == nil {
		defer func() { _ = os.Chdir(wd) }()
	}
	app := New()
	var out, errb bytes.Buffer
	app.Stdout, app.Stderr, app.Stdin = &out, &errb, strings.NewReader(stdin)
	root := app.Root()
	root.SetArgs(args)
	err := root.Execute()
	app.close()
	code := 0
	if err != nil {
		code = ExitCode(err)
	}
	return run{code: code, stdout: out.String(), stderr: errb.String()}
}

func TestHookStopGuardBlocksWithBareDecisionJSON(t *testing.T) {
	r := execStdin(t, `{"session_id":"s","stop_hook_active":false,"last_assistant_message":"That failure is pre-existing."}`, "hook", "stop-guard")
	if r.code != 0 {
		t.Fatalf("code = %d, stderr %q", r.code, r.stderr)
	}
	var d struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &d); err != nil {
		t.Fatalf("stdout is not the decision JSON: %q (%v)", r.stdout, err)
	}
	if d.Decision != "block" || !strings.HasPrefix(d.Reason, "STOP HOOK VIOLATION: NOTHING IS PRE-EXISTING") {
		t.Errorf("decision = %+v", d)
	}
	if strings.Contains(r.stdout, `"ok"`) {
		t.Errorf("hook output must not be wrapped in the CLI envelope: %q", r.stdout)
	}
}

func TestHookStopGuardPrintsNothingWhenAllowed(t *testing.T) {
	for _, in := range []string{
		`{"stop_hook_active":false,"last_assistant_message":"Done; all tests pass."}`,
		`{"stop_hook_active":true,"last_assistant_message":"That failure is pre-existing."}`,
		`{}`,
	} {
		r := execStdin(t, in, "hook", "stop-guard")
		if r.code != 0 || r.stdout != "" {
			t.Errorf("input %s: got %+v, want exit 0 and no output", in, r)
		}
	}
}

func TestHookStopGuardFailsOnInvalidInput(t *testing.T) {
	r := execStdin(t, "not json", "hook", "stop-guard")
	if r.code == 0 || r.stdout != "" {
		t.Errorf("got %+v, want a non-zero exit and no decision", r)
	}
}

// finishedStoryDir writes a plans/stories tree holding one story whose steps
// and criteria are all done but whose status is still in_progress, and
// returns the directory holding both.
func finishedStoryDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	plans := filepath.Join(root, "docs", "plans")
	stories := filepath.Join(root, "docs", "stories")
	for _, d := range []string{plans, stories} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	story := `---
id: 0001-abc
title: Finished story
purpose: Prove the review
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

Done.

## Steps

### 1: Only step

Do it.

## Acceptance Criteria

- [x] It works

## Work Log
`
	if err := os.WriteFile(filepath.Join(stories, "0001-abc-feat-finished-story.md"), []byte(story), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestReviewCommandListsFinishedStory(t *testing.T) {
	root := finishedStoryDir(t)
	r := execStdin(t, "", "--plans-dir", filepath.Join(root, "docs", "plans"), "--stories-dir", filepath.Join(root, "docs", "stories"), "review")
	if r.code != 0 {
		t.Fatalf("got %+v", r)
	}
	res := result(t, r)
	stories, _ := res["stories"].([]any)
	if len(stories) != 1 {
		t.Fatalf("stories = %v", res["stories"])
	}
	if s := stories[0].(map[string]any); s["id"] != "0001-abc" || s["status"] != "in_progress" {
		t.Errorf("story = %v", s)
	}
}

func TestHookStatusReviewAsksAboutFinishedStory(t *testing.T) {
	root := finishedStoryDir(t)
	in := `{"hook_event_name":"UserPromptSubmit","cwd":` + strconv.Quote(root) + `}`
	r := execStdin(t, in, "hook", "status-review", "--state-dir", t.TempDir())
	if r.code != 0 {
		t.Fatalf("got %+v", r)
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatalf("stdout is not hook JSON: %q (%v)", r.stdout, err)
	}
	ctx := out.HookSpecificOutput.AdditionalContext
	if out.HookSpecificOutput.HookEventName != "UserPromptSubmit" || !strings.Contains(ctx, "Story 0001-abc") || !strings.Contains(ctx, "Ask the user") {
		t.Errorf("output = %+v", out)
	}
}

func TestHookStatusReviewIsSilentWhenNothingIsOpenOrNoDocuments(t *testing.T) {
	empty := t.TempDir()
	r := execStdin(t, `{"hook_event_name":"SessionStart","cwd":`+strconv.Quote(empty)+`}`, "hook", "status-review", "--state-dir", t.TempDir())
	if r.code != 0 || r.stdout != "" {
		t.Errorf("empty directory: got %+v, want exit 0 and no output", r)
	}
}

func TestHookStatusReviewHonoursEvery(t *testing.T) {
	root := finishedStoryDir(t)
	state := t.TempDir()
	in := `{"hook_event_name":"UserPromptSubmit","cwd":` + strconv.Quote(root) + `}`
	first := execStdin(t, in, "hook", "status-review", "--state-dir", state, "--every", "1h")
	if first.code != 0 || first.stdout == "" {
		t.Fatalf("first run should review: %+v", first)
	}
	second := execStdin(t, in, "hook", "status-review", "--state-dir", state, "--every", "1h")
	if second.code != 0 || second.stdout != "" {
		t.Errorf("second run within the interval should be silent: %+v", second)
	}
	forced := execStdin(t, in, "hook", "status-review", "--state-dir", state)
	if forced.stdout == "" {
		t.Errorf("without --every the review runs again: %+v", forced)
	}
}
