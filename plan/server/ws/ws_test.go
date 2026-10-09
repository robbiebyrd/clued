package ws

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/ops"
	"github.com/robbiebyrd/clued/plan/service"
	"github.com/robbiebyrd/clued/plan/store/memstore"
)

type frame struct {
	ID      json.RawMessage `json:"id"`
	Op      string          `json:"op"`
	OK      *bool           `json:"ok"`
	Result  json.RawMessage `json:"result"`
	Error   *service.Error  `json:"error"`
	Event   string          `json:"event"`
	PlanID  string          `json:"planId"`
	Warning *service.Error  `json:"warning"`
}

func dial(t *testing.T) (*websocket.Conn, *service.Service) {
	t.Helper()
	svc, err := service.New(config.Default(), memstore.New(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(Handler(svc, ops.Default(), nil))
	t.Cleanup(ts.Close)
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, svc
}

func send(t *testing.T, c *websocket.Conn, msg string) {
	t.Helper()
	if err := c.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatal(err)
	}
}

func recv(t *testing.T, c *websocket.Conn) frame {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var f frame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("bad frame %s: %v", data, err)
	}
	return f
}

func TestCommandsAndEvents(t *testing.T) {
	c, svc := dial(t)
	send(t, c, `{"id":1,"op":"ping"}`)
	if f := recv(t, c); string(f.ID) != "1" || f.OK == nil || !*f.OK || string(f.Result) != `"pong"` {
		t.Errorf("ping: %+v", f)
	}
	send(t, c, `not json`)
	if f := recv(t, c); f.Error == nil || f.Error.Kind != service.KindBadRequest {
		t.Errorf("bad frame: %+v", f)
	}
	send(t, c, `{"id":"a","op":"get","params":{"plan":"0001-abc"}}`)
	if f := recv(t, c); f.Error == nil || f.Error.Kind != service.KindNotFound || string(f.ID) != `"a"` {
		t.Errorf("not found: %+v", f)
	}
	input, _ := os.ReadFile("../../testdata/create-dsgn.json")
	send(t, c, `{"id":2,"op":"create","params":{"input":`+string(input)+`}}`)
	f := recv(t, c)
	if f.OK == nil || !*f.OK {
		t.Fatalf("create: %+v", f)
	}
	var plan struct {
		FrontMatter struct{ ID string } `json:"frontMatter"`
	}
	json.Unmarshal(f.Result, &plan)
	id := plan.FrontMatter.ID

	// Nothing arrives before subscribing.
	svc.SetPriority(t.Context(), id, "P0")
	send(t, c, `{"id":3,"op":"subscribe","params":{"plans":["`+id+`"]}}`)
	if f := recv(t, c); string(f.ID) != "3" || !strings.Contains(string(f.Result), `"subscribed":true`) {
		t.Fatalf("subscribe: %+v %s", f, f.Result)
	}
	other, _ := svc.Create(t.Context(), input, "")
	svc.SetPriority(t.Context(), id, "P1")
	f = recv(t, c)
	if f.Event != "plan.updated" || f.PlanID != id {
		t.Errorf("expected update for %s only, got %+v", id, f)
	}
	// Widen to the other plan, then unsubscribe it again.
	send(t, c, `{"op":"subscribe","params":{"plans":["`+other.ID()+`"]}}`)
	if f := recv(t, c); !strings.Contains(string(f.Result), other.ID()) {
		t.Errorf("widen: %s", f.Result)
	}
	svc.SetPriority(t.Context(), other.ID(), "P2")
	if f := recv(t, c); f.PlanID != other.ID() {
		t.Errorf("event for widened plan: %+v", f)
	}
	send(t, c, `{"op":"unsubscribe","params":{"plans":["`+other.ID()+`"]}}`)
	if f := recv(t, c); strings.Contains(string(f.Result), other.ID()) {
		t.Errorf("narrow: %s", f.Result)
	}
	svc.SetPriority(t.Context(), other.ID(), "P3")
	svc.Delete(t.Context(), id, false)
	if f := recv(t, c); f.Event != "plan.deleted" || f.PlanID != id {
		t.Errorf("delete event (and no event for unsubscribed plan): %+v", f)
	}
	// Subscribe to everything, then stop.
	send(t, c, `{"op":"subscribe"}`)
	if f := recv(t, c); !strings.Contains(string(f.Result), `"all":true`) {
		t.Errorf("all: %s", f.Result)
	}
	svc.SetPriority(t.Context(), other.ID(), "P4")
	if f := recv(t, c); f.Event != "plan.updated" {
		t.Errorf("all event: %+v", f)
	}
	send(t, c, `{"op":"unsubscribe"}`)
	if f := recv(t, c); !strings.Contains(string(f.Result), `"subscribed":false`) {
		t.Errorf("stop: %s", f.Result)
	}
	svc.SetPriority(t.Context(), other.ID(), "P5")
	send(t, c, `{"id":9,"op":"ping"}`)
	if f := recv(t, c); string(f.ID) != "9" {
		t.Errorf("expected only the pong after unsubscribing, got %+v", f)
	}
}
