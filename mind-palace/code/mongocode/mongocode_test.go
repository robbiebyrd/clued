package mongocode

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/codetest"
)

// testDB connects to MIND_PALACE_TEST_MONGODB_URI and returns a database of
// its own, dropped when the test ends; it skips without the variable.
func testDB(t *testing.T) *mongo.Database {
	t.Helper()
	uri := os.Getenv("MIND_PALACE_TEST_MONGODB_URI")
	if uri == "" {
		t.Skip("set MIND_PALACE_TEST_MONGODB_URI to run the MongoDB code store suite")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Ping(ctx, nil); err != nil {
		t.Fatalf("connect mongodb: %v", err)
	}
	db := client.Database(fmt.Sprintf("mongocode_test_%d_%d", os.Getpid(), time.Now().UnixNano()))
	t.Cleanup(func() {
		db.Drop(context.Background())
		client.Disconnect(context.Background())
	})
	return db
}

var prefixes atomic.Int64

// suite runs codetest with a fresh project prefix per subtest.
func suite(t *testing.T, opts ...Option) {
	db := testDB(t)
	codetest.Run(t, func(t *testing.T) code.Store {
		prefix := ProjectPrefix("", fmt.Sprintf("github.com__org__repo%d", prefixes.Add(1)))
		s, err := Open(t.Context(), db, prefix, opts...)
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

// Runs only with MIND_PALACE_TEST_MONGODB_URI=mongodb://127.0.0.1:27017.
func TestMongoCode(t *testing.T) {
	suite(t)
}

// The ordered, idempotent writes of a deployment without transactions.
func TestMongoCodeWithoutTransactions(t *testing.T) {
	suite(t, WithoutTransactions())
}

func TestTransactionsDetected(t *testing.T) {
	db := testDB(t)
	s, err := Open(t.Context(), db, "p_")
	if err != nil {
		t.Fatal(err)
	}
	want, err := supportsTransactions(t.Context(), db.Client())
	if err != nil {
		t.Fatal(err)
	}
	if s.Transactions() != want {
		t.Fatalf("transactions %v, deployment supports them: %v", s.Transactions(), want)
	}
	t.Logf("deployment supports transactions: %v", want)
	s, err = Open(t.Context(), db, "q_", WithoutTransactions())
	if err != nil {
		t.Fatal(err)
	}
	if s.Transactions() {
		t.Fatal("WithoutTransactions still uses transactions")
	}
}

// Collections are named like store/mongostore's: the project prefix, then
// the collection, so two projects in one database never meet.
func TestCollectionsArePrefixedByProject(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	a, err := Open(ctx, db, ProjectPrefix("srv_", "github.com__org__a"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(ctx, db, ProjectPrefix("srv_", "github.com__org__b"))
	if err != nil {
		t.Fatal(err)
	}
	names, err := db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"srv_github.com__org__a_", "srv_github.com__org__b_"} {
		for _, n := range []string{"code_blobs", "code_trees", "code_worktrees", "code_changes", "code_counters"} {
			if !slices.Contains(names, p+n) {
				t.Errorf("collection %s missing from %v", p+n, names)
			}
		}
	}

	const wt = "wt_0123456789abcdef"
	if err := a.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "acct", Refs: []string{"main"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Commit(ctx, code.Change{Owner: "acct", Worktree: wt, Ref: "main",
		Upserts: []code.Entry{{Path: "a.go", Size: 1, Mode: "100644", Dirty: true, Unpushed: true}}}); err != nil {
		t.Fatal(err)
	}
	if ws, err := b.Worktrees(ctx); err != nil || len(ws) != 0 {
		t.Fatalf("other project's worktrees: %v %v", ws, err)
	}
	if cs, err := b.Changes(ctx, 0, 10); err != nil || len(cs) != 0 {
		t.Fatalf("other project's changes: %v %v", cs, err)
	}
	if _, err := b.Commit(ctx, code.Change{Worktree: wt, Ref: "main"}); err == nil {
		t.Fatal("commit to another project's worktree succeeded")
	}
}

// Without a transaction, a commit that failed after writing its tree is
// completed by the retry: the tree ends as the change says and the log holds
// it.
func TestRetryAfterPartialCommitWithoutTransactions(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	s, err := Open(ctx, db, "partial_", WithoutTransactions())
	if err != nil {
		t.Fatal(err)
	}
	const wt = "wt_0123456789abcdef"
	if err := s.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "acct", Refs: []string{"main"}}); err != nil {
		t.Fatal(err)
	}
	c := code.Change{Owner: "acct", Worktree: wt, Ref: "main", SessionID: "s1",
		Upserts: []code.Entry{{Path: "a.go", Size: 1, Mode: "100644", Dirty: true, Unpushed: true, Session: "s1"}}}
	// The first attempt's tree write, then a lost connection.
	if err := s.applyTree(ctx, c, []entryDoc{toEntryDoc(c.Upserts[0])}); err != nil {
		t.Fatal(err)
	}
	seq, err := s.Commit(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("seq %d, want 1", seq)
	}
	tree, err := s.Tree(ctx, wt, "main")
	if err != nil || len(tree) != 1 || tree[0] != c.Upserts[0] {
		t.Fatalf("tree %#v %v", tree, err)
	}
	cs, err := s.Changes(ctx, 0, 10)
	if err != nil || len(cs) != 1 || cs[0].Upserts[0] != c.Upserts[0] {
		t.Fatalf("changes %#v %v", cs, err)
	}
}

// Session ids and refs are data, never field paths of the update pipeline.
func TestDollarValuesAreLiterals(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	s, err := Open(ctx, db, "dollar_")
	if err != nil {
		t.Fatal(err)
	}
	const wt = "wt_0123456789abcdef"
	if err := s.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "acct", Refs: []string{"$owner"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchSession(ctx, wt, "$owner", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, code.Change{Owner: "acct", Worktree: wt, Ref: "$root", SessionID: "$$ss"}); err != nil {
		t.Fatal(err)
	}
	ws, err := s.Worktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w := ws[0]
	slices.Sort(w.Refs)
	if !slices.Equal(w.Refs, []string{"$owner", "$root"}) {
		t.Fatalf("refs %v", w.Refs)
	}
	if len(w.Sessions) != 2 || w.Sessions[0].ID != "$owner" || !w.Sessions[0].Ended || w.Sessions[1].ID != "$$ss" {
		t.Fatalf("sessions %#v", w.Sessions)
	}
}

// Paths and refs reach the database only as digests: a delete and a full
// reconcile remove exactly the entries they name, in batches, whatever
// characters the paths hold.
func TestDeletesByDigestInBatches(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	s, err := Open(ctx, db, "digest_")
	if err != nil {
		t.Fatal(err)
	}
	old := deleteBatch
	deleteBatch = 3
	t.Cleanup(func() { deleteBatch = old })

	const wt = "wt_0123456789abcdef"
	if err := s.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "acct", Refs: []string{"main"}}); err != nil {
		t.Fatal(err)
	}
	entry := func(p string) code.Entry {
		return code.Entry{Path: p, Size: 1, Mode: "100644", Dirty: true, Unpushed: true}
	}
	var all []code.Entry
	for i := range 10 {
		all = append(all, entry(fmt.Sprintf("$d/%d.go", i)))
	}
	all = append(all, entry(".*"), entry("{\"$ne\":1}"))
	for _, ref := range []string{"main", "other"} {
		if _, err := s.Commit(ctx, code.Change{Owner: "acct", Worktree: wt, Ref: ref, Full: true, Upserts: all}); err != nil {
			t.Fatal(err)
		}
	}
	paths := func(ref string) []string {
		t.Helper()
		tree, err := s.Tree(ctx, wt, ref)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range tree {
			out = append(out, e.Path)
		}
		return out
	}

	// Deletes: five paths across two batches, plus one the tree lacks.
	if _, err := s.Commit(ctx, code.Change{Owner: "acct", Worktree: wt, Ref: "main",
		Deletes: []string{"$d/0.go", "$d/1.go", "$d/2.go", ".*", "{\"$ne\":1}", "absent.go"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := paths("main"), []string{"$d/3.go", "$d/4.go", "$d/5.go", "$d/6.go", "$d/7.go", "$d/8.go", "$d/9.go"}; !slices.Equal(got, want) {
		t.Fatalf("after deletes: %v, want %v", got, want)
	}

	// A full reconcile keeping two paths prunes the other five in batches.
	if _, err := s.Commit(ctx, code.Change{Owner: "acct", Worktree: wt, Ref: "main", Full: true,
		Upserts: []code.Entry{entry("$d/4.go"), entry("new.go")}}); err != nil {
		t.Fatal(err)
	}
	if got, want := paths("main"), []string{"$d/4.go", "new.go"}; !slices.Equal(got, want) {
		t.Fatalf("after full: %v, want %v", got, want)
	}
	// The other ref's tree is untouched.
	if got := paths("other"); len(got) != len(all) {
		t.Fatalf("other ref: %v", got)
	}
}
