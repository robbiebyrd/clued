package ws

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/ops"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

func newPalace(t *testing.T) *service.Palace {
	t.Helper()
	palace, err := service.NewPalace(config.Default(), memstore.New(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	return palace
}

// resolvingServer serves ResolvingHandler with a resolver that picks the
// palace named by the X-Palace header; "boom" is a plain error and anything
// else a 403 StatusError.
func resolvingServer(t *testing.T, palaces map[string]*service.Palace) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(ResolvingHandler(func(r *http.Request) (*service.Palace, error) {
		name := r.Header.Get("X-Palace")
		if name == "boom" {
			return nil, errors.New("database down")
		}
		if p, ok := palaces[name]; ok {
			return p, nil
		}
		return nil, &httpapi.StatusError{Status: http.StatusForbidden, Kind: "forbidden", Message: "not yours"}
	}, ops.All(), nil))
	t.Cleanup(ts.Close)
	return ts
}

func dialWith(t *testing.T, ts *httptest.Server, palace string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	if palace != "" {
		h.Set("X-Palace", palace)
	}
	c, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), h)
	if c != nil {
		t.Cleanup(func() { c.Close() })
	}
	return c, resp, err
}

func TestResolverErrorEndsTheHandshakeWithTheEnvelope(t *testing.T) {
	ts := resolvingServer(t, map[string]*service.Palace{"a": newPalace(t)})
	_, resp, err := dialWith(t, ts, "")
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected a 403 handshake failure, got err=%v resp=%v", err, resp)
	}
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil || env.OK || env.Error.Kind != "forbidden" || env.Error.Message != "not yours" {
		t.Errorf("body: %s", body)
	}
	if _, resp, err := dialWith(t, ts, "boom"); err == nil || resp == nil || resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("plain error: err=%v resp=%v", err, resp)
	}
}

func TestResolverPicksThePalacePerConnection(t *testing.T) {
	a, b := newPalace(t), newPalace(t)
	ts := resolvingServer(t, map[string]*service.Palace{"a": a, "b": b})
	ca, _, err := dialWith(t, ts, "a")
	if err != nil {
		t.Fatal(err)
	}
	cb, _, err := dialWith(t, ts, "b")
	if err != nil {
		t.Fatal(err)
	}
	// b subscribes to everything before a creates a plan.
	send(t, cb, `{"id":1,"op":"subscribe"}`)
	if f := recv(t, cb); f.OK == nil || !*f.OK {
		t.Fatalf("subscribe: %+v", f)
	}
	input, _ := os.ReadFile("../../testdata/create-dsgn.json")
	send(t, ca, `{"id":2,"op":"create","params":{"input":`+string(input)+`}}`)
	if f := recv(t, ca); f.OK == nil || !*f.OK {
		t.Fatalf("create on a: %+v", f)
	}
	send(t, cb, `{"id":3,"op":"list"}`)
	if f := recv(t, cb); f.Event != "" || string(f.ID) != "3" || string(f.Result) != "[]" {
		t.Errorf("b must see no event from a and an empty list, got %+v %s", f, f.Result)
	}
	send(t, ca, `{"id":4,"op":"list"}`)
	if f := recv(t, ca); string(f.ID) != "4" || !strings.Contains(string(f.Result), `"id"`) {
		t.Errorf("a lists its plan: %+v %s", f, f.Result)
	}
	if docs, _ := b.Plans().List(t.Context(), service.ListFilter{}); len(docs) != 0 {
		t.Errorf("palace b has %d plans", len(docs))
	}
}
