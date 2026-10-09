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

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/ops"
	"github.com/robbiebyrd/clued/plan/service"
	"github.com/robbiebyrd/clued/plan/store/memstore"
)

func connect(t *testing.T, prefix string) (*mcp.ClientSession, *service.Service, *updates) {
	t.Helper()
	svc, err := service.New(config.Default(), memstore.New(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(svc, ops.Default(), &Options{ToolPrefix: prefix})
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
	return sess, svc, u
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
	sess, svc, upd := connect(t, "")
	tools, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]*mcp.Tool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = tl
	}
	for _, want := range []string{"create", "get", "list", "setStatus", "getTemplate", "setProgress", "sync", "watch"} {
		if names[want] == nil {
			t.Errorf("missing tool %s", want)
		}
	}
	if schema, _ := json.Marshal(names["create"].InputSchema); !strings.Contains(string(schema), "frontMatter") {
		t.Errorf("create input schema should inline the plan schema: %.200s", schema)
	}
	if names["get"].Annotations == nil || !names["get"].Annotations.ReadOnlyHint {
		t.Error("read-only hint")
	}

	var input map[string]any
	b, _ := os.ReadFile("../../testdata/create-impl.json")
	json.Unmarshal(b, &input)
	res, body := call(t, sess, "create", map[string]any{"input": input})
	if res.IsError || body["ok"] != true {
		t.Fatalf("create: %+v", body)
	}
	id := body["result"].(map[string]any)["frontMatter"].(map[string]any)["id"].(string)

	res, body = call(t, sess, "get", map[string]any{"plan": "0099-zzz"})
	if !res.IsError || body["error"].(map[string]any)["kind"] != service.KindNotFound {
		t.Errorf("tool error: %+v", body)
	}
	res, body = call(t, sess, "setStatus", map[string]any{"plan": id, "status": "complete"})
	if !res.IsError || body["error"].(map[string]any)["kind"] != service.KindInvalidTransition {
		t.Errorf("workflow through MCP: %+v", body)
	}
	_, body = call(t, sess, "getProgress", map[string]any{"plan": id})
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
	call(t, sess, "delete", map[string]any{"plan": id})
	list, _ = sess.ListResources(t.Context(), nil)
	for _, r := range list.Resources {
		if r.URI == "plan://"+id {
			t.Error("deleted plan still listed")
		}
	}
}

func TestToolPrefix(t *testing.T) {
	sess, _, _ := connect(t, "plan_")
	tools, _ := sess.ListTools(t.Context(), nil)
	for _, tl := range tools.Tools {
		if !strings.HasPrefix(tl.Name, "plan_") {
			t.Errorf("tool %s lacks prefix", tl.Name)
		}
	}
}
