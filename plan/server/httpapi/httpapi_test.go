package httpapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/ops"
	"github.com/robbiebyrd/clued/plan/service"
	"github.com/robbiebyrd/clued/plan/store/memstore"
)

func newServer(t *testing.T) (*httptest.Server, *service.Service) {
	t.Helper()
	svc, err := service.New(config.Default(), memstore.New(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(Handler(svc, ops.Default()))
	t.Cleanup(ts.Close)
	return ts, svc
}

type envelope struct {
	OK      bool            `json:"ok"`
	Result  json.RawMessage `json:"result"`
	Error   *service.Error  `json:"error"`
	Warning *service.Error  `json:"warning"`
}

func do(t *testing.T, method, url string, rest ...string) (int, envelope, string) {
	t.Helper()
	body := ""
	var headers []string
	if len(rest) > 0 {
		body, headers = rest[0], rest[1:]
	}
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env envelope
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		json.Unmarshal(raw, &env)
		var buf bytes.Buffer
		if json.Compact(&buf, env.Result) == nil {
			env.Result = buf.Bytes()
		}
	}
	return resp.StatusCode, env, string(raw)
}

func TestRESTFlow(t *testing.T) {
	ts, _ := newServer(t)
	base := ts.URL + Prefix
	input, _ := os.ReadFile("../../testdata/create-impl.json")

	// Create with the bare plan input.
	code, env, raw := do(t, "POST", base+"/plans", string(input))
	if code != 201 || !env.OK {
		t.Fatalf("create: %d %s", code, raw)
	}
	var plan struct {
		Path        string `json:"path"`
		FrontMatter struct{ ID, Status string } `json:"frontMatter"`
	}
	json.Unmarshal(env.Result, &plan)
	id := plan.FrontMatter.ID

	// Create through the generic op route with {input, template}.
	code, _, raw = do(t, "POST", base+"/ops/create", `{"input":`+string(input)+`}`)
	if code != 201 {
		t.Fatalf("ops create: %d %s", code, raw)
	}
	// Validation error → 400 with problems.
	code, env, _ = do(t, "POST", base+"/plans", `{"frontMatter":{"title":"x","type":"dsgn","priority":"9"},"body":{"summary":{"goal":"g","problem":"p"}}}`)
	if code != 400 || env.Error == nil || env.Error.Kind != service.KindValidation {
		t.Errorf("validation: %d %+v", code, env.Error)
	}

	code, env, _ = do(t, "GET", base+"/plans?type=impl")
	var list []json.RawMessage
	json.Unmarshal(env.Result, &list)
	if code != 200 || len(list) != 2 {
		t.Errorf("list: %d %d", code, len(list))
	}
	code, _, _ = do(t, "GET", base+"/plans?status=bogus")
	if code != 400 {
		t.Errorf("bad filter: %d", code)
	}
	code, env, _ = do(t, "GET", base+"/plans/"+id)
	if code != 200 || !env.OK {
		t.Errorf("get: %d", code)
	}
	code, _, _ = do(t, "GET", base+"/plans/0099-zzz")
	if code != 404 {
		t.Errorf("missing: %d", code)
	}
	code, _, _ = do(t, "GET", base+"/plans/"+plan.Path+"/front-matter")
	if code != 200 {
		t.Errorf("by path: %d", code)
	}
	code, _, raw = do(t, "GET", base+"/plans/"+id+"/content")
	if code != 200 || !strings.HasPrefix(raw, "# Add plan service") {
		t.Errorf("content: %d %q", code, raw[:30])
	}
	code, env, _ = do(t, "GET", base+"/plans/"+id+"/content", "", "Accept", "application/json")
	if code != 200 || !strings.HasPrefix(string(env.Result), `"# Add plan service`) {
		t.Errorf("content json: %d %s", code, env.Result[:30])
	}
	code, _, _ = do(t, "PUT", base+"/plans/"+id+"/content", "## Replaced\n", "Content-Type", "text/markdown")
	if code != 200 {
		t.Errorf("update text: %d", code)
	}
	code, _, raw = do(t, "PUT", base+"/plans/"+id+"/content", `{"content":"## Via JSON\n\n## Phase 1: X\n"}`, "Content-Type", "application/json")
	if code != 200 {
		t.Errorf("update json: %d %s", code, raw)
	}
	code, env, _ = do(t, "PUT", base+"/plans/"+id+"/status", `{"status":"complete"}`)
	if code != 409 || env.Error.Kind != service.KindInvalidTransition {
		t.Errorf("status: %d %+v", code, env.Error)
	}
	code, _, _ = do(t, "PUT", base+"/plans/"+id+"/status", `{"status":"start"}`)
	if code != 200 {
		t.Errorf("status ok: %d", code)
	}
	code, env, _ = do(t, "GET", base+"/plans/"+id+"/transitions")
	if code != 200 || !strings.Contains(string(env.Result), "complete") {
		t.Errorf("transitions: %d %s", code, env.Result)
	}
	code, env, _ = do(t, "PATCH", base+"/plans/"+id, `{"priority":"P0","effort":null}`)
	if code != 200 || !strings.Contains(string(env.Result), `"priority":"0"`) {
		t.Errorf("patch: %d %s", code, env.Result)
	}
	code, env, _ = do(t, "PATCH", base+"/plans/"+id, `{"id":"x"}`)
	if code != 409 || env.Error.Kind != service.KindImmutableField {
		t.Errorf("patch immutable: %d %+v", code, env.Error)
	}
	code, env, _ = do(t, "GET", base+"/plans/"+id+"/progress")
	if code != 200 || !strings.Contains(string(env.Result), `"sections":["1"]`) {
		t.Errorf("progress: %d %s", code, env.Result)
	}
	code, env, _ = do(t, "GET", base+"/plans/"+id+"/validate")
	if code != 200 || !strings.Contains(string(env.Result), `"valid":false`) {
		t.Errorf("validate (dangling progress keys): %d %s", code, env.Result)
	}
	code, _, _ = do(t, "POST", base+"/ops/setProgress", `{"plan":"`+id+`","section":"1","status":"done"}`)
	if code != 200 {
		t.Errorf("setProgress: %d", code)
	}
	code, env, _ = do(t, "POST", base+"/ops/setProgress", `{"plan":"`+id+`","section":"7","status":"done"}`)
	if code != 400 || env.Error.Kind != service.KindUnknownSection {
		t.Errorf("unknown section: %d %+v", code, env.Error)
	}
	code, _, _ = do(t, "GET", base+"/ops/getTransitions?plan="+id)
	if code != 200 {
		t.Errorf("GET read-only op: %d", code)
	}
	code, _, _ = do(t, "GET", base+"/ops/setStatus?plan="+id)
	if code != 400 {
		t.Errorf("GET write op must be refused: %d", code)
	}
	code, env, _ = do(t, "GET", base+"/ops")
	if code != 200 || !strings.Contains(string(env.Result), `"name":"create"`) {
		t.Errorf("ops: %d", code)
	}
	code, _, raw = do(t, "GET", base+"/schema")
	if code != 200 || !strings.Contains(raw, "frontMatter") {
		t.Errorf("schema: %d", code)
	}
	code, env, _ = do(t, "GET", base+"/config")
	if code != 200 || !strings.Contains(string(env.Result), "workflow") {
		t.Errorf("config: %d", code)
	}

	// Templates.
	code, _, raw = do(t, "GET", base+"/templates/default")
	if code != 200 || !strings.Contains(raw, "## Summary") {
		t.Errorf("template: %d", code)
	}
	code, _, _ = do(t, "PUT", base+"/templates/tiny", "# {{ .Title }}\n", "Content-Type", "text/plain")
	if code != 200 {
		t.Errorf("create template: %d", code)
	}
	code, _, _ = do(t, "PUT", base+"/templates/tiny", `{"content":"# {{ .Title }}!\n"}`, "Content-Type", "application/json")
	if code != 200 {
		t.Errorf("update template: %d", code)
	}
	code, env, _ = do(t, "GET", base+"/templates")
	if code != 200 || !strings.Contains(string(env.Result), `"tiny"`) {
		t.Errorf("list templates: %d", code)
	}
	code, _, _ = do(t, "DELETE", base+"/templates/tiny")
	if code != 200 {
		t.Errorf("delete template: %d", code)
	}
	code, _, _ = do(t, "DELETE", base+"/templates/tiny")
	if code != 404 {
		t.Errorf("delete missing template: %d", code)
	}

	// Delete.
	code, _, _ = do(t, "DELETE", base+"/plans/"+id)
	if code != 200 {
		t.Errorf("delete: %d", code)
	}
	code, _, _ = do(t, "GET", base+"/plans/"+id)
	if code != 404 {
		t.Errorf("after delete: %d", code)
	}
	code, _, _ = do(t, "POST", base+"/ops/explode", `{}`)
	if code != 400 {
		t.Errorf("unknown op: %d", code)
	}
}

func TestSSE(t *testing.T) {
	ts, svc := newServer(t)
	resp, err := http.Get(ts.URL + Prefix + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	line, _ := r.ReadString('\n')
	if !strings.HasPrefix(line, "event: ready") {
		t.Fatalf("first line: %q", line)
	}
	input, _ := os.ReadFile("../../testdata/create-dsgn.json")
	go func() {
		time.Sleep(50 * time.Millisecond)
		svc.Create(t.Context(), input, "")
	}()
	deadline := time.After(3 * time.Second)
	got := make(chan string, 1)
	go func() {
		for {
			l, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(l, "event: plan.created") {
				got <- l
				return
			}
		}
	}()
	select {
	case <-got:
	case <-deadline:
		t.Fatal("no plan.created event over SSE")
	}
}
