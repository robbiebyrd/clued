package ops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/service"
	"github.com/robbiebyrd/clued/plan/store/memstore"
)

func newSvc(t *testing.T) *service.Service {
	t.Helper()
	svc, err := service.New(config.Default(), memstore.New(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestRegistrySchemasAndNames(t *testing.T) {
	r := Default()
	svc := newSvc(t)
	if len(r.List()) != 39 {
		t.Errorf("expected every spec operation, got %d", len(r.List()))
	}
	for _, op := range r.List() {
		s, err := op.InputSchema(svc)
		if err != nil || s == nil {
			t.Errorf("%s: schema: %v", op.Name, err)
			continue
		}
		if _, err := json.Marshal(s); err != nil {
			t.Errorf("%s: schema marshal: %v", op.Name, err)
		}
	}
	for _, alias := range []string{"getFrontMatter", "get-front-matter", "get_front_matter", "GETFRONTMATTER"} {
		if op, ok := r.Get(alias); !ok || op.Name != "getFrontMatter" {
			t.Errorf("alias %q not resolved", alias)
		}
	}
	if _, ok := r.Get("explode"); ok {
		t.Error("unknown op resolved")
	}
	desc := r.Describe(svc)
	if len(desc) != len(r.List()) || desc[0].Name != "create" {
		t.Errorf("describe: %d %s", len(desc), desc[0].Name)
	}
	// The create schema inlines the plan schema.
	b, _ := json.Marshal(desc[0].Params)
	if !strings.Contains(string(b), `"frontMatter"`) || strings.Contains(string(b), `"$schema"`) {
		t.Errorf("create schema: %s", b[:200])
	}
}

func TestInvoke(t *testing.T) {
	r := Default()
	svc := newSvc(t)
	ctx := context.Background()
	if _, err := r.Invoke(ctx, svc, "nope", nil); !service.IsKind(err, service.KindBadRequest) {
		t.Errorf("unknown op: %v", err)
	}
	if _, err := r.Invoke(ctx, svc, "get", json.RawMessage(`{"plan":"x","bogus":1}`)); !service.IsKind(err, service.KindBadRequest) {
		t.Errorf("unknown field: %v", err)
	}
	if _, err := r.Invoke(ctx, svc, "get", json.RawMessage(`{"plan":"0001-abc"}`)); !service.IsKind(err, service.KindNotFound) {
		t.Errorf("not found: %v", err)
	}
	in := `{"input":{"frontMatter":{"title":"Via ops","type":"drft","priority":"P3"},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}}`
	res, err := r.Invoke(ctx, svc, "create", json.RawMessage(in))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.Value)
	var created struct {
		FrontMatter struct{ ID, Priority string } `json:"frontMatter"`
	}
	json.Unmarshal(b, &created)
	if created.FrontMatter.Priority != "3" {
		t.Errorf("create via ops: %s", b)
	}
	res, err = r.Invoke(ctx, svc, "list", nil)
	if err != nil {
		t.Fatal(err)
	}
	if list, ok := res.Value.([]service.Summary); !ok || len(list) != 1 {
		t.Errorf("list: %#v", res.Value)
	}
	res, err = r.Invoke(ctx, svc, "setProgress", json.RawMessage(`{"plan":"`+created.FrontMatter.ID+`","section":"1","status":"done"}`))
	if !service.IsKind(err, service.KindUnknownSection) {
		t.Errorf("unknown section through ops: %v", err)
	}
	res, err = r.Invoke(ctx, svc, "getConfig", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if m := res.Value.(map[string]any); m["stores"] == nil {
		t.Error("getConfig")
	}
	res, _ = r.Invoke(ctx, svc, "getSchema", nil)
	if !strings.Contains(string(res.Value.(json.RawMessage)), "frontMatter") {
		t.Error("getSchema")
	}
}
