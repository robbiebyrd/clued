// Package sessionmcp exposes the captured-session queries and restore as the
// six clued MCP tools, both as a standalone "clued" server and as a tool
// provider on the shared mind-palace server.
package sessionmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
	"github.com/robbiebyrd/clued/mind-palace/session/restore"
)

// Version is the version the standalone server reports.
const Version = "1.0.0"

// Instructions describes the session tools to clients of a server that hosts them.
const Instructions = `Session tools query and restore previously captured Claude Code sessions: find_sessions, get_session_context, get_full_session, search_commands, read_transcript and restore_session. Every result is JSON text; a failure is reported as a tool error with its message.`

// Tools serves the session tools for one account over a lazily connected store.
type Tools struct {
	cfg       session.Config
	connect   func(ctx context.Context) (session.Store, error)
	accountID string

	mu    sync.Mutex
	store session.Store
}

// New returns the tools. connect is called on the first tool call that needs
// the store, and again on later calls for as long as it fails.
func New(cfg session.Config, connect func(ctx context.Context) (session.Store, error), accountID string) *Tools {
	return &Tools{cfg: cfg, connect: connect, accountID: accountID}
}

// Server builds a standalone MCP server named clued holding the six tools.
func (t *Tools) Server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "clued", Version: Version}, &mcp.ServerOptions{Instructions: Instructions})
	t.AddTools(s, "")
	return s
}

// AddTools registers the six tools on s, each named prefix + its name.
func (t *Tools) AddTools(s *mcp.Server, prefix string) {
	for _, spec := range t.specs() {
		s.AddTool(&mcp.Tool{
			Name:        prefix + spec.name,
			Description: spec.description,
			InputSchema: json.RawMessage(spec.schema),
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: spec.readOnly},
		}, spec.handler)
	}
}

type toolSpec struct {
	name, description, schema string
	readOnly                  bool
	handler                   mcp.ToolHandler
}

func (t *Tools) specs() []toolSpec {
	return []toolSpec{
		{
			name:        "find_sessions",
			description: "Find previous Claude Code sessions, newest first. Filters match (case-insensitive regex) against project path, cwd, and git origin.",
			schema:      `{"type":"object","properties":{"project_path":{"type":"string","description":"Regex matched against the session project path"},"git_origin":{"type":"string","description":"Regex matched against the git remote origin URL"},"query":{"type":"string","description":"Regex matched against project path or cwd"},"limit":{"type":"number","description":"Max sessions to return (default 10, max 500)"}}}`,
			readOnly:    true,
			handler: tool(t, func(ctx context.Context, q *query.Service, args query.FindSessionsArgs, _ *mcp.CallToolRequest) (any, error) {
				docs, err := q.FindSessions(ctx, args)
				return nonNil(docs), err
			}),
		},
		{
			name:        "get_session_context",
			description: "Summarise one session: metadata, its most recent distinct Bash commands, and the first/last transcript lines.",
			schema:      `{"type":"object","properties":{"session_id":{"type":"string"}},"required":["session_id"]}`,
			readOnly:    true,
			handler: tool(t, func(ctx context.Context, q *query.Service, args sessionArgs, _ *mcp.CallToolRequest) (any, error) {
				return q.SessionContext(ctx, args.SessionID)
			}),
		},
		{
			name:        "get_full_session",
			description: "Return the complete record for one session: metadata, all transcript lines, all subagent lines, blob metadata (content excluded), and all hook events.",
			schema:      `{"type":"object","properties":{"session_id":{"type":"string"}},"required":["session_id"]}`,
			readOnly:    true,
			handler: tool(t, func(ctx context.Context, q *query.Service, args sessionArgs, _ *mcp.CallToolRequest) (any, error) {
				return q.FullSession(ctx, args.SessionID)
			}),
		},
		{
			name:        "search_commands",
			description: "Search Bash commands run in previous sessions, newest first.",
			schema:      `{"type":"object","properties":{"pattern":{"type":"string","description":"Case-insensitive regex matched against the command"},"session_id":{"type":"string","description":"Restrict to one session"},"git_origin":{"type":"string","description":"Restrict to sessions whose git origin matches this regex"},"limit":{"type":"number","description":"Max commands to return (default 20, max 500)"}},"required":["pattern"]}`,
			readOnly:    true,
			handler: tool(t, func(ctx context.Context, q *query.Service, args query.SearchCommandsArgs, _ *mcp.CallToolRequest) (any, error) {
				hits, err := q.SearchCommands(ctx, args)
				return nonNil(hits), err
			}),
		},
		{
			name:        "read_transcript",
			description: "Read transcript lines of a session in order, paginated by offset/limit.",
			schema:      `{"type":"object","properties":{"session_id":{"type":"string"},"offset":{"type":"number","description":"Line offset (default 0)"},"limit":{"type":"number","description":"Lines to return (default 200, max 500)"}},"required":["session_id"]}`,
			readOnly:    true,
			handler: tool(t, func(ctx context.Context, q *query.Service, args query.ReadTranscriptArgs, req *mcp.CallToolRequest) (any, error) {
				return q.ReadTranscript(ctx, args, progressReporter(ctx, req))
			}),
		},
		{
			name:        "restore_session",
			description: "Restore a session from MongoDB to the local filesystem. Reconstructs the main JSONL, subagent files, tool-result blobs, and file-history backups.",
			schema:      `{"type":"object","properties":{"session_id":{"type":"string","description":"Session to restore"},"project_path":{"type":"string","description":"Override the recorded project path (use when username/homedir differs on this machine)"},"projects_dir":{"type":"string","description":"Override the target ~/.claude/projects directory"}},"required":["session_id"]}`,
			handler:     t.restoreHandler(),
		},
	}
}

