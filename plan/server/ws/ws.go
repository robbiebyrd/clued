// Package ws exposes the plan service over WebSockets. A client sends JSON
// requests and receives responses plus, once subscribed, a stream of plan
// events.
//
// Request:  {"id": "1", "op": "get", "params": {"plan": "0002-a3f"}}
// Response: {"id": "1", "ok": true, "result": …, "warning"?: …}
//
//	{"id": "1", "ok": false, "error": {"kind": …, "message": …, "problems": […]}}
//
// Subscriptions (not service operations, handled by the socket itself):
//
//	{"op": "subscribe"}                                 every plan
//	{"op": "subscribe",   "params": {"plans": ["0002-a3f"]}}  add plans
//	{"op": "unsubscribe", "params": {"plans": ["0002-a3f"]}}  drop plans
//	{"op": "unsubscribe"}                               stop everything
//	{"op": "ping"}
//
// Events: {"event": "plan.updated", "planId": "0002-a3f", "plan": {…}, "operation": "setStatus", "at": "…"}
package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/robbiebyrd/clued/plan/events"
	"github.com/robbiebyrd/clued/plan/ops"
	"github.com/robbiebyrd/clued/plan/service"
)

// Request is an inbound frame.
type Request struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Op     string          `json:"op"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is an outbound reply frame.
type Response struct {
	ID      json.RawMessage `json:"id,omitempty"`
	Op      string          `json:"op,omitempty"`
	OK      bool            `json:"ok"`
	Result  any             `json:"result,omitempty"`
	Warning *service.Error  `json:"warning,omitempty"`
	Error   *service.Error  `json:"error,omitempty"`
}

type subscribeParams struct {
	Plans []string `json:"plans,omitempty"`
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
func Handler(svc *service.Service, reg *ops.Registry, opts *Options) http.Handler {
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
		conn := &conn{c: c, svc: svc, reg: reg, out: make(chan any, 256), opts: opts}
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
	c    *websocket.Conn
	svc  *service.Service
	reg  *ops.Registry
	opts *Options
	out  chan any

	mu    sync.Mutex
	sub   *events.Subscription
	all   bool
	plans []string
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
	resp := Response{ID: req.ID, Op: req.Op}
	switch req.Op {
	case "ping":
		resp.OK, resp.Result = true, "pong"
		return resp
	case "subscribe", "unsubscribe":
		var p subscribeParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				resp.Error = &service.Error{Kind: service.KindBadRequest, Message: "params must be {\"plans\": [...]}"}
				return resp
			}
		}
		resp.OK = true
		if req.Op == "subscribe" {
			resp.Result = cn.subscribe(ctx, p.Plans)
		} else {
			resp.Result = cn.unsubscribe(p.Plans)
		}
		return resp
	}
	res, err := cn.reg.Invoke(ctx, cn.svc, req.Op, req.Params)
	if err != nil {
		resp.Error = service.AsError(err)
		return resp
	}
	resp.OK, resp.Result, resp.Warning = true, res.Value, res.Warning
	return resp
}

type subState struct {
	Subscribed bool     `json:"subscribed"`
	All        bool     `json:"all"`
	Plans      []string `json:"plans"`
}

func (cn *conn) state(plans []string) subState {
	if plans == nil {
		plans = []string{}
	}
	return subState{Subscribed: cn.sub != nil, All: cn.all, Plans: plans}
}

func (cn *conn) subscribe(ctx context.Context, plans []string) subState {
	cn.mu.Lock()
	defer cn.mu.Unlock()
	if cn.sub == nil {
		var filter []string
		if len(plans) > 0 {
			filter = plans
		}
		cn.sub = cn.svc.Bus().Subscribe(filter, 256)
		cn.all = len(plans) == 0
		cn.plans = appendUnique(nil, plans...)
		go cn.forward(ctx, cn.sub)
		return cn.state(cn.plans)
	}
	if len(plans) == 0 {
		cn.all = true
		cn.sub.SetFilter(nil)
		cn.plans = nil
		return cn.state(nil)
	}
	if cn.all {
		return cn.state(nil)
	}
	cn.sub.Add(plans...)
	cn.plans = appendUnique(cn.plans, plans...)
	return cn.state(cn.plans)
}

func (cn *conn) unsubscribe(plans []string) subState {
	cn.mu.Lock()
	defer cn.mu.Unlock()
	if cn.sub == nil {
		return cn.state(nil)
	}
	if len(plans) == 0 || cn.all {
		cn.sub.Close()
		cn.sub, cn.all, cn.plans = nil, false, nil
		return cn.state(nil)
	}
	cn.sub.Remove(plans...)
	cn.plans = removeAll(cn.plans, plans...)
	if len(cn.plans) == 0 {
		cn.sub.Close()
		cn.sub = nil
	}
	return cn.state(cn.plans)
}

func (cn *conn) unsubscribeAll() {
	cn.mu.Lock()
	defer cn.mu.Unlock()
	if cn.sub != nil {
		cn.sub.Close()
		cn.sub = nil
	}
	cn.plans = nil
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
