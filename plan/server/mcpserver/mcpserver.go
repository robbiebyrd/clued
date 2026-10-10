// Package mcpserver exposes the plan service as an MCP server over Streamable
// HTTP (and stdio). Every registered operation becomes a tool with the same
// name and parameters as the other entrypoints. Plans are also resources
// (plan://<id> as Markdown, plan://<id>/json as JSON) that clients can
// subscribe to for resources/updated notifications, and a `watch` tool lets
// tool-only clients block until one of a set of plans changes.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/plan/events"
	"github.com/robbiebyrd/clued/plan/markdown"
	"github.com/robbiebyrd/clued/plan/model"
	"github.com/robbiebyrd/clued/plan/ops"
	"github.com/robbiebyrd/clued/plan/service"
)

// Version reported to clients.
const Version = "0.1.0"

// URI scheme for plan resources.
const Scheme = "plan://"

// Options configures the MCP server.
type Options struct {
	// ToolPrefix is prepended to every tool name ("" keeps the operation names).
	ToolPrefix string
	Logger     *slog.Logger
}

// Server wraps an mcp.Server bound to a plan service.
type Server struct {
	MCP  *mcp.Server
	svc  *service.Service
	reg  *ops.Registry
	opts Options

	mu        sync.Mutex
	resources map[string]bool
	stop      func()
}

// New builds the MCP server, registers tools and resources and starts
// forwarding plan events as resource notifications.
func New(svc *service.Service, reg *ops.Registry, opts *Options) *Server {
	if opts == nil {
		opts = &Options{}
	}
	s := &Server{svc: svc, reg: reg, opts: *opts, resources: map[string]bool{}}
	s.MCP = mcp.NewServer(&mcp.Implementation{Name: "plan", Title: "Plan Service", Version: Version}, &mcp.ServerOptions{
		Instructions: instructions,
		Logger:       opts.Logger,
		SubscribeHandler: func(ctx context.Context, req *mcp.SubscribeRequest) error {
			if _, err := s.planFromURI(ctx, req.Params.URI); err != nil {
				return err
			}
			return nil
		},
		UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
	})
	s.addTools()
	s.addResources()
	ctx, cancel := context.WithCancel(context.Background())
	s.stop = cancel
	go s.forwardEvents(ctx)
	return s
}

// Close stops event forwarding.
func (s *Server) Close() {
	if s.stop != nil {
		s.stop()
	}
}

// Handler returns a Streamable HTTP handler for the server.
func (s *Server) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.MCP }, &mcp.StreamableHTTPOptions{Logger: s.opts.Logger})
}

// RunStdio serves the MCP server over stdin/stdout until ctx ends.
func (s *Server) RunStdio(ctx context.Context) error {
	return s.MCP.Run(ctx, &mcp.StdioTransport{})
}

const instructions = `Plan service: create, read, update and monitor Plans (design and implementation plans stored as Markdown with YAML front matter).
Start with getTemplate to learn the content layout and getSchema/getConfig for the creation schema, plan types, statuses and workflow.
Every write is validated; status changes follow the configured workflow (see getTransitions). Plans are also resources at plan://<id> (Markdown) and plan://<id>/json; subscribe to them or call watch to receive updates.`

func (s *Server) toolName(op string) string { return s.opts.ToolPrefix + op }

func (s *Server) addTools() {
	for _, op := range s.reg.List() {
		op := op
		schema, err := op.InputSchema(s.svc)
		if err != nil {
			continue
		}
		s.MCP.AddTool(&mcp.Tool{
			Name:        s.toolName(op.Name),
			Description: op.Description,
			InputSchema: schema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: op.ReadOnly, DestructiveHint: boolPtr(op.Name == "delete" || op.Name == "deleteTemplate"), IdempotentHint: op.ReadOnly},
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			res, err := s.reg.Invoke(ctx, s.svc, op.Name, req.Params.Arguments)
			if err != nil {
				return errorResult(err), nil
			}
			return result(res)
		})
	}
	s.MCP.AddTool(&mcp.Tool{
		Name:        s.toolName("watch"),
		Description: "Block until one of the given plans (or any plan when none are given) changes, or the timeout passes. Returns the events seen. Use it to monitor progress when resource subscriptions are not available.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"plans":{"type":"array","items":{"type":"string"},"description":"Plan ids to watch (empty = every plan)"},"timeoutSeconds":{"type":"number","description":"Seconds to wait (default 30, max 600)"}},"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.watch)
}

func boolPtr(b bool) *bool { return &b }

