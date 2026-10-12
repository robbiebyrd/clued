package mcpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/ops"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

// headerTransport adds fixed headers to every request of an http.Client.
type headerTransport struct {
	header http.Header
	next   http.RoundTripper
}

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h.header {
		r.Header[k] = v
	}
	return h.next.RoundTrip(r)
}

func clientWithHeader(key, value string) *http.Client {
	h := http.Header{}
	if value != "" {
		h.Set(key, value)
	}
	return &http.Client{Transport: headerTransport{header: h, next: http.DefaultTransport}}
}

// multiServer serves NewMulti over palaces picked by the X-Palace header:
// "boom" is a plain error, an unknown name a 403 StatusError.
func multiServer(t *testing.T, palaces map[string]*service.Palace) (*httptest.Server, *Multi) {
	t.Helper()
	m := NewMulti(func(r *http.Request) (*service.Palace, error) {
		name := r.Header.Get("X-Palace")
		if name == "boom" {
			return nil, errors.New("database down")
		}
		if p, ok := palaces[name]; ok {
			return p, nil
		}
		return nil, &httpapi.StatusError{Status: http.StatusForbidden, Kind: "forbidden", Message: "not yours"}
	}, ops.All(), nil)
	t.Cleanup(m.Close)
	ts := httptest.NewServer(m.Handler())
	t.Cleanup(ts.Close)
	return ts, m
}

func newPalace(t *testing.T) *service.Palace {
	t.Helper()
	palace, err := service.NewPalace(config.Default(), memstore.New(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	return palace
}

func connectMulti(t *testing.T, ts *httptest.Server, palace string) (*mcp.ClientSession, error) {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: ts.URL, HTTPClient: clientWithHeader("X-Palace", palace)}, nil)
	if err == nil {
		t.Cleanup(func() { sess.Close() })
	}
	return sess, err
}

func TestMultiServesOnePalacePerRequest(t *testing.T) {
	a, b := newPalace(t), newPalace(t)
	ts, m := multiServer(t, map[string]*service.Palace{"a": a, "b": b})
	sa, err := connectMulti(t, ts, "a")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := connectMulti(t, ts, "b")
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	raw, _ := os.ReadFile("../../testdata/create-impl.json")
	json.Unmarshal(raw, &input)
	if res, body := call(t, sa, "plan_create", map[string]any{"input": input}); res.IsError || body["ok"] != true {
		t.Fatalf("plan_create on a: %+v", body)
	}
	if _, body := call(t, sa, "plan_list", map[string]any{}); len(body["result"].([]any)) != 1 {
		t.Errorf("a lists %v", body["result"])
	}
	if _, body := call(t, sb, "plan_list", map[string]any{}); len(body["result"].([]any)) != 0 {
		t.Errorf("b lists %v", body["result"])
	}
	if docs, _ := b.Plans().List(t.Context(), service.ListFilter{}); len(docs) != 0 {
		t.Errorf("palace b has %d plans", len(docs))
	}
	// Reconnecting to the same palace reuses its server; a new palace adds one.
	if _, err := connectMulti(t, ts, "a"); err != nil {
		t.Fatal(err)
	}
	if n := m.Len(); n != 2 {
		t.Errorf("Multi holds %d servers, want 2", n)
	}
}

// postJSONRPC posts one JSON-RPC message with the given headers and returns
// the status, the response headers and the body.
func postJSONRPC(t *testing.T, url, body string, header map[string]string) (int, http.Header, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(raw)
}

const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"raw","version":"0"}}}`

func TestMultiResolverErrorsAnswerInTheEnvelope(t *testing.T) {
	ts, _ := multiServer(t, map[string]*service.Palace{"a": newPalace(t)})
	status, _, body := postJSONRPC(t, ts.URL, initializeBody, nil)
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if status != http.StatusForbidden || json.Unmarshal([]byte(body), &env) != nil || env.OK || env.Error.Kind != "forbidden" || env.Error.Message != "not yours" {
		t.Errorf("forbidden: %d %s", status, body)
	}
	if status, _, body := postJSONRPC(t, ts.URL, initializeBody, map[string]string{"X-Palace": "boom"}); status != http.StatusInternalServerError || !strings.Contains(body, service.KindStorage) {
		t.Errorf("plain error: %d %s", status, body)
	}
	if _, err := connectMulti(t, ts, ""); err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("client connect: %v", err)
	}
}

func TestMultiSessionIsBoundToItsPalace(t *testing.T) {
	ts, _ := multiServer(t, map[string]*service.Palace{"a": newPalace(t), "b": newPalace(t)})
	status, header, body := postJSONRPC(t, ts.URL, initializeBody, map[string]string{"X-Palace": "a"})
	sid := header.Get("Mcp-Session-Id")
	if status != http.StatusOK || sid == "" {
		t.Fatalf("initialize: %d %v %s", status, header, body)
	}
	list := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	if status, _, body := postJSONRPC(t, ts.URL, list, map[string]string{"X-Palace": "b", "Mcp-Session-Id": sid}); status != http.StatusNotFound || !strings.Contains(body, `"not_found"`) {
		t.Errorf("session of a used on b: %d %s", status, body)
	}
	if status, _, body := postJSONRPC(t, ts.URL, list, map[string]string{"X-Palace": "a", "Mcp-Session-Id": sid}); status == http.StatusNotFound {
		t.Errorf("session of a used on a: %d %s", status, body)
	}
	// The resolver still runs first: a forbidden palace is 403 even with a session.
	if status, _, _ := postJSONRPC(t, ts.URL, list, map[string]string{"Mcp-Session-Id": sid}); status != http.StatusForbidden {
		t.Errorf("forbidden with a session: %d", status)
	}
}
