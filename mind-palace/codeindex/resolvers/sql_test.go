package resolvers_test

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeindex"
	"github.com/robbiebyrd/clued/mind-palace/codeindex/resolvers"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// sqlSrc builds, by hand, the facts the SQL extractor gives for one file
// (codeparse/queries/sql): USE <db> as the package, which prefixes
// qualified names; a schema-qualified name as its last part qualified by
// the schema, nested under the file's CREATE SCHEMA when it has one;
// columns under their table; a trigger named after its table at top
// level; quotes stripped. A file's content is its facts as JSON.
type sqlSrc struct {
	f    codeparse.Facts
	line int
}

func sqlFile(use string) *sqlSrc { return &sqlSrc{f: codeparse.Facts{Lang: "sql", Package: use}} }

func (s *sqlSrc) pos() codeparse.Pos {
	s.line++
	return codeparse.Pos{Line: s.line, Offset: s.line * 100}
}

// def adds a definition with its id (= qualified name) and parent id.
func (s *sqlSrc) def(kind, id, parent string) *sqlSrc {
	name := id
	if i := strings.LastIndex(id, "."); i >= 0 {
		name = id[i+1:]
	}
	at := s.pos()
	s.f.Symbols = append(s.f.Symbols, codeparse.Symbol{ID: id, Kind: kind, Name: name, QualName: id,
		Start: at, End: at, Parent: parent, Exported: true})
	return s
}

func (s *sqlSrc) schema(name string) *sqlSrc { return s.def("schema", name, "") }

// table adds a table and its columns; parent is the schema symbol's id or "".
func (s *sqlSrc) table(id, parent string, columns ...string) *sqlSrc {
	s.def("table", id, parent)
	for _, c := range columns {
		s.def("column", id+"."+c, id)
	}
	return s
}

// ref adds a reference from enclosing ("" at file level) to target, the
// name with its qualifier as written (billing.users, u.email, invoices).
func (s *sqlSrc) ref(kind, enclosing, target string) *sqlSrc {
	qual, name := "", target
	if i := strings.LastIndex(target, "."); i >= 0 {
		qual, name = target[:i], target[i+1:]
	}
	s.f.Refs = append(s.f.Refs, codeparse.Ref{Name: name, Qualifier: qual, Kind: kind, Start: s.pos(), Enclosing: enclosing})
	return s
}

func (s *sqlSrc) table2(enclosing, target string) *sqlSrc {
	return s.ref(codeparse.RefType, enclosing, target)
}