func (s *Server) watch(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var p struct {
		Plans          []string `json:"plans"`
		TimeoutSeconds float64  `json:"timeoutSeconds"`
	}
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &p); err != nil {
			return errorResult(&service.Error{Kind: service.KindBadRequest, Message: err.Error()}), nil
		}
	}
	timeout := time.Duration(p.TimeoutSeconds * float64(time.Second))
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	var filter []string
	if len(p.Plans) > 0 {
		filter = p.Plans
	}
	sub := s.svc.Bus().Subscribe(filter, 64)
	defer sub.Close()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var seen []events.Event
	select {
	case <-ctx.Done():
		return errorResult(ctx.Err()), nil
	case <-timer.C:
	case e := <-sub.C:
		seen = append(seen, e)
		// Gather anything else that arrived in the same instant.
		settle := time.After(100 * time.Millisecond)
	drain:
		for {
			select {
			case e := <-sub.C:
				seen = append(seen, e)
			case <-settle:
				break drain
			}
		}
	}
	if seen == nil {
		seen = []events.Event{}
	}
	return result(&ops.Result{Value: map[string]any{"events": seen, "timedOut": len(seen) == 0}})
}

func result(res *ops.Result) (*mcp.CallToolResult, error) {
	body := map[string]any{"ok": true, "result": res.Value}
	if res.Warning != nil {
		body["warning"] = res.Warning
	}
	b, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, StructuredContent: body}, nil
}

func errorResult(err error) *mcp.CallToolResult {
	e := service.AsError(err)
	body := map[string]any{"ok": false, "error": e}
	b, _ := json.MarshalIndent(body, "", "  ")
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, StructuredContent: body}
}

// ---------------------------------------------------------------------------
// Resources

func planURI(id string) string     { return Scheme + id }
func planJSONURI(id string) string { return Scheme + id + "/json" }

func (s *Server) planFromURI(ctx context.Context, uri string) (*model.Plan, error) {
	rest, ok := strings.CutPrefix(uri, Scheme)
	if !ok {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	id := strings.TrimSuffix(rest, "/json")
	p, err := s.svc.Get(ctx, id)
	if err != nil {
		if service.IsKind(err, service.KindNotFound) {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		return nil, err
	}
	return p, nil
}

func (s *Server) readResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	p, err := s.planFromURI(ctx, uri)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(uri, "/json") {
		b, _ := json.MarshalIndent(p, "", "  ")
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(b)}}}, nil
	}
	doc, err := markdown.RenderPlan(p)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/markdown", Text: doc}}}, nil
}

func (s *Server) addResources() {
	s.MCP.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: Scheme + "{id}",
		Name:        "plan",
		Title:       "Plan document",
		Description: "A plan as Markdown with YAML front matter. Append /json for the structured form.",
		MIMEType:    "text/markdown",
	}, s.readResource)
	s.MCP.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: Scheme + "{id}/json",
		Name:        "plan-json",
		Title:       "Plan (JSON)",
		Description: "A plan as JSON: path, frontMatter and content.",
		MIMEType:    "application/json",
	}, s.readResource)
	plans, err := s.svc.List(context.Background(), service.ListFilter{IncludeArchived: true})
	if err != nil {
		return
	}
	for _, p := range plans {
		s.upsertResource(p.FrontMatter.ID, p.FrontMatter.Title, p.FrontMatter.Type, p.FrontMatter.Status)
	}
}

func (s *Server) upsertResource(id, title, typ, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources[id] = true
	s.MCP.AddResource(&mcp.Resource{
		URI:         planURI(id),
		Name:        id,
		Title:       title,
		Description: fmt.Sprintf("%s plan, %s", typ, status),
		MIMEType:    "text/markdown",
	}, s.readResource)
	s.MCP.AddResource(&mcp.Resource{
		URI:      planJSONURI(id),
		Name:     id + "/json",
		Title:    title + " (JSON)",
		MIMEType: "application/json",
	}, s.readResource)
}

func (s *Server) removeResource(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resources[id] {
		delete(s.resources, id)
		s.MCP.RemoveResources(planURI(id), planJSONURI(id))
	}
}

func (s *Server) forwardEvents(ctx context.Context) {
	sub := s.svc.Bus().Subscribe(nil, 256)
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			if e.PlanID == "" {
				continue
			}
			switch e.Type {
			case events.PlanDeleted:
				s.removeResource(e.PlanID)
			default:
				if e.Plan != nil {
					s.upsertResource(e.PlanID, e.Plan.FrontMatter.Title, e.Plan.FrontMatter.Type, e.Plan.FrontMatter.Status)
				}
			}
			_ = s.MCP.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: planURI(e.PlanID)})
			_ = s.MCP.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: planJSONURI(e.PlanID)})
		}
	}
}