type sessionArgs struct {
	SessionID string `json:"session_id"`
}

// tool adapts a use case to an MCP handler: it decodes the arguments into A,
// connects the store, and reports the JSON of the result or the failure.
func tool[A any](t *Tools, run func(ctx context.Context, q *query.Service, args A, req *mcp.CallToolRequest) (any, error)) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args A
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return failure(err), nil
			}
		}
		store, err := t.getStore(ctx)
		if err != nil {
			return failure(err), nil
		}
		out, err := run(ctx, &query.Service{Store: store, AccountID: t.accountID}, args, req)
		if err != nil {
			return failure(err), nil
		}
		return success(out)
	}
}

func (t *Tools) restoreHandler() mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args restore.Args
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return failure(err), nil
			}
		}
		if args.ProjectsDir == "" {
			args.ProjectsDir = t.cfg.ProjectsDir
		}
		store, err := t.getStore(ctx)
		if err != nil {
			return failure(err), nil
		}
		res, err := restore.Session(ctx, store, t.accountID, args, t.cfg.FileHistoryDir)
		if err != nil {
			return failure(err), nil
		}
		return success(res)
	}
}

// getStore connects the store on first use; a failed connection is not cached.
func (t *Tools) getStore(ctx context.Context) (session.Store, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.store == nil {
		store, err := t.connect(ctx)
		if err != nil {
			return nil, fmt.Errorf("MongoDB connection failed: %w", err)
		}
		t.store = store
	}
	return t.store, nil
}

// progressReporter returns a read_transcript progress callback, or nil when
// the request carries no progress token.
func progressReporter(ctx context.Context, req *mcp.CallToolRequest) func(done, total int) {
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}
	return func(done, total int) {
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: token, Progress: float64(done), Total: float64(total),
		})
	}
}

// nonNil keeps an empty result list encoding as [] rather than null.
func nonNil[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

func success(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return failure(err), nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
}

func failure(err error) *mcp.CallToolResult {
	msg := err.Error()
	if errors.Is(err, session.ErrNotFound) {
		msg = session.ErrNotFound.Error()
	}
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}