func (s *sqlSrc) src() string {
	b, err := json.Marshal(s.f)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// sqlView is a resolvers.View over a fixed set of SQL files.
type sqlView struct{ files map[string]*resolvers.File }

func (v sqlView) Layer() string { return codegraph.BaseLayerID }
func (v sqlView) Has(p string) bool {
	_, ok := v.files[p]
	return ok
}
func (v sqlView) Content(context.Context, string) ([]byte, bool, error) { return nil, false, nil }
func (v sqlView) Package(family, key string) []*resolvers.File {
	var out []*resolvers.File
	for _, f := range v.files {
		if f.Family == family && f.Package == key {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func sqlResolver(t *testing.T) resolvers.Resolver {
	t.Helper()
	family, r, ok := resolvers.ForLang("sql")
	if !ok || family != codeparse.FamilySQL {
		t.Fatalf("ForLang(sql) = %q, %v", family, ok)
	}
	return r
}

func newSQLView(t *testing.T, tree map[string]*sqlSrc) sqlView {
	t.Helper()
	r := sqlResolver(t)
	v := sqlView{files: map[string]*resolvers.File{}}
	for p, s := range tree {
		facts := s.f
		v.files[p] = &resolvers.File{Path: p, Layer: codegraph.BaseLayerID, Lang: "sql", Family: codeparse.FamilySQL, Facts: &facts}
	}
	for _, f := range v.files {
		key, err := r.Package(context.Background(), v, f)
		if err != nil {
			t.Fatal(err)
		}
		f.Package = key
	}
	return v
}

func TestRules_sql(t *testing.T) {
	r := sqlResolver(t)
	rules := r.Rules()
	if !reflect.DeepEqual(rules.Separators, []string{"."}) || !rules.CaseInsensitive || !rules.NameFallback || !rules.MemberFallback {
		t.Errorf("rules = %+v", rules)
	}
	for in, want := range map[string]string{
		"Users":     "users",
		` "Users" `: "Users",
		"`Shop`":    "Shop",
		"[dbo]":     "dbo",
		`"a""b"`:    `a"b`,
		"":          "",
		`"`:         `"`,
	} {
		if got := resolvers.SQLIdent(in); got != want {
			t.Errorf("SQLIdent(%q) = %q, want %q", in, got, want)
		}
	}
	for use, want := range map[string]string{"": "public", "Shop": "shop", "`Shop`": "Shop", "  ": "public"} {
		f := &resolvers.File{Path: "x.sql", Family: codeparse.FamilySQL, Facts: &codeparse.Facts{Package: use}}
		if key, err := r.Package(context.Background(), sqlView{}, f); err != nil || key != want {
			t.Errorf("Package(USE %q) = %q, %v; want %q", use, key, err, want)
		}
	}
	if got := resolvers.SQLFileSchema(nil); got != resolvers.SQLDefaultSchema {
		t.Errorf("SQLFileSchema(nil) = %q", got)
	}
}

func TestScope_sql(t *testing.T) {
	tree := map[string]*sqlSrc{
		"schema.sql": sqlFile("").schema("billing").table("billing.users", "billing", "email").
			table("invoices", "", "user_id").def("trigger", "invoices.t", "").def("column", "orphans.c", ""),
		"shop.sql": sqlFile("shop").table("shop.customers", ""),
		"q.sql": sqlFile("").
			table2("", "public.invoices").    // bound: invoices is written without a schema in a public file
			table2("", "PUBLIC.invoices.x").  // schema.table.column: bound as written
			table2("", "billing.users").      // spelled billing.users: free
			table2("", "shop.customers").     // spelled by USE: free
			table2("", "public.t").           // a trigger on a public table: bound too
			table2("", "public.c").           // a column is no schema object
			table2("", "audit.events").       // no such schema
			table2("", "u.email").            // an alias
			table2("", "db.public.invoices"). // a database prefix: free
			table2("", "invoices"),
	}
	v := newSQLView(t, tree)
	r := sqlResolver(t)
	sc, err := r.Scope(context.Background(), v, v.files["q.sql"])
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"public": {"public"}, "PUBLIC": {"public"}}
	if !reflect.DeepEqual(sc.Qualifiers, want) || sc.Names != nil || sc.Open != nil {
		t.Errorf("scope = %+v, want qualifiers %v", sc, want)
	}
	if sc, err := r.Scope(context.Background(), v, &resolvers.File{Path: "x.sql"}); err != nil || sc.Qualifiers != nil {
		t.Errorf("no facts: %+v, %v", sc, err)
	}
}

func TestVisible_sql(t *testing.T) {
	tree := map[string]*sqlSrc{
		"schema.sql": sqlFile("").schema("billing").table("billing.users", "billing", "email").
			table("invoices", "", "user_id").table("audit.log", "").
			def("trigger", "billing.users.touch", "").def("index", "invoices_idx", ""),
		"shop.sql": sqlFile("shop").table("shop.customers", ""),
		"q.sql":    sqlFile(""),
		"m.sql":    sqlFile("shop"),
	}
	v := newSQLView(t, tree)
	r := sqlResolver(t)
	sym := func(p, id string) resolvers.Candidate {
		f := v.files[p]
		for i := range f.Facts.Symbols {
			if f.Facts.Symbols[i].ID == id {
				return resolvers.Candidate{File: f, Symbol: &f.Facts.Symbols[i]}
			}
		}
		t.Fatalf("no symbol %s#%s", p, id)
		return resolvers.Candidate{}
	}
	q, m := v.files["q.sql"], v.files["m.sql"]
	for _, c := range []struct {
		from *resolvers.File
		c    resolvers.Candidate
		step resolvers.Step
		want bool
	}{
		{q, sym("schema.sql", "invoices"), resolvers.StepPackage, true},  // public
		{m, sym("schema.sql", "invoices"), resolvers.StepPackage, false}, // m uses shop
		{q, sym("schema.sql", "audit.log"), resolvers.StepFile, false},   // spelled audit
		{q, sym("schema.sql", "invoices_idx"), resolvers.StepFile, true},
		{q, sym("schema.sql", "billing.users.touch"), resolvers.StepFile, false}, // on billing.users
		{m, sym("shop.sql", "shop.customers"), resolvers.StepPackage, true},
		{q, sym("schema.sql", "invoices"), resolvers.StepImport, true},           // keyed public, lives in public
		{q, sym("schema.sql", "audit.log"), resolvers.StepImport, false},         // keyed public, lives in audit
		{q, sym("shop.sql", "shop.customers"), resolvers.StepImport, true},       // keyed shop, spelled shop
		{q, sym("schema.sql", "billing.users"), resolvers.StepName, true},        // a schema object under CREATE SCHEMA
		{q, sym("schema.sql", "billing.users.email"), resolvers.StepName, false}, // a column
		{q, sym("schema.sql", "billing"), resolvers.StepName, false},             // a schema
		{q, sym("schema.sql", "billing.users.email"), resolvers.StepMember, true},
		{q, sym("schema.sql", "billing.users"), resolvers.StepMember, false}, // never a member of a qualifier
		{q, sym("schema.sql", "billing.users"), resolvers.StepQualified, true},
		{q, sym("schema.sql", "billing.users"), resolvers.StepScope, true},
		{q, sym("schema.sql", "billing.users.email"), resolvers.StepScope, false}, // a column is no bare name's target
	} {
		if got := r.Visible(c.from, c.c, c.step); got != c.want {
			t.Errorf("Visible(%s, %s, %s) = %v, want %v", c.from.Path, c.c.Symbol.ID, c.step, got, c.want)
		}
	}
}

// sqlIndex indexes tree (path → content) as one commit with parse and
// returns the neighbors of a symbol in the base view, as target (or
// source, with from) → confidence.
func sqlIndex(t *testing.T, tree map[string]string, parse codeindex.ParseFunc) func(rel codegraph.Relation, path, id string, from bool) map[string]string {
	t.Helper()
	ctx := context.Background()
	const proj = "u0123456789abcdef__sql-resolver"
	const wt = "wt_0000000000000001"
	store, graphs := memcode.New(), memgraph.New()
	svc, err := codeindex.New(codeindex.Options{
		Projects: func(context.Context) ([]string, error) { return []string{proj}, nil },
		Code: func(_ context.Context, p string) (code.Store, error) {
			if p != proj {
				return nil, code.ErrNotFound
			}
			return store, nil
		},
		Graphs:  graphs,
		Cursors: codeindex.NewMemCursors(nil),
		Apply:   codeindex.NewResolver(codeindex.ResolverOptions{}),
		Parse:   parse,
		Holder:  "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	head := sqlOID("commit main")
	ch := code.Change{Worktree: wt, Ref: "main", Head: head, Upstream: head, Full: true}
	for p, c := range tree {
		sum := sha256.Sum256([]byte(c))
		sha := hex.EncodeToString(sum[:])
		if err := store.PutBlob(ctx, code.Blob{SHA256: sha, Size: int64(len(c)), Content: []byte(c)}); err != nil {
			t.Fatal(err)
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: sha, Size: int64(len(c)), GitOID: sqlOID(c), Mode: "100644", Lang: "sql"})
	}
	if _, err := store.Commit(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Sync(ctx, proj); err != nil {
		t.Fatal(err)
	}
	g, err := graphs.Open(ctx, proj)
	if err != nil {
		t.Fatal(err)
	}
	return func(rel codegraph.Relation, path, id string, from bool) map[string]string {
		t.Helper()
		rows, err := g.Neighbors(ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(path, id), Relation: rel})
		if err != nil {
			t.Fatalf("%s of %s#%s: %v", rel, path, id, err)
		}
		out := map[string]string{}
		for _, r := range rows {
			if from {
				out[r.From.ID] = r.Confidence
			} else {
				out[r.To.ID] = r.Confidence
			}
		}
		return out
	}
}

func wantSQL(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %v\nwant %v", what, got, want)
	}
}

// sqlJSONFacts reads the facts an sqlSrc wrote.
func sqlJSONFacts(p string, content []byte) (codeparse.Facts, error) {
	if !strings.HasSuffix(p, ".sql") {
		return codeparse.Facts{ParseError: codeparse.ParseNoGrammar}, nil
	}
	var f codeparse.Facts
	err := json.Unmarshal(content, &f)
	return f, err
}

// TestResolve_sql runs the engine over SQL files of hand-built facts: the
// edges the references of a view and a MySQL table get in the base view.
func TestResolve_sql(t *testing.T) {
	const report = "report"
	tree := map[string]string{
		"db/schema.sql": sqlFile("").schema("billing").
			table("billing.users", "billing", "email", "team_id").
			table("billing.teams", "billing", "id").
			def("view", "billing.active_users", "billing").
			def("function", "billing.user_total", "billing").
			table("invoices", "", "user_id", "total").
			def("function", "touch_user", "").
			def("trigger", "billing.users.users_touch", "").
			table2("billing.users.team_id", "billing.teams").
			table2("invoices", "billing.users").
			ref(codeparse.RefCall, "billing.users.users_touch", "touch_user").
			src(),
		"db/audit.sql":  sqlFile("").schema("audit").table("audit.events", "audit").src(),
		"db/legacy.sql": sqlFile("").table("events", "").table("sessions", "").src(),
		"db/dup.sql":    sqlFile("").table("sessions", "").src(),
		"db/shop.sql": sqlFile("Shop").
			table("Shop.customers", "", "id").
			table("Shop.orders", "", "customer_id").
			table2("Shop.orders.customer_id", "customers"). // the file's own database
			table2("Shop.orders", "invoices").              // public, not shop: name
			src(),
		"db/report.sql": sqlFile("").def("view", report, "").
			table2(report, "invoices").        // public: exact
			table2(report, "INVOICES").        // without case
			table2(report, "public.invoices"). // bound public: exact
			ref(codeparse.RefValue, report, "public.invoices.user_id").
			table2(report, "billing.users"). // qualified
			table2(report, "Billing.Teams"). // qualified, without case
			ref(codeparse.RefCall, report, "billing.user_total").
			table2(report, "events").                   // public's events, not audit's
			table2(report, "audit.events").             // audit's
			table2(report, "sessions").                 // two in public: name, by path
			table2(report, "active_users").             // only billing has one: name
			table2(report, "customers").                // only shop has one: name
			ref(codeparse.RefValue, report, "u.email"). // an alias: a column, name
			table2(report, "recent").                   // a CTE: nothing
			table2(report, "audit.missing").
			src(),
		"README.md": "# db\n",
	}
	neighbors := sqlIndex(t, tree, sqlJSONFacts)
	refs := func(path, id string) map[string]string {
		return neighbors(codegraph.RelReferences, path, id, true)
	}
	const rep = "db/report.sql#" + report
	wantSQL(t, "invoices", refs("db/schema.sql", "invoices"), map[string]string{
		rep:                       codegraph.ConfidenceExact,
		"db/shop.sql#Shop.orders": codegraph.ConfidenceName,
	})
	wantSQL(t, "invoices.user_id", refs("db/schema.sql", "invoices.user_id"), map[string]string{rep: codegraph.ConfidenceQualified})
	wantSQL(t, "billing.users", refs("db/schema.sql", "billing.users"), map[string]string{
		rep:                      codegraph.ConfidenceQualified,
		"db/schema.sql#invoices": codegraph.ConfidenceQualified,
	})
	wantSQL(t, "billing.teams", refs("db/schema.sql", "billing.teams"), map[string]string{
		rep:                                   codegraph.ConfidenceQualified,
		"db/schema.sql#billing.users.team_id": codegraph.ConfidenceQualified,
	})
	wantSQL(t, "events", refs("db/legacy.sql", "events"), map[string]string{rep: codegraph.ConfidenceExact})
	wantSQL(t, "audit.events", refs("db/audit.sql", "audit.events"), map[string]string{rep: codegraph.ConfidenceQualified})
	wantSQL(t, "dup sessions", refs("db/dup.sql", "sessions"), map[string]string{rep: codegraph.ConfidenceName})
	wantSQL(t, "legacy sessions", refs("db/legacy.sql", "sessions"), map[string]string{})
	wantSQL(t, "active_users", refs("db/schema.sql", "billing.active_users"), map[string]string{rep: codegraph.ConfidenceName})
	wantSQL(t, "customers", refs("db/shop.sql", "Shop.customers"), map[string]string{
		rep:                                   codegraph.ConfidenceName,
		"db/shop.sql#Shop.orders.customer_id": codegraph.ConfidenceExact,
	})
	wantSQL(t, "email", refs("db/schema.sql", "billing.users.email"), map[string]string{rep: codegraph.ConfidenceName})
	wantSQL(t, "report's calls", neighbors(codegraph.RelCallees, "db/report.sql", report, false), map[string]string{
		"db/schema.sql#billing.user_total": codegraph.ConfidenceQualified,
	})
	wantSQL(t, "trigger's calls", neighbors(codegraph.RelCallees, "db/schema.sql", "billing.users.users_touch", false), map[string]string{
		"db/schema.sql#touch_user": codegraph.ConfidenceExact,
	})
}

// TestResolveGoldens_sql parses the extractor's SQL inputs
// (codeparse/testdata/sql) with codeparse.Parse, next to a file of views
// over them, and checks the edges of their references.
func TestResolveGoldens_sql(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("..", "..", "codeparse", "testdata", "sql", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	tree := map[string]string{
		"db/schema.sql":        read("schema.sql"),
		"db/queries.sql":       read("queries.sql"),
		"db/broken.sql":        read("broken.sql"),
		"mysql/mysql.sql":      read("mysql.sql"),
		"prisma/migration.sql": read("migration.sql"),
		"db/reports.sql": `-- Views over the billing schema.
CREATE VIEW team_report AS
  SELECT t.name, count(*) FROM billing.teams t
  JOIN billing.active_users u ON u.team = t.name
  JOIN public.invoices i ON true
  GROUP BY t.name;

CREATE VIEW recent_invoices AS
  WITH recent AS (SELECT * FROM invoices)
  SELECT * FROM recent JOIN ledger ON true;
`,
	}
	parse := func(p string, content []byte) (codeparse.Facts, error) { return codeparse.Parse(p, content) }
	neighbors := sqlIndex(t, tree, parse)
	refs := func(path, id string) map[string]string {
		return neighbors(codegraph.RelReferences, path, id, true)
	}
	wantSQL(t, "billing.users", refs("db/schema.sql", "billing.users"), map[string]string{
		"db/schema.sql#invoices":                  codegraph.ConfidenceQualified,
		"db/schema.sql#users_email_idx":           codegraph.ConfidenceQualified,
		"db/schema.sql#billing.active_users":      codegraph.ConfidenceQualified,
		"db/schema.sql#billing.users.users_touch": codegraph.ConfidenceQualified,
	})
	wantSQL(t, "billing.teams", refs("db/schema.sql", "billing.teams"), map[string]string{
		"db/schema.sql#billing.users.team_id": codegraph.ConfidenceQualified,
		"db/schema.sql#billing.active_users":  codegraph.ConfidenceQualified,
		"db/reports.sql#team_report":          codegraph.ConfidenceQualified,
	})
	wantSQL(t, "billing.mood", refs("db/schema.sql", "billing.mood"), map[string]string{
		"db/schema.sql#billing.users.mood": codegraph.ConfidenceQualified,
	})
	wantSQL(t, "email_address", refs("db/schema.sql", "email_address"), map[string]string{
		"db/schema.sql#billing.users.email": codegraph.ConfidenceExact,
	})
	wantSQL(t, "money_amount", refs("db/schema.sql", "money_amount"), map[string]string{
		"db/schema.sql#invoices.total": codegraph.ConfidenceExact,
	})
	wantSQL(t, "invoices", refs("db/schema.sql", "invoices"), map[string]string{
		"db/schema.sql#invoice_totals":   codegraph.ConfidenceExact,
		"db/reports.sql#team_report":     codegraph.ConfidenceExact, // public.invoices
		"db/reports.sql#recent_invoices": codegraph.ConfidenceExact,
	})
	wantSQL(t, "active_users", refs("db/schema.sql", "billing.active_users"), map[string]string{
		"db/reports.sql#team_report": codegraph.ConfidenceQualified,
	})
	wantSQL(t, "touch_user", refs("db/schema.sql", "touch_user"), map[string]string{})
	wantSQL(t, "users_touch's calls", neighbors(codegraph.RelCallees, "db/schema.sql", "billing.users.users_touch", false), map[string]string{
		"db/schema.sql#touch_user": codegraph.ConfidenceExact,
	})
	// recent is a CTE and ledger never parsed in broken.sql: neither resolves.
	wantSQL(t, "accounts", refs("db/broken.sql", "accounts"), map[string]string{
		"db/broken.sql#accounts.account_id": codegraph.ConfidenceExact,
	})
	wantSQL(t, "customers", refs("mysql/mysql.sql", "shop.customers"), map[string]string{
		"mysql/mysql.sql#shop.orders.customer_id": codegraph.ConfidenceExact,
	})
	wantSQL(t, "orders", refs("mysql/mysql.sql", "shop.orders"), map[string]string{
		"mysql/mysql.sql#shop.orders_customer_idx": codegraph.ConfidenceExact,
	})
	wantSQL(t, "Role", refs("prisma/migration.sql", "Role"), map[string]string{
		"prisma/migration.sql#User.role": codegraph.ConfidenceExact,
	})
	wantSQL(t, "User", refs("prisma/migration.sql", "User"), map[string]string{
		"prisma/migration.sql#User_email_key": codegraph.ConfidenceExact,
	})
}

func sqlOID(label string) string {
	s := sha1.Sum([]byte(label))
	return hex.EncodeToString(s[:])
}
