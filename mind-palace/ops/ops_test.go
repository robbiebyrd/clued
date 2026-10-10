package ops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

func newPalace(t *testing.T) *service.Palace {
	t.Helper()
	p, err := service.NewPalace(config.Default(), memstore.New(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRegistrySchemasAndNames(t *testing.T) {
	regs := All()
	palace := newPalace(t)
	if n := len(regs[kind.Plan].List()); n != 39 {
		t.Errorf("expected every plan operation, got %d", n)
	}
	if n := len(regs[kind.Story].List()); n != 46 {
		t.Errorf("expected every story operation, got %d", n)
	}
	for _, k := range kind.All() {
		r := regs[k.Name]
		svc, _ := palace.Service(k.Name)
		for _, op := range r.List() {
			s, err := op.InputSchema(svc)
			if err != nil || s == nil {
				t.Errorf("%s %s: schema: %v", k.Name, op.Name, err)
				continue
			}
			b, err := json.Marshal(s)
			if err != nil {
				t.Errorf("%s %s: schema marshal: %v", k.Name, op.Name, err)
			}
			if strings.Contains(string(b), `"document"`) {
				t.Errorf("%s %s: schema still exposes the internal document parameter: %s", k.Name, op.Name, b)
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
		if len(desc) != len(r.List()) || desc[0].Name != "create" || desc[0].Kind != k.Name {
			t.Errorf("describe: %d %s %s", len(desc), desc[0].Name, desc[0].Kind)
		}
		// The create schema inlines the kind's schema.
		b, _ := json.Marshal(desc[0].Params)
		if !strings.Contains(string(b), `"frontMatter"`) || strings.Contains(string(b), `"$schema"`) {
			t.Errorf("create schema: %s", b[:200])
		}
	}
	// The identifier parameter is named after the kind; progress uses step for stories.
	get, _ := regs[kind.Plan].Get("get")
	s, _ := get.InputSchema(nil)
	if s.Properties["plan"] == nil || s.Required[0] != "plan" {
		t.Errorf("plan get schema: %+v", s)
	}
	get, _ = regs[kind.Story].Get("setProgress")
	s, _ = get.InputSchema(nil)
	if s.Properties["story"] == nil || s.Properties["step"] == nil || s.Properties["section"] != nil {
		t.Errorf("story setProgress schema: %+v", s.Properties)
	}
	// Story-only and plan-only operations.
	for _, name := range []string{"setPurpose", "setPlanSections", "addFile", "getCriteria", "setCriterion", "appendWorkLog"} {
		if _, ok := regs[kind.Plan].Get(name); ok {
			t.Errorf("plan registry should not have %s", name)
		}
		if _, ok := regs[kind.Story].Get(name); !ok {
			t.Errorf("story registry should have %s", name)
		}
	}
	for _, name := range []string{"addProgressStory", "removeProgressStory"} {
		if _, ok := regs[kind.Story].Get(name); ok {
			t.Errorf("story registry should not have %s", name)
		}
	}
	if _, err := regs.Get("nope"); err == nil {
		t.Error("unknown kind")
	}
}

func TestInvoke(t *testing.T) {
	regs := All()
	palace := newPalace(t)
	r := regs[kind.Plan]
	svc := palace.Plans()
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
	if _, err := r.Invoke(ctx, svc, "get", json.RawMessage(`{"document":"0001-abc"}`)); !service.IsKind(err, service.KindNotFound) {
		t.Errorf("document alias: %v", err)
	}
	if _, err := r.Invoke(ctx, svc, "get", json.RawMessage(`{"plan":"0001-abc","document":"0001-abc"}`)); !service.IsKind(err, service.KindBadRequest) {
		t.Errorf("both names: %v", err)
	}
	if _, err := r.Invoke(ctx, svc, "get", json.RawMessage(`{"story":"0001-abc"}`)); !service.IsKind(err, service.KindBadRequest) {
		t.Errorf("story parameter on the plan registry: %v", err)
	}
	if _, err := r.Invoke(ctx, palace.Stories(), "get", json.RawMessage(`{"plan":"0001-abc"}`)); !service.IsKind(err, service.KindBadRequest) {
		t.Errorf("registry/service kind mismatch: %v", err)
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
	res, err = r.Invoke(ctx, svc, "list", json.RawMessage(`{"plan":"0009-zzz"}`))
	if err != nil {
		t.Fatal(err)
	}
	if list, ok := res.Value.([]service.Summary); !ok || len(list) != 0 {
		t.Errorf("list filter by linked plan must not be rewritten to the identifier: %#v", res.Value)
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

	// Stories: step alias and story-only operations.
	sr, ss := regs[kind.Story], palace.Stories()
	sin := `{"input":{"frontMatter":{"title":"Via ops","type":"bug","priority":"P3"},"body":{"problemStatement":{"statement":"s"},"steps":[{"number":"1","name":"a","description":"d"}],"acceptanceCriteria":[{"text":"ok"}]}}}`
	res, err = sr.Invoke(ctx, ss, "create", json.RawMessage(sin))
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(res.Value)
	json.Unmarshal(b, &created)
	id := created.FrontMatter.ID
	if _, err := sr.Invoke(ctx, ss, "setProgress", json.RawMessage(`{"story":"`+id+`","step":"1","status":"done"}`)); err != nil {
		t.Errorf("step alias: %v", err)
	}
	if _, err := sr.Invoke(ctx, ss, "setProgress", json.RawMessage(`{"story":"`+id+`","section":"1","status":"done"}`)); err != nil {
		t.Errorf("internal name still accepted: %v", err)
	}
	if _, err := sr.Invoke(ctx, ss, "setProgress", json.RawMessage(`{"story":"`+id+`","step":"9","status":"done"}`)); !service.IsKind(err, service.KindUnknownStep) {
		t.Errorf("unknown step: %v", err)
	}
	res, err = sr.Invoke(ctx, ss, "getCriteria", json.RawMessage(`{"story":"`+id+`"}`))
	if err != nil || res.Value.(*service.CriteriaReport).Open != 1 {
		t.Errorf("getCriteria: %v %+v", err, res)
	}
	res, err = sr.Invoke(ctx, ss, "setCriterion", json.RawMessage(`{"story":"`+id+`","position":1,"state":"done"}`))
	if err != nil {
		t.Errorf("setCriterion: %v", err)
	}
	if _, err := sr.Invoke(ctx, ss, "setCriterion", json.RawMessage(`{"story":"`+id+`","position":5,"state":"done"}`)); !service.IsKind(err, service.KindUnknownCriterion) {
		t.Errorf("unknown criterion: %v", err)
	}
	if _, err := sr.Invoke(ctx, ss, "appendWorkLog", json.RawMessage(`{"story":"`+id+`","entry":"did it"}`)); err != nil {
		t.Errorf("appendWorkLog: %v", err)
	}
}
