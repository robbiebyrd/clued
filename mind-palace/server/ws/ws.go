// Package ws exposes the plan and story services over WebSockets. A client
// sends JSON requests and receives responses plus, once subscribed, a stream
// of document events.
//
// Request:  {"id": "1", "kind": "story", "op": "get", "params": {"story": "0002-a3f"}}
// Response: {"id": "1", "ok": true, "result": …, "warning"?: …}
//
//	{"id": "1", "ok": false, "error": {"kind": …, "message": …, "problems": […]}}
//
// kind selects the service ("plan" by default, or "story").
//
// Subscriptions (not service operations, handled by the socket itself):
//
//	{"op": "subscribe"}                                             every document of every kind
//	{"op": "subscribe", "kind": "story"}                             every story
//	{"op": "subscribe", "kind": "plan", "params": {"ids": ["0002-a3f"]}}  add plans
//	{"op": "unsubscribe", "params": {"ids": ["0002-a3f"]}}          drop documents
//	{"op": "unsubscribe"}                                           stop everything
//	{"op": "ping"}
//
// params.plans and params.stories are shortcuts for kind + ids.
//
// Events: {"event": "story.updated", "kind": "story", "id": "0002-a3f", "document": {…}, "operation": "setStatus", "at": "…"}
package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/ops"
	"github.com/robbiebyrd/clued/mind-palace/service"
)

