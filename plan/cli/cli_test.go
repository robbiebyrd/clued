package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/robbiebyrd/clued/plan/store/filestore"
	_ "github.com/robbiebyrd/clued/plan/store/memstore"
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
	root.SetArgs(append([]string{"--dir", dir}, args...))
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

	r := exec(t, dir, "create")
	if r.code != 2 || !strings.Contains(r.stderr, "--input is required") {
		t.Errorf("create without input: %+v", r)
	}
	r = exec(t, dir, "create", "--input", input)
	if r.code != 0 {
		t.Fatalf("create: %+v", r)
	}
	id := result(t, r)["frontMatter"].(map[string]any)["id"].(string)
	if _, err := os.Stat(filepath.Join(dir, id+"-impl-add-plan-service.md")); err != nil {
		t.Error("plan file not written to --dir")
	}
	r = exec(t, dir, "create", "--input", `{"frontMatter":{"title":"Inline","type":"drft","priority":"1"},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`)
	if r.code != 0 {
		t.Errorf("inline create: %+v", r)
	}
	r = exec(t, dir, "create", "--input", `{"frontMatter":{"title":"Bad","type":"drft","priority":"7"},"body":{"summary":{"goal":"g","problem":"p"}}}`)
	if r.code != 3 || !strings.Contains(r.stderr, "ValidationError") {
		t.Errorf("validation exit code: %+v", r)
	}
	r = exec(t, dir, "get", "0099-zzz")
	if r.code != 4 {
		t.Errorf("not found exit code: %+v", r)
	}
	r = exec(t, dir, "get", id, "--content")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "# Add plan service\n") {
		t.Errorf("raw content: %+v", r)
	}
	r = exec(t, dir, "get", id, "--front-matter", "--format", "yaml")
	if r.code != 0 || !strings.Contains(r.stdout, "result:\n") || !strings.Contains(r.stdout, "id: "+id) {
		t.Errorf("yaml output: %+v", r)
	}
	r = exec(t, dir, "list", "--type", "impl", "--compact")
	if r.code != 0 || strings.Count(r.stdout, "\n") != 1 || !strings.Contains(r.stdout, id) {
		t.Errorf("list compact: %+v", r)
	}
	r = exec(t, dir, "set-status", id, "done")
	if r.code != 5 {
		t.Errorf("invalid transition exit code: %+v", r)
	}
	r = exec(t, dir, "set-status", id, "done", "--force")
	if r.code != 0 || result(t, r)["frontMatter"].(map[string]any)["completed"] == "" {
		t.Errorf("forced complete: %+v", r)
	}
	r = exec(t, dir, "patch", id, "--priority", "P0", "--json", `{"id":"nope"}`)
	if r.code != 6 {
		t.Errorf("immutable exit code: %+v", r)
	}
	r = exec(t, dir, "set-progress", id, "4.4", "done")
	if r.code != 7 {
		t.Errorf("unknown section exit code: %+v", r)
	}
	r = exec(t, dir, "link", id, id, "blocks")
	if r.code != 2 {
		t.Errorf("self link exit code: %+v", r)
	}
	other := result(t, exec(t, dir, "create", "--input", input))["frontMatter"].(map[string]any)["id"].(string)
	if r = exec(t, dir, "link", other, id, "depends"); r.code != 0 {
		t.Errorf("link: %+v", r)
	}
	if r = exec(t, dir, "delete", id); r.code != 8 {
		t.Errorf("linked plan exit code: %+v", r)
	}
	if r = exec(t, dir, "delete", id, "--force"); r.code != 0 {
		t.Errorf("forced delete: %+v", r)
	}
	// The forced delete left a dangling link that validate reports.
	r = exec(t, dir, "validate", other)
	if r.code != 3 || !strings.Contains(r.stdout, "does not exist") {
		t.Errorf("validate with dangling link: %+v", r)
	}
	exec(t, dir, "unlink", other, id)
	if r = exec(t, dir, "validate", other); r.code != 0 {
		t.Errorf("validate: %+v", r)
	}
	r = exec(t, dir, "template", "get")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "# {{ .Title }}") {
		t.Errorf("template get raw: %+v", r)
	}
	r = exec(t, dir, "template", "create", "tiny", "--content", input) // any file works as content
	if r.code != 0 {
		t.Errorf("template create: %+v", r)
	}
	r = exec(t, dir, "template", "create", "tiny", "--content", input)
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
	for _, cmd := range []string{"ops", "config", "schema", "stores"} {
		if r = exec(t, dir, cmd); r.code != 0 {
			t.Errorf("%s: %+v", cmd, r)
		}
	}
	r = exec(t, dir, "sync", "--from", "file", "--to", "file")
	if r.code != 2 {
		t.Errorf("sync with a single store: %+v", r)
	}
}

func TestConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "plan.config.yaml")
	os.WriteFile(cfgPath, []byte("plansDir: "+filepath.Join(dir, "p")+"\nstorage:\n  - {name: file, kind: file}\n  - {name: mem, kind: memory}\n"), 0o644)
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
	root.SetArgs([]string{"--config", filepath.Join(dir, "missing.yaml"), "list"})
	if err := root.Execute(); err == nil {
		t.Error("missing config should fail")
	}
}
