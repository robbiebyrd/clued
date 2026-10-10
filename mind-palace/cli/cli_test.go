package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/robbiebyrd/clued/mind-palace/store/filestore"
	_ "github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

type run struct {
	code   int
	stdout string
	stderr string
}

func exec(t *testing.T, dir string, args ...string) run {
	t.Helper()
	app := New()
	var out, errb bytes.Buffer
	app.Stdout, app.Stderr, app.Stdin = &out, &errb, strings.NewReader("")
	root := app.Root()
	root.SetArgs(append([]string{"--plans-dir", dir, "--stories-dir", filepath.Join(dir, "..", "stories")}, args...))
	err := root.Execute()
	app.close()
	code := 0
	if err != nil {
		code = ExitCode(err)
	}
	return run{code: code, stdout: out.String(), stderr: errb.String()}
}

func result(t *testing.T, r run) map[string]any {
	t.Helper()
	var env struct {
		OK     bool           `json:"ok"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &env); err != nil {
		t.Fatalf("not JSON: %q (%v)", r.stdout, err)
	}
	return env.Result
}

func TestCLIFlow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plans")
	wd, _ := os.Getwd()
	input := filepath.Join(wd, "..", "testdata", "create-impl.json")

	r := exec(t, dir, "plan", "create")
	if r.code != 2 || !strings.Contains(r.stderr, "--input is required") {
		t.Errorf("create without input: %+v", r)
	}
	r = exec(t, dir, "plan", "create", "--input", input)
	if r.code != 0 {
		t.Fatalf("create: %+v", r)
	}
	id := result(t, r)["frontMatter"].(map[string]any)["id"].(string)
	if _, err := os.Stat(filepath.Join(dir, id+"-impl-add-plan-service.md")); err != nil {
		t.Error("plan file not written to --dir")
	}
	r = exec(t, dir, "plan", "create", "--input", `{"frontMatter":{"title":"Inline","type":"drft","priority":"1"},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`)
	if r.code != 0 {
		t.Errorf("inline create: %+v", r)
	}
	r = exec(t, dir, "plan", "create", "--input", `{"frontMatter":{"title":"Bad","type":"drft","priority":"7"},"body":{"summary":{"goal":"g","problem":"p"}}}`)
	if r.code != 3 || !strings.Contains(r.stderr, "ValidationError") {
		t.Errorf("validation exit code: %+v", r)
	}
	r = exec(t, dir, "plan", "get", "0099-zzz")
	if r.code != 4 {
		t.Errorf("not found exit code: %+v", r)
	}
	r = exec(t, dir, "plan", "get", id, "--content")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "# Add plan service\n") {
		t.Errorf("raw content: %+v", r)
	}
	r = exec(t, dir, "plan", "get", id, "--front-matter", "--format", "yaml")
	if r.code != 0 || !strings.Contains(r.stdout, "result:\n") || !strings.Contains(r.stdout, "id: "+id) {
		t.Errorf("yaml output: %+v", r)
	}
	r = exec(t, dir, "plan", "list", "--type", "impl", "--compact")
	if r.code != 0 || strings.Count(r.stdout, "\n") != 1 || !strings.Contains(r.stdout, id) {
		t.Errorf("list compact: %+v", r)
	}
	r = exec(t, dir, "plan", "set-status", id, "done")
	if r.code != 5 {
		t.Errorf("invalid transition exit code: %+v", r)
	}
	r = exec(t, dir, "plan", "set-status", id, "done", "--force")
	if r.code != 0 || result(t, r)["frontMatter"].(map[string]any)["completed"] == "" {
		t.Errorf("forced complete: %+v", r)
	}
	r = exec(t, dir, "plan", "patch", id, "--priority", "P0", "--json", `{"id":"nope"}`)
	if r.code != 6 {
		t.Errorf("immutable exit code: %+v", r)
	}
	r = exec(t, dir, "plan", "set-progress", id, "4.4", "done")
	if r.code != 7 {
		t.Errorf("unknown section exit code: %+v", r)
	}
	r = exec(t, dir, "plan", "link", id, id, "blocks")
	if r.code != 2 {
		t.Errorf("self link exit code: %+v", r)
	}
	other := result(t, exec(t, dir, "plan", "create", "--input", input))["frontMatter"].(map[string]any)["id"].(string)
	if r = exec(t, dir, "plan", "link", other, id, "depends"); r.code != 0 {
		t.Errorf("link: %+v", r)
	}
	if r = exec(t, dir, "plan", "delete", id); r.code != 8 {
		t.Errorf("linked plan exit code: %+v", r)
	}
	if r = exec(t, dir, "plan", "delete", id, "--force"); r.code != 0 {
		t.Errorf("forced delete: %+v", r)
	}
	// The forced delete left a dangling link that validate reports.
	r = exec(t, dir, "plan", "validate", other)
	if r.code != 3 || !strings.Contains(r.stdout, "does not exist") {
		t.Errorf("validate with dangling link: %+v", r)
	}
	exec(t, dir, "plan", "unlink", other, id)
	if r = exec(t, dir, "plan", "validate", other); r.code != 0 {
		t.Errorf("validate: %+v", r)
	}
	r = exec(t, dir, "plan", "template", "get")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "# {{ .Title }}") {
		t.Errorf("template get raw: %+v", r)
	}
	r = exec(t, dir, "plan", "template", "create", "tiny", "--content", input) // any file works as content
	if r.code != 0 {
		t.Errorf("template create: %+v", r)
	}
	r = exec(t, dir, "plan", "template", "create", "tiny", "--content", input)
	if r.code != 9 {
		t.Errorf("conflict exit code: %+v", r)
	}
	r = exec(t, dir, "call", "getTransitions", "--params", `{"plan":"`+other+`"}`)
	if r.code != 0 || !strings.Contains(r.stdout, "transitions") {
		t.Errorf("call: %+v", r)
	}
	r = exec(t, dir, "call", "nope")
	if r.code != 2 {
		t.Errorf("unknown op: %+v", r)
	}
	r = exec(t, dir, "call", "list", "--kind", "story")
	if r.code != 0 || !strings.Contains(r.stdout, `"result": []`) {
		t.Errorf("call with kind: %+v", r)
	}
	for _, cmd := range []string{"ops", "config", "schema", "stores"} {
		if r = exec(t, dir, cmd); r.code != 0 {
			t.Errorf("%s: %+v", cmd, r)
		}
	}
	if r = exec(t, dir, "schema", "--kind", "story"); r.code != 0 || !strings.Contains(r.stdout, "problemStatement") {
		t.Errorf("story schema: %+v", r)
	}
	if r = exec(t, dir, "story", "schema"); r.code != 0 || !strings.Contains(r.stdout, "problemStatement") {
		t.Errorf("story schema via group: %+v", r)
	}
	r = exec(t, dir, "sync", "--from", "file", "--to", "file")
	if r.code != 2 {
		t.Errorf("sync with a single store: %+v", r)
	}
}

func TestConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "plan.config.yaml")
	os.WriteFile(cfgPath, []byte("plansDir: "+filepath.Join(dir, "p")+"\nstories:\n  dir: "+filepath.Join(dir, "s")+"\nstorage:\n  - {name: file, kind: file}\n  - {name: mem, kind: memory}\n"), 0o644)
	app := New()
	var out, errb bytes.Buffer
	app.Stdout, app.Stderr = &out, &errb
	root := app.Root()
	root.SetArgs([]string{"--config", cfgPath, "stores"})
	if err := root.Execute(); err != nil {
		t.Fatalf("%v %s", err, errb.String())
	}
	if !strings.Contains(out.String(), "mem (memory)") {
		t.Errorf("stores from config: %s", out.String())
	}
	out.Reset()
	root = app.Root()
	root.SetArgs([]string{"--config", cfgPath, "sync", "--from", "file", "--to", "mem"})
	if err := root.Execute(); err != nil {
		t.Fatalf("sync: %v %s", err, errb.String())
	}
	if !strings.Contains(out.String(), `"copied"`) {
		t.Errorf("sync output: %s", out.String())
	}
	fresh := New()
	fresh.Stdout, fresh.Stderr = &out, &errb
	root = fresh.Root()
	root.SetArgs([]string{"--config", filepath.Join(dir, "missing.yaml"), "plan", "list"})
	if err := root.Execute(); err == nil {
		t.Error("missing config should fail")
	}
}

func TestStoryCommandsAndKindBinaries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plans")
	wd, _ := os.Getwd()
	planInput := filepath.Join(wd, "..", "testdata", "create-impl.json")
	storyInput := filepath.Join(wd, "..", "testdata", "create-story.json")

	plan := result(t, exec(t, dir, "plan", "create", "--input", planInput))["frontMatter"].(map[string]any)["id"].(string)
	r := exec(t, dir, "story", "create", "--input", storyInput)
	if r.code != 0 {
		t.Fatalf("story create: %+v", r)
	}
	id := result(t, r)["frontMatter"].(map[string]any)["id"].(string)
	if _, err := os.Stat(filepath.Join(dir, "..", "stories", id+"-bugs-fix-empty-payload-crash.md")); err != nil {
		t.Error("story file not written to --stories-dir")
	}
	if r = exec(t, dir, "story", "set-status", id, "complete", "--force"); r.code != 11 {
		t.Errorf("incomplete criteria exit code: %+v", r)
	}
	if r = exec(t, dir, "story", "set-progress", id, "9", "done"); r.code != 12 {
		t.Errorf("unknown step exit code: %+v", r)
	}
	if r = exec(t, dir, "story", "check", id, "5"); r.code != 13 {
		t.Errorf("unknown criterion exit code: %+v", r)
	}
	if r = exec(t, dir, "story", "link-plan", id, plan, "included", "--sections", "1.1,9"); r.code != 7 {
		t.Errorf("unknown section exit code: %+v", r)
	}
	if r = exec(t, dir, "story", "link-plan", id, plan, "included", "--sections", "1.1,2"); r.code != 0 {
		t.Errorf("link-plan: %+v", r)
	}
	r = exec(t, dir, "story", "plan-sections", id, plan)
	if links := result(t, r)["frontMatter"].(map[string]any)["links"].(map[string]any)["plans"].([]any); r.code != 0 || len(links[0].([]any)) != 2 {
		t.Errorf("plan-sections clear: %+v", r)
	}
	if r = exec(t, dir, "plan", "delete", plan); r.code != 8 {
		t.Errorf("plan linked from story exit code: %+v", r)
	}
	other := result(t, exec(t, dir, "story", "create", "--input", storyInput))["frontMatter"].(map[string]any)["id"].(string)
	exec(t, dir, "story", "link", other, id, "parent")
	if r = exec(t, dir, "story", "delete", id); r.code != 14 {
		t.Errorf("linked story exit code: %+v", r)
	}
	for _, args := range [][]string{
		{"story", "set-purpose", id, "Because"},
		{"story", "check", id, "1"}, {"story", "check", id, "2", "not_applicable"},
		{"story", "add-criterion", id, "Dashboard", "--kind", "manual"}, {"story", "remove-criterion", id, "3"},
		{"story", "criteria", id}, {"story", "log", id, "Done"}, {"story", "add-file", id, "a.go"}, {"story", "remove-file", id, "a.go"},
		{"story", "set-repo", id, "--pull-request", "https://github.com/o/p/pull/1"},
		{"story", "set-status", id, "start"}, {"story", "set-progress", id, "1", "done"}, {"story", "set-progress", id, "1.1", "done"},
		{"story", "set-progress", id, "2", "done"}, {"story", "template", "get"}, {"story", "ops"}, {"story", "list", "--plan", plan},
	} {
		if r = exec(t, dir, args...); r.code != 0 {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if st := result(t, exec(t, dir, "story", "get", id))["frontMatter"].(map[string]any); st["status"] != "complete" || st["started"] == nil {
		t.Errorf("auto-complete through the CLI: %+v", st)
	}
	if r = exec(t, dir, "story", "template", "delete", "default"); r.code != 9 {
		t.Errorf("protected default template: %+v", r)
	}
	if r = exec(t, dir, "plan", "set-purpose", plan, "x"); r.code != 2 {
		t.Errorf("plans have no set-purpose: %+v", r)
	}
	// The kind binaries route document commands to their group and pass top-level commands through.
	for _, tc := range []struct {
		kind string
		args []string
		want string
	}{
		{"story", []string{"--plans-dir", dir, "--stories-dir", filepath.Join(dir, "..", "stories"), "get", id}, `"kind": "story"`},
		{"plan", []string{"--plans-dir", dir, "--stories-dir", filepath.Join(dir, "..", "stories"), "get", plan}, `"kind": "plan"`},
		{"plan", []string{"--plans-dir", dir, "--stories-dir", filepath.Join(dir, "..", "stories"), "stores"}, `"plugins"`},
		{"story", []string{"--plans-dir", dir, "--stories-dir", filepath.Join(dir, "..", "stories"), "story", "list"}, `"kind": "story"`},
	} {
		app := New()
		var out, errb bytes.Buffer
		app.Stdout, app.Stderr, app.Stdin = &out, &errb, strings.NewReader("")
		if code := kindMain(app, tc.kind, tc.args); code != 0 || !strings.Contains(out.String(), tc.want) {
			t.Errorf("%s %v: code %d out %s err %s", tc.kind, tc.args, code, out.String(), errb.String())
		}
	}
}
