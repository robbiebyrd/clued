// Package mcpserver exposes the plan and story services as an MCP server over
// Streamable HTTP (and stdio). Every registered operation becomes a tool
// named <kind>_<op> (plan_create, story_setStatus) with the same parameters
// as the other entrypoints; sync and getConfig, which span both kinds, are
// registered once under their own names. Documents are also resources
// (plan://<id>, story://<id>, with /json for the structured form) that
// clients can subscribe to for resources/updated notifications, and a
// `watch` tool lets tool-only clients block until a document changes.
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

	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/ops"
	"github.com/robbiebyrd/clued/mind-palace/service"
)

// Version reported to clients.
const Version = "0.2.0"

// Options configures the MCP server.
type Options struct {
	// ToolPrefix is prepended to every tool name ("" keeps <kind>_<op>).
	ToolPrefix string
	Logger     *slog.Logger
	// Tools are external tool providers registered after the kind tools.
	Tools []ToolProvider
	// ExtraInstructions is appended to the server instructions after a blank line.
	ExtraInstructions string
}

// ToolProvider registers additional tools on the server. Implementations
// apply prefix to every tool name they add.
type ToolProvider interface {
	AddTools(s *mcp.Server, prefix string)
}

// Server wraps an mcp.Server bound to a palace.
type Server struct {
	MCP    *mcp.Server
	palace *service.Palace
	regs   ops.Registries
	opts   Options

	mu        sync.Mutex
	resources map[string]bool // "<kind>:<id>"
	stop      func()
}

// sharedOps are registered once, without a kind prefix.
var sharedOps = map[string]bool{"sync": true, "getConfig": true}

// New builds the MCP server, registers tools and resources and starts
// forwarding document events as resource notifications.
func New(palace *service.Palace, regs ops.Registries, opts *Options) *Server {
	if opts == nil {
		opts = &Options{}
	}
	s := &Server{palace: palace, regs: regs, opts: *opts, resources: map[string]bool{}}
	serverInstructions := instructions
	if opts.ExtraInstructions != "" {
		serverInstructions += "\n\n" + opts.ExtraInstructions
	}
	s.MCP = mcp.NewServer(&mcp.Implementation{Name: "mind-palace", Title: "Mind Palace (plans and stories)", Version: Version}, &mcp.ServerOptions{
		Instructions: serverInstructions,
		Logger:       opts.Logger,
		SubscribeHandler: func(ctx context.Context, req *mcp.SubscribeRequest) error {
			_, _, err := s.documentFromURI(ctx, req.Params.URI)
			return err
		},
		UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
	})
	s.addTools()
	for _, p := range opts.Tools {
		p.AddTools(s.MCP, opts.ToolPrefix)
	}
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

const instructions = `Mind Palace: create, read, update and monitor Plans (design and implementation plans) and Stories (issues: bugs, features, improvements, chores, tasks), stored as Markdown with YAML front matter.
Tools are named <kind>_<operation> (plan_create, story_setStatus); sync and getConfig span both kinds.
Start with plan_getTemplate / story_getTemplate to learn the content layouts and <kind>_getSchema / getConfig for the creation schemas, types, statuses and workflow.
Every write is validated; status changes follow the configured workflow (see <kind>_getTransitions). A story cannot be completed while an acceptance criterion is open (story_getCriteria, story_setCriterion). Documents are also resources at plan://<id> and story://<id> (Markdown; append /json for the structured form); subscribe to them or call watch to receive updates.`

// ToolName returns the tool name of an operation of a kind.
func (s *Server) ToolName(kindName, op string) string {
	if sharedOps[op] {
		return s.opts.ToolPrefix + op
	}
	return s.opts.ToolPrefix + kindName + "_" + op
}

func (s *Server) addTools() {
	shared := map[string]bool{}
	for _, svc := range s.palace.Services() {
		svc := svc
		k := svc.Kind()
		reg := s.regs[k.Name]
		if reg == nil {
			continue
		}
		for _, op := range reg.List() {
			op := op
			if sharedOps[op.Name] {
				if shared[op.Name] {
					continue
				}
				shared[op.Name] = true
			}
			schema, err := op.InputSchema(svc)
			if err != nil {
				continue
			}
			s.MCP.AddTool(&mcp.Tool{
				Name:        s.ToolName(k.Name, op.Name),
				Description: op.Description,
				InputSchema: schema,
				Annotations: &mcp.ToolAnnotations{ReadOnlyHint: op.ReadOnly, DestructiveHint: boolPtr(op.Destructive), IdempotentHint: op.ReadOnly},
			}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				res, err := reg.Invoke(ctx, svc, op.Name, req.Params.Arguments)
				if err != nil {
					return errorResult(err), nil
				}
				return result(res)
			})
		}
	}
	s.MCP.AddTool(&mcp.Tool{
		Name:        s.opts.ToolPrefix + "watch",
		Description: "Block until one of the given documents (or any document when none are given) changes, or the timeout passes. Returns the events seen. Use it to monitor progress when resource subscriptions are not available.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","enum":["plan","story"],"description":"Document kind to watch (empty = every kind)"},"ids":{"type":"array","items":{"type":"string"},"description":"Document ids to watch (empty = every document of the kind)"},"plans":{"type":"array","items":{"type":"string"},"description":"Shortcut: plan ids to watch"},"stories":{"type":"array","items":{"type":"string"},"description":"Shortcut: story ids to watch"},"timeoutSeconds":{"type":"number","description":"Seconds to wait (default 30, max 600)"}},"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.watch)
}

