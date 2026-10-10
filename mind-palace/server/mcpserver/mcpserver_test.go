package mcpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/ops"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

func connect(t *testing.T, prefix string) (*mcp.ClientSession, *service.Palace, *updates) {
	t.Helper()
	palace, err := service.NewPalace(config.Default(), memstore.New(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(palace, ops.All(), &Options{ToolPrefix: prefix})
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	u := &updates{}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, &mcp.ClientOptions{
		ResourceUpdatedHandler: func(ctx context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
			u.add(req.Params.URI)
		},
	})
	sess, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess, palace, u
}

type updates struct {
	mu   sync.Mutex
	uris []string
}

func (u *updates) add(uri string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.uris = append(u.uris, uri)
}

func (u *updates) has(uri string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, x := range u.uris {
		if x == uri {
			return true
		}
	}
	return false
}

func call(t *testing.T, sess *mcp.ClientSession, name string, args any) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	body := map[string]any{}
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			json.Unmarshal([]byte(tc.Text), &body)
		}
	}
	return res, body
}

func TestToolsResourcesAndSubscriptions(t *testing.T) {
	sess, palace, upd := connect(t, "")
	svc := palace.Plans()
	tools, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]*mcp.Tool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = tl
	}
	for _, want := range []string{"plan_create", "plan_get", "plan_list", "plan_setStatus", "plan_getTemplate", "plan_setProgress", "story_create", "story_getCriteria", "story_appendWorkLog", "story_getSchema", "sync", "getConfig", "watch"} {
		if names[want] == nil {
			t.Errorf("missing tool %s", want)
		}
	}
	for _, unwanted := range []string{"create", "plan_sync", "story_sync", "plan_getCriteria", "story_addProgressStory"} {
		if names[unwanted] != nil {
			t.Errorf("unexpected tool %s", unwanted)
		}
	}
	if schema, _ := json.Marshal(names["plan_create"].InputSchema); !strings.Contains(string(schema), "frontMatter") {
		t.Errorf("create input schema should inline the plan schema: %.200s", schema)
	}
	if schema, _ := json.Marshal(names["story_create"].InputSchema); !strings.Contains(string(schema), "problemStatement") {
		t.Errorf("story create input schema should inline the story schema: %.200s", schema)
	}
	if schema, _ := json.Marshal(names["story_get"].InputSchema); !strings.Contains(string(schema), `"story"`) {
		t.Errorf("story tools name their identifier story: %s", schema)
	}
	if names["plan_get"].Annotations == nil || !names["plan_get"].Annotations.ReadOnlyHint {
		t.Error("read-only hint")
	}
	if names["plan_delete"].Annotations == nil || names["plan_delete"].Annotations.DestructiveHint == nil || !*names["plan_delete"].Annotations.DestructiveHint {
		t.Error("destructive hint")
	}

	var input map[string]any
	b, _ := os.ReadFile("../../testdata/create-impl.json")
	json.Unmarshal(b, &input)
	res, body := call(t, sess, "plan_create", map[string]any{"input": input})
	if res.IsError || body["ok"] != true {
		t.Fatalf("create: %+v", body)
	}
	id := body["result"].(map[string]any)["frontMatter"].(map[string]any)["id"].(string)

	res, body = call(t, sess, "plan_get", map[string]any{"plan": "0099-zzz"})
	if !res.IsError || body["error"].(map[string]any)["kind"] != service.KindNotFound {
		t.Errorf("tool error: %+v", body)
	}
	res, body = call(t, sess, "plan_setStatus", map[string]any{"plan": id, "status": "complete"})
	if !res.IsError || body["error"].(map[string]any)["kind"] != service.KindInvalidTransition {
		t.Errorf("workflow through MCP: %+v", body)
	}
	_, body = call(t, sess, "plan_getProgress", map[string]any{"plan": id})
	if body["ok"] != true {
		t.Errorf("getProgress: %+v", body)
	}

	// Resources.
	list, err := sess.ListResources(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range list.Resources {
		if r.URI == "plan://"+id {
			found = true
		}
	}
	if !found {
		t.Errorf("plan resource not listed: %+v", list.Resources)
	}
	rr, err := sess.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "plan://" + id})
	if err != nil || !strings.HasPrefix(rr.Contents[0].Text, "---\nid: "+id) {
		t.Errorf("read markdown resource: %v %+v", err, rr)
	}
	rr, err = sess.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "plan://" + id + "/json"})
	if err != nil || !strings.Contains(rr.Contents[0].Text, `"frontMatter"`) {
		t.Errorf("read json resource: %v", err)
	}
	if _, err := sess.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "plan://0099-zzz"}); err == nil {
		t.Error("missing resource should error")
	}
	if err := sess.Subscribe(t.Context(), &mcp.SubscribeParams{URI: "plan://0099-zzz"}); err == nil {
		t.Error("subscribe to missing plan should error")
	}
	if err := sess.Subscribe(t.Context(), &mcp.SubscribeParams{URI: "plan://" + id}); err != nil {
		t.Fatal(err)
	}
	svc.SetPriority(t.Context(), id, "P0")
	deadline := time.Now().Add(3 * time.Second)
	for !upd.has("plan://"+id) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !upd.has("plan://" + id) {
		t.Error("no resources/updated notification")
	}

	// watch returns on the first event, or times out.
	_, body = call(t, sess, "watch", map[string]any{"plans": []string{id}, "timeoutSeconds": 0.3})
	if body["result"].(map[string]any)["timedOut"] != true {
		t.Errorf("watch should time out: %+v", body)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		svc.SetProgress(context.Background(), id, "1.1", "done")
	}()
	_, body = call(t, sess, "watch", map[string]any{"plans": []string{id}, "timeoutSeconds": 5})
	evs := body["result"].(map[string]any)["events"].([]any)
	if len(evs) != 1 || evs[0].(map[string]any)["operation"] != "setProgress" {
		t.Errorf("watch events: %+v", body)
	}

	// Delete removes the resource.
	call(t, sess, "plan_delete", map[string]any{"plan": id})
	list, _ = sess.ListResources(t.Context(), nil)
	for _, r := range list.Resources {
		if r.URI == "plan://"+id {
			t.Error("deleted plan still listed")
		}
	}
}

