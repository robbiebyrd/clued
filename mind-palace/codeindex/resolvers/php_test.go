package resolvers_test

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeindex"
	"github.com/robbiebyrd/clued/mind-palace/codeindex/resolvers"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// phpGolden reads one of the PHP extraction goldens' inputs.
func phpGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "codeparse", "testdata", "php", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func phpParse(t *testing.T, p, content string) *codeparse.Facts {
	t.Helper()
	f, err := codeparse.Parse(p, []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	return &f
}

// phpView is a resolvers.View over files given by content; Package keys
// are filled by phpKeys.
type phpView struct {
	content map[string]string
	files   map[string]*resolvers.File
}

func (v *phpView) Layer() string     { return codegraph.BaseLayerID }
func (v *phpView) Has(p string) bool { _, ok := v.content[p]; return ok }
func (v *phpView) Content(_ context.Context, p string) ([]byte, bool, error) {
	c, ok := v.content[p]
	return []byte(c), ok, nil
}
func (v *phpView) Package(family, key string) []*resolvers.File {
	var out []*resolvers.File
	for _, f := range v.files {
		if f.Family == family && f.Package == key {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// newPHPView parses every .php file of tree and computes its package key.
func newPHPView(t *testing.T, tree map[string]string) *phpView {
	t.Helper()
	res, ok := resolvers.Lookup(codeparse.FamilyPHP)
	if !ok {
		t.Fatal("no php resolver")
	}
	v := &phpView{content: tree, files: map[string]*resolvers.File{}}
	var files []*resolvers.File
	for p, c := range tree {
		if filepath.Ext(p) != ".php" {
			continue
		}
		f := &resolvers.File{Path: p, Layer: codegraph.BaseLayerID, Lang: "php", Family: codeparse.FamilyPHP, Facts: phpParse(t, p, c)}
		key, err := res.Package(context.Background(), v, f)
		if err != nil {
			t.Fatal(err)
		}
		f.Package = key
		files = append(files, f)
	}
	for _, f := range files {
		v.files[f.Path] = f
	}
	return v
}

func TestPackageKeys_php(t *testing.T) {
	v := newPHPView(t, map[string]string{
		"app/Billing/Invoice.php":         phpGolden(t, "definitions.php"),
		"legacy/helpers.php":              phpGolden(t, "nested.php"),
		"views/page.php":                  phpGolden(t, "template.php"),
		"composer.json":                   `{"autoload": {"psr-4": {"App\\": "app/", "Lib\\": ["lib/", "more/"]}}, "autoload-dev": {"psr-4": {"Tests\\": "tests/"}}}`,
		"app/Lost/Thing.php":              "<?php\nclass Thing\n{\n    public function go() {}\n}\n",
		"app/Lost/script.php":             "<?php\nfunction loose() {}\n",
		"more/Deep/Item.php":              "<?php\ninterface Item {}\n",
		"tests/Unit/CaseTest.php":         "<?php\nfinal class CaseTest {}\n",
		"plugins/Braced.php":              "<?php\nnamespace Plug\\In {\n    class Braced {}\n}\n",
		"plugins/sub/composer.json":       `{"autoload": {"psr-4": {"": "src"}}}`,
		"plugins/sub/src/Pkg/Widget.php":  "<?php\nclass Widget {}\n",
		"plugins/sub/src/Pkg/notjson.php": "<?php\nclass Other {}\n",
	})
	want := map[string]string{
		"app/Billing/Invoice.php":         `app\billing`,
		"legacy/helpers.php":              "", // a braced namespace and the global block
		"views/page.php":                  "",
		"app/Lost/Thing.php":              `app\lost`, // PSR-4: the file is named after its class
		"app/Lost/script.php":             "",         // no class named after the file: global
		"more/Deep/Item.php":              `lib\deep`,
		"tests/Unit/CaseTest.php":         `tests\unit`,
		"plugins/Braced.php":              `plug\in`,
		"plugins/sub/src/Pkg/Widget.php":  "pkg", // the nearest composer.json, an empty prefix
		"plugins/sub/src/Pkg/notjson.php": "",    // Other is not the file's name
	}
	got := map[string]string{}
	for p, f := range v.files {
		got[p] = f.Package
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("package keys:\n got %v\nwant %v", got, want)
	}
}

func TestPSR4Namespace_php(t *testing.T) {
	composer := []byte(`{"autoload": {"psr-4": {"App\\": "src/", "App\\Http\\": "web/http", "Root\\": ""}}}`)
	for _, tc := range []struct {
		dir, ns string
		ok      bool
	}{
		{"src", "App", true},
		{"src/Models/Sub", `App\Models\Sub`, true},
		{"web/http/Controllers", `App\Http\Controllers`, true},
		{"", "Root", true},
		{"other/x", `Root\other\x`, true},
		{"srcx", `Root\srcx`, true},
	} {
		ns, ok := resolvers.PHPPSR4Namespace(composer, tc.dir)
		if ns != tc.ns || ok != tc.ok {
			t.Errorf("PHPPSR4Namespace(%q) = %q, %v; want %q, %v", tc.dir, ns, ok, tc.ns, tc.ok)
		}
	}
	if _, ok := resolvers.PHPPSR4Namespace([]byte(`{"autoload": {"psr-4": {"App\\": "src/"}}}`), "lib"); ok {
		t.Error("a directory outside every root mapped")
	}
	if _, ok := resolvers.PHPPSR4Namespace([]byte("not json"), "src"); ok {
		t.Error("a broken manifest mapped")
	}
}

func TestRequirePaths_php(t *testing.T) {
	for _, tc := range []struct {
		spec string
		want []string
	}{
		{"config/app.php", []string{"app/Http/config/app.php", "config/app.php"}},
		{"__DIR__ . '/../bootstrap.php'", []string{"app/bootstrap.php"}},
		{`__DIR__."/lib/x.php"`, []string{"app/Http/lib/x.php"}},
		{"dirname(__DIR__) . '/helpers.php'", []string{"app/helpers.php"}},
		{"dirname(__DIR__, 2) . '/helpers.php'", []string{"helpers.php"}},
		{"dirname(__FILE__) . '/same.php'", []string{"app/Http/same.php"}},
		{"dirname(__DIR__, 3) . '/x.php'", nil}, // above the project
		{"../../../etc/passwd", nil},
		{"/etc/passwd", nil},
		{"$path . '/x.php'", nil},
		{"__DIR__ . DIRECTORY_SEPARATOR . 'x.php'", nil},
	} {
		if got := resolvers.PHPRequirePaths("app/Http/Kernel.php", tc.spec); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("PHPRequirePaths(%q) = %v, want %v", tc.spec, got, tc.want)
		}
	}
}

func TestScope_php(t *testing.T) {
	v := newPHPView(t, map[string]string{
		"app/Http/Controllers/UserController.php": phpGolden(t, "imports.php"),
		"app/Http/bootstrap.php":                  "<?php\nnamespace Boot;\nfunction boot() {}\n",
		"config/app.php":                          "<?php\nfunction config() {}\n",
	})
	res, _ := resolvers.Lookup(codeparse.FamilyPHP)
	f := v.files["app/Http/Controllers/UserController.php"]
	if f.Package != `app\http\controllers` {
		t.Fatalf("package %q", f.Package)
	}
	s, err := res.Scope(context.Background(), v, f)
	if err != nil {
		t.Fatal(err)
	}
	wantQ := map[string][]string{
		"namespace": {`app\http\controllers`},
		"User":      {`app\models`, `app\models\user`},
		"BlogPost":  {`app\models`, `app\models\post`},
		"Mailer":    {`app\services`, `app\services\mailer`},
		"Log":       {`app\services`, `app\services\logger`},
		"DB":        {`illuminate\support\facades`, `illuminate\support\facades\db`},
		"Str":       {`illuminate\support`, `illuminate\support\str`},
	}
	wantN := map[string][]string{
		"User":        {`app\models`},
		"Mailer":      {`app\services`},
		"DB":          {`illuminate\support\facades`},
		"Str":         {`illuminate\support`},
		"helper":      {`app\support`},
		"format_date": {`app\support`},
		"VERSION":     {`app\support`},
		"LIMIT":       {`app\support`},
	}
	if !reflect.DeepEqual(s.Qualifiers, wantQ) {
		t.Errorf("qualifiers:\n got %v\nwant %v", s.Qualifiers, wantQ)
	}
	if !reflect.DeepEqual(s.Names, wantN) {
		t.Errorf("names:\n got %v\nwant %v", s.Names, wantN)
	}
	// use … as: the original name in its namespace (the namespaces are not
	// in this view, so the qualifiers above stay bound too).
	wantR := map[string][]resolvers.Original{
		"BlogPost": {{Package: `app\models`, Name: "Post"}},
		"Log":      {{Package: `app\services`, Name: "Logger"}},
		"money":    {{Package: `app\support`, Name: "format_money"}}, // use function … as money
		"APP_MODE": {{Package: `app\support`, Name: "MODE"}},         // use const … as APP_MODE
	}
	if !reflect.DeepEqual(s.Renamed, wantR) {
		t.Errorf("renamed:\n got %v\nwant %v", s.Renamed, wantR)
	}
	// The global fallback, bootstrap.php's namespace and config/app.php's
	// (global); the two other requires name no file of the view.
	if want := []string{"", "boot"}; !reflect.DeepEqual(s.Open, want) {
		t.Errorf("open = %v, want %v", s.Open, want)
	}

	if rules := res.Rules(); !rules.CaseInsensitive || rules.NameFallback || !rules.MemberFallback || !rules.AbsoluteQualifiers ||
		!reflect.DeepEqual(rules.Separators, []string{`\`, "::"}) {
		t.Errorf("rules = %+v", rules)
	}
}

func TestVisible_php(t *testing.T) {
	v := newPHPView(t, map[string]string{
		"app/Billing/Invoice.php": phpGolden(t, "definitions.php"),
		"legacy/helpers.php":      phpGolden(t, "nested.php"),
		"app/Other/Use.php":       "<?php\nnamespace App\\Other;\nfunction user() {}\n",
	})
	res, _ := resolvers.Lookup(codeparse.FamilyPHP)
	inv, legacy, user := v.files["app/Billing/Invoice.php"], v.files["legacy/helpers.php"], v.files["app/Other/Use.php"]
	sym := func(f *resolvers.File, id string) resolvers.Candidate {
		for i := range f.Facts.Symbols {
			if f.Facts.Symbols[i].ID == id {
				return resolvers.Candidate{File: f, Symbol: &f.Facts.Symbols[i]}
			}
		}
		t.Fatalf("no symbol %s in %s", id, f.Path)
		return resolvers.Candidate{}
	}
	for _, tc := range []struct {
		what string
		from *resolvers.File
		c    resolvers.Candidate
		step resolvers.Step
		want bool
	}{
		{"a method is no bare name", inv, sym(inv, `App\Billing\Invoice\amount`), resolvers.StepScope, false},
		{"a promoted property is no bare name", inv, sym(inv, `App\Billing\Invoice\__construct\number`), resolvers.StepScope, false},
		{"a nested function is", legacy, sym(legacy, `Legacy\Helpers\render\escape`), resolvers.StepScope, true},
		{"a braced namespace's function is", legacy, sym(legacy, `Legacy\Helpers\render`), resolvers.StepScope, true},
		{"a member through a qualifier", inv, sym(inv, `App\Billing\Invoice\secret`), resolvers.StepMember, true},
		{"a private member of another namespace", user, sym(inv, `App\Billing\Invoice\secret`), resolvers.StepMember, false},
		{"a public member of another namespace", user, sym(inv, `App\Billing\Invoice\amount`), resolvers.StepMember, true},
		{"the global fallback reaches a function", user, sym(legacy, "global_helper"), resolvers.StepOpen, true},
		{"but not a class", user, sym(legacy, "Route"), resolvers.StepOpen, false},
		{"nor another namespace", user, sym(inv, `App\Billing\format_amount`), resolvers.StepOpen, false},
		{"an import", user, sym(inv, `App\Billing\format_amount`), resolvers.StepImport, true},
	} {
		if got := res.Visible(tc.from, tc.c, tc.step); got != tc.want {
			t.Errorf("%s: Visible = %v, want %v", tc.what, got, tc.want)
		}
	}
}

// phpEnv indexes a tree with the real parser and the resolver.
type phpEnv struct {
	t   *testing.T
	ctx context.Context
	g   codegraph.Graph
}

const phpProject = "u0123456789abcdef__php-resolver"

func hexOf(sum []byte) string { return hex.EncodeToString(sum) }

func newPHPEnv(t *testing.T, tree map[string]string) *phpEnv {
	t.Helper()
	ctx := context.Background()
	store := memcode.New()
	graphs := memgraph.New()
	svc, err := codeindex.New(codeindex.Options{
		Projects: func(context.Context) ([]string, error) { return []string{phpProject}, nil },
		Code:     func(context.Context, string) (code.Store, error) { return store, nil },
		Graphs:   graphs,
		Cursors:  codeindex.NewMemCursors(nil),
		Apply:    codeindex.NewResolver(codeindex.ResolverOptions{}),
		Parse:    codeparse.Parse,
		Holder:   "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	wt := "wt_0000000000000001"
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	commit := sha1.Sum([]byte("commit main"))
	ch := code.Change{Worktree: wt, Ref: "main", Head: hexOf(commit[:]), Upstream: hexOf(commit[:]), Full: true}
	for p, c := range tree {
		sha := sha256.Sum256([]byte(c))
		oid := sha1.Sum([]byte(c))
		if err := store.PutBlob(ctx, code.Blob{SHA256: hexOf(sha[:]), Size: int64(len(c)), Content: []byte(c)}); err != nil {
			t.Fatal(err)
		}
		lang := "php"
		if filepath.Ext(p) != ".php" {
			lang = ""
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: hexOf(sha[:]), Size: int64(len(c)), GitOID: hexOf(oid[:]), Mode: "100644", Lang: lang})
	}
	if _, err := store.Commit(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Sync(ctx, phpProject); err != nil {
		t.Fatal(err)
	}
	g, err := graphs.Open(ctx, phpProject)
	if err != nil {
		t.Fatal(err)
	}
	return &phpEnv{t: t, ctx: ctx, g: g}
}

// edges returns the neighbours of path#id by rel in the base view, as
// path#id → confidence.
func (e *phpEnv) edges(rel codegraph.Relation, path, id string) map[string]string {
	e.t.Helper()
	rows, err := e.g.Neighbors(e.ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(path, id), Relation: rel})
	if err != nil {
		e.t.Fatalf("%s of %s#%s: %v", rel, path, id, err)
	}
	out := map[string]string{}
	for _, r := range rows {
		other := r.To.ID
		if rel == codegraph.RelCallers || rel == codegraph.RelReferences || rel == codegraph.RelImplementations {
			other = r.From.ID
		}
		out[other] = r.Confidence
	}
	return out
}

func (e *phpEnv) want(what string, got, want map[string]string) {
	e.t.Helper()
	if !reflect.DeepEqual(got, want) {
		e.t.Errorf("%s:\n got %v\nwant %v", what, got, want)
	}
}

func TestResolve_php(t *testing.T) {
	const (
		ctrl   = "app/Http/Controllers/UserController.php"
		repo   = "app/Repository/OrderRepository.php"
		legacy = "legacy/helpers.php"
	)
	tree := map[string]string{
		// The extraction goldens' inputs.
		ctrl:                      phpGolden(t, "imports.php"),
		repo:                      phpGolden(t, "references.php"),
		legacy:                    phpGolden(t, "nested.php"),
		"app/Billing/Invoice.php": phpGolden(t, "definitions.php"),
		"views/page.php":          phpGolden(t, "template.php"),
		"views/broken.php":        phpGolden(t, "broken.php"),
		// What they refer to.
		"composer.json":                 `{"autoload": {"psr-4": {"App\\": "app/"}}}`,
		"app/Models/User.php":           "<?php\nnamespace App\\Models;\n\nclass User\n{\n    public static function findOrFail(int $id): self {}\n}\n",
		"app/Models/Order.php":          "<?php\nnamespace App\\Models;\n\nclass Order\n{\n    public const TAX_RATE = 20;\n    public static function fromRow($row): self {}\n    public function toArray(): array {}\n}\n",
		"app/Services/Mailer.php":       "<?php\nnamespace App\\Services;\n\nclass Mailer\n{\n    public static function send($user, $amount): void {}\n}\n",
		"app/Services/Logger.php":       "<?php\nnamespace App\\Services;\n\nclass Logger\n{\n    public static function info(string $m, array $c): void {}\n}\n",
		"app/Support/functions.php":     "<?php\nnamespace App\\Support;\n\nfunction sum(array $lines, int $rate): int {}\n",
		"app/Contracts/Countable.php":   "<?php\nnamespace App\\Contracts;\n\ninterface Countable {}\n",
		"app/Repository/Base.php":       "<?php\nnamespace App\\Repository;\n\nabstract class BaseRepository\n{\n    public static function flush(): void {}\n}\n",
		"app/Repository/Connection.php": "<?php\nnamespace App\\Repository;\n\nclass Connection\n{\n    public function select($t, $w) {}\n    public function insert($t, $r) {}\n    public function all($t) {}\n}\n",
		"app/Repository/functions.php":  "<?php\nnamespace App\\Repository;\n\nfunction audit($o): void {}\n",
		"helpers.php":                   "<?php\nfunction event($e) {}\nfunction report($e) {}\n",
		// PSR-4 without a declaration, case-insensitive names, the global
		// fallback, and a require that decides a member.
		"app/Lost/Thing.php": "<?php\nclass Thing\n{\n    public static function go() {}\n}\n",
		"app/Uses/Caller.php": "<?php\nnamespace App\\Uses;\n\nuse App\\Lost\\Thing;\n\nfunction call()\n{\n" +
			"    Thing::go();\n    $t = new Thing();\n    \\app\\support\\SUM([], 1);\n    GLOBAL_HELPER();\n    $r = new Route('/');\n    event(1);\n}\n",
		"req/run.php":        "<?php\nnamespace Req;\n\nrequire_once __DIR__ . '/lib/Engine.php';\n\nfunction run($e)\n{\n    $e->start();\n}\n",
		"req/lib/Engine.php": "<?php\nnamespace Lib;\n\nclass Engine\n{\n    public function start() {}\n}\n",
		"other/Motor.php":    "<?php\nnamespace Other;\n\nclass Motor\n{\n    public function start() {}\n}\n",
		"req/norun.php":      "<?php\nnamespace Req;\n\nfunction norun($e)\n{\n    $e->start();\n}\n",
	}
	e := newPHPEnv(t, tree)
	const exact, qualified, name = codegraph.ConfidenceExact, codegraph.ConfidenceQualified, codegraph.ConfidenceName

	// imports.php: use, use as, group use.
	e.want("show", e.edges(codegraph.RelCallees, ctrl, `App\Http\Controllers\UserController\show`), map[string]string{
		`app/Services/Logger.php#App\Services\Logger\info`: qualified, // Log aliases Logger: Logger::info
		`app/Models/User.php#App\Models\User\findOrFail`:   qualified,
		`app/Services/Mailer.php#App\Services\Mailer\send`: qualified, // a group use
	})
	e.want("User", e.edges(codegraph.RelReferences, "app/Models/User.php", `App\Models\User`), map[string]string{
		ctrl + `#App\Http\Controllers\UserController\show`: exact,
	})

	// references.php.
	e.want("save", e.edges(codegraph.RelCallees, repo, `App\Repository\OrderRepository\save`), map[string]string{
		`app/Repository/Connection.php#App\Repository\Connection\insert`: name,
		`app/Models/Order.php#App\Models\Order\toArray`:                  name,
		"helpers.php#event": exact, // the global fallback
		`app/Repository/Base.php#App\Repository\BaseRepository\flush`: name,
		`app/Support/functions.php#App\Support\sum`:                   exact, // \App\Support\sum: the whole name
		`app/Repository/functions.php#App\Repository\audit`:           exact, // namespace\audit
	})
	e.want("find", e.edges(codegraph.RelCallees, repo, `App\Repository\OrderRepository\find`), map[string]string{
		`app/Repository/Connection.php#App\Repository\Connection\select`: name,
		`app/Models/Order.php#App\Models\Order\fromRow`:                  qualified,
	})
	// count() is PHP's count, not the method it is called from.
	e.want("count", e.edges(codegraph.RelCallees, repo, `App\Repository\OrderRepository\count`), map[string]string{
		`app/Repository/Connection.php#App\Repository\Connection\all`: name,
		"helpers.php#report": exact,
	})
	e.want("BaseRepository", e.edges(codegraph.RelImplementations, "app/Repository/Base.php", `App\Repository\BaseRepository`), map[string]string{
		repo + `#App\Repository\OrderRepository`: exact,
	})
	e.want("Countable", e.edges(codegraph.RelImplementations, "app/Contracts/Countable.php", `App\Contracts\Countable`), map[string]string{
		repo + `#App\Repository\OrderRepository`: exact,
	})
	e.want("TAX_RATE", e.edges(codegraph.RelReferences, "app/Models/Order.php", `App\Models\Order\TAX_RATE`), map[string]string{
		repo + `#App\Repository\OrderRepository\save`: qualified,
	})

	// nested.php: a fully qualified call into a braced namespace, a nested
	// function.
	e.want("global_helper", e.edges(codegraph.RelCallees, legacy, "global_helper"), map[string]string{
		legacy + `#Legacy\Helpers\render`: exact, // AbsoluteQualifiers
	})
	e.want("render", e.edges(codegraph.RelCallees, legacy, `Legacy\Helpers\render`), map[string]string{
		legacy + `#Legacy\Helpers\render\escape`: exact,
	})

	// PSR-4, case, the global fallback (functions only).
	e.want("call", e.edges(codegraph.RelCallees, "app/Uses/Caller.php", `App\Uses\call`), map[string]string{
		"app/Lost/Thing.php#Thing\\go":              qualified,
		`app/Support/functions.php#App\Support\sum`: exact, // \app\support\SUM, any case
		legacy + "#global_helper":                   exact,
		"helpers.php#event":                         exact,
	})
	e.want("Thing", e.edges(codegraph.RelReferences, "app/Lost/Thing.php", "Thing"), map[string]string{
		`app/Uses/Caller.php#App\Uses\call`: exact,
	})
	e.want("Route", e.edges(codegraph.RelReferences, legacy, "Route"), map[string]string{})

	// require: the required file's namespace wins a member through an
	// expression; without it the first by path does.
	e.want("run", e.edges(codegraph.RelCallees, "req/run.php", `Req\run`), map[string]string{
		`req/lib/Engine.php#Lib\Engine\start`: name,
	})
	e.want("norun", e.edges(codegraph.RelCallees, "req/norun.php", `Req\norun`), map[string]string{
		`other/Motor.php#Other\Motor\start`: name,
	})
}
