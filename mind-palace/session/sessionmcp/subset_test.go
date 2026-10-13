package sessionmcp_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/sessionmcp"
)

func toolNames(t *testing.T, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	must(t, err)
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestSubsetDropsTheToolsThatNeedTheLocalDisk(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		remote := newTools(seeded(t), session.Config{}).Subset(true)
		cs := connectClient(t, remote.Server(), nil)
		names := toolNames(t, cs)
		for _, absent := range []string{"restore_session", "prime_context"} {
			if names[absent] {
				t.Errorf("remote subset still lists %s", absent)
			}
		}
		for _, present := range []string{"find_sessions", "get_session_context", "get_full_session", "search_commands", "search_memory", "read_transcript"} {
			if !names[present] {
				t.Errorf("remote subset lacks %s", present)
			}
		}
		if len(names) != 6 {
			t.Errorf("tools = %v", names)
		}
		if n := len(okJSON[[]obj](t, call(t, cs, "find_sessions", obj{}))); n != 4 {
			t.Fatalf("find_sessions on the subset returned %d sessions", n)
		}
		// Subset(false) is the full set, and AddTools honours the subset too.
		full := newTools(seeded(t), session.Config{}).Subset(false)
		if n := len(toolNames(t, connectClient(t, full.Server(), nil))); n != 8 {
			t.Errorf("full set has %d tools, want 8", n)
		}
		shared := mcp.NewServer(&mcp.Implementation{Name: "shared", Version: "1"}, nil)
		remote.AddTools(shared, "pfx_")
		if names := toolNames(t, connectClient(t, shared, nil)); len(names) != 6 || names["pfx_restore_session"] || !names["pfx_find_sessions"] {
			t.Errorf("prefixed subset = %v", names)
		}
		if remote.Instructions() != sessionmcp.RemoteInstructions || full.Instructions() != sessionmcp.Instructions {
			t.Error("Instructions does not follow the subset")
		}
		if sessionmcp.RemoteInstructions == sessionmcp.Instructions || strings.Contains(sessionmcp.RemoteInstructions, "restore_session") || strings.Contains(sessionmcp.RemoteInstructions, "prime_context") {
			t.Errorf("RemoteInstructions = %q", sessionmcp.RemoteInstructions)
		}
	})
}

type accountKey struct{}

func TestAccountFromContextScopesEveryCall(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		tools := newTools(seeded(t), session.Config{})
		tools.Account = func(ctx context.Context) string {
			acc, _ := ctx.Value(accountKey{}).(string)
			return acc
		}
		srv := tools.Subset(true).Server()
		connectAs := func(acc string) *mcp.ClientSession {
			ctx := context.WithValue(context.Background(), accountKey{}, acc)
			ct, st := mcp.NewInMemoryTransports()
			ss, err := srv.Connect(ctx, st, nil)
			must(t, err)
			t.Cleanup(func() { _ = ss.Close() })
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(context.Background(), ct, nil)
			must(t, err)
			t.Cleanup(func() { _ = cs.Close() })
			return cs
		}
		if got := ids(okJSON[[]obj](t, call(t, connectAs(foreign), "find_sessions", obj{}))); !reflect.DeepEqual(got, []string{"sess-acct-2"}) {
			t.Errorf("foreign account sees %v", got)
		}
		if got := len(okJSON[[]obj](t, call(t, connectAs(account), "find_sessions", obj{}))); got != 4 {
			t.Errorf("account sees %d sessions, want 4", got)
		}
		if got := len(okJSON[[]obj](t, call(t, connectAs("nobody"), "find_sessions", obj{}))); got != 0 {
			t.Errorf("unknown account sees %d sessions, want 0", got)
		}
	})
}