// Request is an inbound frame.
type Request struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Kind   string          `json:"kind,omitempty"`
	Op     string          `json:"op"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is an outbound reply frame.
type Response struct {
	ID      json.RawMessage `json:"id,omitempty"`
	Kind    string          `json:"kind,omitempty"`
	Op      string          `json:"op,omitempty"`
	OK      bool            `json:"ok"`
	Result  any             `json:"result,omitempty"`
	Warning *service.Error  `json:"warning,omitempty"`
	Error   *service.Error  `json:"error,omitempty"`
}

type subscribeParams struct {
	Kind    string   `json:"kind,omitempty"`
	IDs     []string `json:"ids,omitempty"`
	Plans   []string `json:"plans,omitempty"`
	Stories []string `json:"stories,omitempty"`
}

// Options configures the socket handler.
type Options struct {
	// CheckOrigin decides whether a browser origin may connect. nil allows
	// same-host and non-browser clients only.
	CheckOrigin func(r *http.Request) bool
	// WriteTimeout bounds each write (default 10s).
	WriteTimeout time.Duration
}

// Handler returns the WebSocket handler.
func Handler(palace *service.Palace, regs ops.Registries, opts *Options) http.Handler {
	if opts == nil {
		opts = &Options{}
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = 10 * time.Second
	}
	up := websocket.Upgrader{ReadBufferSize: 64 << 10, WriteBufferSize: 64 << 10, CheckOrigin: opts.CheckOrigin}
	if up.CheckOrigin == nil {
		up.CheckOrigin = sameHost
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn := &conn{c: c, palace: palace, regs: regs, out: make(chan any, 256), opts: opts}
		conn.serve(r.Context())
	})
}

func sameHost(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	return websocket.IsWebSocketUpgrade(r) && equalHost(origin, r.Host)
}

func equalHost(origin, host string) bool {
	for _, scheme := range []string{"http://", "https://", "ws://", "wss://"} {
		if origin == scheme+host {
			return true
		}
	}
	return false
}

type conn struct {
	c      *websocket.Conn
	palace *service.Palace
	regs   ops.Registries
	opts   *Options
	out    chan any

	mu      sync.Mutex
	sub     *events.Subscription
	subKind string
	all     bool
	ids     []string
}

func (cn *conn) serve(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer cn.c.Close()
	defer cn.unsubscribeAll()

	go cn.writer(ctx)
	cn.c.SetReadLimit(16 << 20)
	for {
		_, data, err := cn.c.ReadMessage()
		if err != nil {
			return
		}
		var req Request
		if err := json.Unmarshal(data, &req); err != nil {
			cn.send(Response{OK: false, Error: &service.Error{Kind: service.KindBadRequest, Message: "invalid frame: " + err.Error()}})
			continue
		}
		cn.send(cn.handle(ctx, req))
	}
}

func (cn *conn) handle(ctx context.Context, req Request) Response {
	resp := Response{ID: req.ID, Op: req.Op, Kind: req.Kind}
	switch req.Op {
	case "ping":
		resp.OK, resp.Result = true, "pong"
		return resp
	case "subscribe", "unsubscribe":
		var p subscribeParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				resp.Error = &service.Error{Kind: service.KindBadRequest, Message: "params must be {\"kind\"?: ..., \"ids\"?: [...]}"}
				return resp
			}
		}
		if p.Kind == "" {
			p.Kind = req.Kind
		}
		filter, err := toFilter(p)
		if err != nil {
			resp.Error = service.AsError(err)
			return resp
		}
		var state subState
		if req.Op == "subscribe" {
			state, err = cn.subscribe(ctx, filter)
		} else {
			state = cn.unsubscribe(filter.IDs)
		}
		if err != nil {
			resp.Error = service.AsError(err)
			return resp
		}
		resp.OK, resp.Result = true, state
		return resp
	}
	k := req.Kind
	if k == "" {
		k = kind.Plan
	}
	reg, err := cn.regs.Get(k)
	if err != nil {
		resp.Error = service.AsError(err)
		return resp
	}
	svc, _ := cn.palace.Service(k)
	resp.Kind = svc.Kind().Name
	res, err := reg.Invoke(ctx, svc, req.Op, req.Params)
	if err != nil {
		resp.Error = service.AsError(err)
		return resp
	}
	resp.OK, resp.Result, resp.Warning = true, res.Value, res.Warning
	return resp
}

// toFilter turns subscribe parameters into an event filter.
func toFilter(p subscribeParams) (events.Filter, error) {
	f := events.Filter{}
	if p.Kind != "" {
		k, ok := kind.Get(p.Kind)
		if !ok {
			return f, &service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("unknown document kind %q (known: %s)", p.Kind, strings.Join(kind.Names(), ", "))}
		}
		f.Kind = k.Name
	}
	if len(p.IDs) > 0 {
		f.IDs = append(f.IDs, p.IDs...)
	}
	for _, shortcut := range []struct {
		kind string
		ids  []string
	}{{kind.Plan, p.Plans}, {kind.Story, p.Stories}} {
		if len(shortcut.ids) == 0 {
			continue
		}
		if f.Kind != "" && f.Kind != shortcut.kind {
			return f, &service.Error{Kind: service.KindBadRequest, Message: "a subscription covers one kind; use ids with kind, or plans/stories alone"}
		}
		f.Kind = shortcut.kind
		f.IDs = append(f.IDs, shortcut.ids...)
	}
	return f, nil
}

type subState struct {
	Subscribed bool     `json:"subscribed"`
	Kind       string   `json:"kind,omitempty"`
	All        bool     `json:"all"`
	IDs        []string `json:"ids"`
}

func (cn *conn) state() subState {
	ids := cn.ids
	if ids == nil {
		ids = []string{}
	}
	return subState{Subscribed: cn.sub != nil, Kind: cn.subKind, All: cn.all, IDs: ids}
}

func (cn *conn) subscribe(ctx context.Context, f events.Filter) (subState, error) {
	cn.mu.Lock()
	defer cn.mu.Unlock()
	if cn.sub == nil {
		cn.sub = cn.palace.Bus().Subscribe(f, 256)
		cn.subKind = f.Kind
		cn.all = len(f.IDs) == 0
		cn.ids = appendUnique(nil, f.IDs...)
		go cn.forward(ctx, cn.sub)
		return cn.state(), nil
	}
	if f.Kind != "" && f.Kind != cn.subKind {
		return cn.state(), &service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("already subscribed to %s events; unsubscribe first to switch kind", kindLabel(cn.subKind))}
	}
	if len(f.IDs) == 0 {
		cn.all = true
		cn.sub.SetFilter(nil)
		cn.ids = nil
		return cn.state(), nil
	}
	if cn.all {
		return cn.state(), nil
	}
	cn.sub.Add(f.IDs...)
	cn.ids = appendUnique(cn.ids, f.IDs...)
	return cn.state(), nil
}

func kindLabel(k string) string {
	if k == "" {
		return "all"
	}
	return k
}

func (cn *conn) unsubscribe(ids []string) subState {
	cn.mu.Lock()
	defer cn.mu.Unlock()
	if cn.sub == nil {
		return cn.state()
	}
	if len(ids) == 0 || cn.all {
		cn.sub.Close()
		cn.sub, cn.all, cn.ids, cn.subKind = nil, false, nil, ""
		return cn.state()
	}
	cn.sub.Remove(ids...)
	cn.ids = removeAll(cn.ids, ids...)
	if len(cn.ids) == 0 {
		cn.sub.Close()
		cn.sub, cn.subKind = nil, ""
	}
	return cn.state()
}

func (cn *conn) unsubscribeAll() {
	cn.mu.Lock()
	defer cn.mu.Unlock()
	if cn.sub != nil {
		cn.sub.Close()
		cn.sub = nil
	}
	cn.ids = nil
}

func appendUnique(list []string, items ...string) []string {
	seen := map[string]bool{}
	for _, x := range list {
		seen[x] = true
	}
	for _, x := range items {
		if !seen[x] {
			seen[x] = true
			list = append(list, x)
		}
	}
	return list
}

func removeAll(list []string, items ...string) []string {
	drop := map[string]bool{}
	for _, x := range items {
		drop[x] = true
	}
	var out []string
	for _, x := range list {
		if !drop[x] {
			out = append(out, x)
		}
	}
	return out
}

func (cn *conn) forward(ctx context.Context, sub *events.Subscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			cn.send(e)
		}
	}
}

func (cn *conn) send(v any) {
	select {
	case cn.out <- v:
	default:
		// Drop rather than block the reader when the client is not keeping up.
	}
}

func (cn *conn) writer(ctx context.Context) {
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			cn.c.SetWriteDeadline(time.Now().Add(cn.opts.WriteTimeout))
			if err := cn.c.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case v := <-cn.out:
			cn.c.SetWriteDeadline(time.Now().Add(cn.opts.WriteTimeout))
			if err := cn.c.WriteJSON(v); err != nil {
				return
			}
		}
	}
}