func TestToolPrefix(t *testing.T) {
	sess, _, _ := connect(t, "mp_")
	tools, _ := sess.ListTools(t.Context(), nil)
	for _, tl := range tools.Tools {
		if !strings.HasPrefix(tl.Name, "mp_") {
			t.Errorf("tool %s lacks prefix", tl.Name)
		}
	}
}

func TestStoryToolsAndResources(t *testing.T) {
	sess, palace, upd := connect(t, "")
	var input map[string]any
	b, _ := os.ReadFile("../../testdata/create-story.json")
	json.Unmarshal(b, &input)
	res, body := call(t, sess, "story_create", map[string]any{"input": input})
	if res.IsError || body["ok"] != true {
		t.Fatalf("story create: %+v", body)
	}
	id := body["result"].(map[string]any)["frontMatter"].(map[string]any)["id"].(string)
	res, body = call(t, sess, "story_setStatus", map[string]any{"story": id, "status": "complete", "force": true})
	if !res.IsError || body["error"].(map[string]any)["kind"] != service.KindIncompleteCriteria {
		t.Errorf("criteria gate through MCP: %+v", body)
	}
	_, body = call(t, sess, "story_setProgress", map[string]any{"story": id, "step": "1", "status": "done"})
	if body["ok"] != true {
		t.Errorf("step alias through MCP: %+v", body)
	}
	rr, err := sess.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "story://" + id})
	if err != nil || !strings.Contains(rr.Contents[0].Text, "purpose: ") {
		t.Errorf("read story resource: %v", err)
	}
	if _, err := sess.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "plan://" + id}); err == nil {
		t.Error("story id must not resolve as a plan resource")
	}
	if err := sess.Subscribe(t.Context(), &mcp.SubscribeParams{URI: "story://" + id}); err != nil {
		t.Fatal(err)
	}
	palace.Stories().AppendWorkLog(t.Context(), id, "progress")
	deadline := time.Now().Add(3 * time.Second)
	for !upd.has("story://"+id) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !upd.has("story://" + id) {
		t.Error("no resources/updated notification for the story")
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		palace.Stories().SetCriterion(context.Background(), id, 1, "done")
	}()
	_, body = call(t, sess, "watch", map[string]any{"stories": []string{id}, "timeoutSeconds": 5})
	evs := body["result"].(map[string]any)["events"].([]any)
	if len(evs) != 1 || evs[0].(map[string]any)["operation"] != "setCriterion" || evs[0].(map[string]any)["kind"] != "story" {
		t.Errorf("watch story events: %+v", body)
	}
	_, body = call(t, sess, "watch", map[string]any{"plans": []string{"x"}, "stories": []string{id}})
	if body["ok"] != false {
		t.Errorf("watch with both kinds: %+v", body)
	}
}