func boolPtr(b bool) *bool { return &b }

func (s *Server) watch(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var p struct {
		Kind           string   `json:"kind"`
		IDs            []string `json:"ids"`
		Plans          []string `json:"plans"`
		Stories        []string `json:"stories"`
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
	filter := events.Filter{}
	if p.Kind != "" {
		k, ok := kind.Get(p.Kind)
		if !ok {
			return errorResult(&service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("unknown document kind %q", p.Kind)}), nil
		}
		filter.Kind = k.Name
	}
	ids := append([]string(nil), p.IDs...)
	if len(p.Plans) > 0 {
		filter.Kind = kind.Plan
		ids = append(ids, p.Plans...)
	}
	if len(p.Stories) > 0 {
		if len(p.Plans) > 0 {
			return errorResult(&service.Error{Kind: service.KindBadRequest, Message: "watch one kind at a time: plans or stories"}), nil
		}
		filter.Kind = kind.Story
		ids = append(ids, p.Stories...)
	}
	if len(ids) > 0 {
		filter.IDs = ids
	}
	sub := s.palace.Bus().Subscribe(filter, 64)
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

func docURI(k *kind.Kind, id string) string     { return k.Scheme + id }
func docJSONURI(k *kind.Kind, id string) string { return k.Scheme + id + "/json" }

// kindFromURI finds the kind whose scheme the URI uses.
func kindFromURI(uri string) (*kind.Kind, string, bool) {
	for _, k := range kind.All() {
		if rest, ok := strings.CutPrefix(uri, k.Scheme); ok {
			return k, rest, true
		}
	}
	return nil, "", false
}

func (s *Server) documentFromURI(ctx context.Context, uri string) (*kind.Kind, *model.Document, error) {
	k, rest, ok := kindFromURI(uri)
	if !ok {
		return nil, nil, mcp.ResourceNotFoundError(uri)
	}
	id := strings.TrimSuffix(rest, "/json")
	svc, _ := s.palace.Service(k.Name)
	d, err := svc.Get(ctx, id)
	if err != nil {
		if service.IsKind(err, service.KindNotFound) {
			return nil, nil, mcp.ResourceNotFoundError(uri)
		}
		return nil, nil, err
	}
	return k, d, nil
}

func (s *Server) readResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	_, d, err := s.documentFromURI(ctx, uri)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(uri, "/json") {
		b, _ := json.MarshalIndent(d, "", "  ")
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(b)}}}, nil
	}
	doc, err := markdown.RenderDocument(d)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/markdown", Text: doc}}}, nil
}

func (s *Server) addResources() {
	for _, svc := range s.palace.Services() {
		k := svc.Kind()
		s.MCP.AddResourceTemplate(&mcp.ResourceTemplate{
			URITemplate: k.Scheme + "{id}",
			Name:        k.Name,
			Title:       k.Title + " document",
			Description: fmt.Sprintf("A %s as Markdown with YAML front matter. Append /json for the structured form.", k.Name),
			MIMEType:    "text/markdown",
		}, s.readResource)
		s.MCP.AddResourceTemplate(&mcp.ResourceTemplate{
			URITemplate: k.Scheme + "{id}/json",
			Name:        k.Name + "-json",
			Title:       k.Title + " (JSON)",
			Description: fmt.Sprintf("A %s as JSON: kind, path, frontMatter and content.", k.Name),
			MIMEType:    "application/json",
		}, s.readResource)
		docs, err := svc.List(context.Background(), service.ListFilter{IncludeArchived: true})
		if err != nil {
			continue
		}
		for _, d := range docs {
			s.upsertResource(k, d.FrontMatter.ID, d.FrontMatter.Title, d.FrontMatter.Type, d.FrontMatter.Status)
		}
	}
}

func (s *Server) upsertResource(k *kind.Kind, id, title, typ, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources[k.Name+":"+id] = true
	s.MCP.AddResource(&mcp.Resource{
		URI:         docURI(k, id),
		Name:        k.Name + "/" + id,
		Title:       title,
		Description: fmt.Sprintf("%s %s, %s", typ, k.Name, status),
		MIMEType:    "text/markdown",
	}, s.readResource)
	s.MCP.AddResource(&mcp.Resource{
		URI:      docJSONURI(k, id),
		Name:     k.Name + "/" + id + "/json",
		Title:    title + " (JSON)",
		MIMEType: "application/json",
	}, s.readResource)
}

func (s *Server) removeResource(k *kind.Kind, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resources[k.Name+":"+id] {
		delete(s.resources, k.Name+":"+id)
		s.MCP.RemoveResources(docURI(k, id), docJSONURI(k, id))
	}
}

func (s *Server) forwardEvents(ctx context.Context) {
	sub := s.palace.Bus().Subscribe(events.Filter{}, 256)
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			if e.ID == "" {
				continue
			}
			k, ok := kind.Get(e.Kind)
			if !ok {
				continue
			}
			switch e.Type {
			case k.Event(events.ActionDeleted):
				s.removeResource(k, e.ID)
			default:
				if e.Document != nil {
					s.upsertResource(k, e.ID, e.Document.FrontMatter.Title, e.Document.FrontMatter.Type, e.Document.FrontMatter.Status)
				}
			}
			_ = s.MCP.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: docURI(k, e.ID)})
			_ = s.MCP.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: docJSONURI(k, e.ID)})
		}
	}
}
