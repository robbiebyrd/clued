package falkor

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/codegraphtest"
)

// testURL is the FalkorDB the suite runs against; without it the tests that
// need a server skip.
func testURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("MIND_PALACE_TEST_FALKORDB_URL")
	if u == "" {
		t.Skip("MIND_PALACE_TEST_FALKORDB_URL is not set")
	}
	return u
}

func newOpener(t *testing.T) *Opener {
	t.Helper()
	o, err := New(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := o.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	return o
}

func TestSuite(t *testing.T) {
	testURL(t)
	codegraphtest.Run(t, func(t *testing.T) codegraph.Opener { return newOpener(t) })
}

// TestRoundTrip writes awkward strings through the parameter encoding and
// reads them back unchanged (NUL and invalid UTF-8 become U+FFFD).
func TestRoundTrip(t *testing.T) {
	o := newOpener(t)
	defer o.Close()
	ctx := context.Background()
	key := fmt.Sprintf("u%016x__falkor-roundtrip", rand.Uint64())
	gr, err := o.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	g := gr.(*Graph)
	defer g.Drop(ctx)

	cases := map[string]string{
		"quotes":    `say "hi" and 'bye'`,
		"backslash": `C:\path\to\` + `\n not a newline`,
		"controls":  "tab\tnewline\ncr\r bell\x07 esc\x1b del\x7f",
		"unicode":   "héllo wörld 日本語 🙂",
		"cypher":    "x\") DETACH DELETE n //",
		"params":    "$view {a: 1} [1,2]",
		"empty":     "",
	}
	for name, s := range cases {
		res, err := g.write(ctx, `RETURN $s, $l`, params{"s": s, "l": []string{s, s}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := str(res.rows[0][0]); got != s {
			t.Errorf("%s: got %q, want %q", name, got, s)
		}
		if got := strs(res.rows[0][1]); len(got) != 2 || got[1] != s {
			t.Errorf("%s: list got %q", name, got)
		}
	}
	res, err := g.write(ctx, `RETURN $s`, params{"s": "a\x00b\xffc"})
	if err != nil {
		t.Fatal(err)
	}
	if got := str(res.rows[0][0]); got != "a\uFFFDb\uFFFDc" {
		t.Errorf("NUL and invalid UTF-8: got %q", got)
	}
	res, err = g.write(ctx, `RETURN $m.end, $m.n, $m.b, $m.f, $m.z`, params{"m": map[string]any{
		"end": 7, "n": int64(-3), "b": true, "f": 2.5, "z": nil,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if r := res.rows[0]; num(r[0]) != 7 || num(r[1]) != -3 || !flag(r[2]) || float(r[3]) != 2.5 || r[4] != nil {
		t.Errorf("map values = %v", r)
	}
}

func TestTextQuery(t *testing.T) {
	for q, want := range map[string]string{
		"Sequence NUMBERS":       "Sequence NUMBERS",
		"sequen*":                "sequen*",
		"the page":               "page",
		"the*":                   "the*",
		`render) | @name:{x}`:    "render name x",
		`"page`:                  "page",
		`-render ~markdown`:      "render markdown",
		"a.b(c)":                 "b c",
		"*lead mid*dle render_x": "lead render_x",
		"***":                    "",
	} {
		if got := textQuery(q); got != want {
			t.Errorf("textQuery(%q) = %q, want %q", q, got, want)
		}
	}
}

func TestBuildQuery(t *testing.T) {
	got, err := buildQuery("RETURN $b, $a", params{"b": []string{`x"y`}, "a": map[string]any{"k": nil, "end": 1.0}})
	if err != nil {
		t.Fatal(err)
	}
	want := "CYPHER a={`end`:1.0,`k`:null} b=[\"x\\\"y\"] RETURN $b, $a"
	if got != want {
		t.Errorf("buildQuery = %q, want %q", got, want)
	}
	if _, err := buildQuery("RETURN 1", params{"bad name": 1}); err == nil {
		t.Error("a bad parameter name was accepted")
	}
	if _, err := buildQuery("RETURN 1", params{"m": map[string]any{"a b": 1}}); err == nil {
		t.Error("a bad map key was accepted")
	}
	if _, err := buildQuery("RETURN 1", params{"x": struct{}{}}); err == nil {
		t.Error("an unsupported type was accepted")
	}
}

func TestNew(t *testing.T) {
	type conf struct {
		addr, user, password string
		db                   int
	}
	for raw, want := range map[string]conf{
		"redis://":                    {addr: DefaultAddr},
		"redis://db.local":            {addr: "db.local:6379"},
		"redis://:pw@db.local:7000/2": {addr: "db.local:7000", password: "pw", db: 2},
		"redis://bob:pw@db.local":     {addr: "db.local:6379", user: "bob", password: "pw"},
		"redis://secret@db.local":     {addr: "db.local:6379", password: "secret"},
	} {
		o, err := New(raw)
		if err != nil {
			t.Errorf("New(%q): %v", raw, err)
			continue
		}
		if o.addr != want.addr || o.user != want.user || o.password != want.password || o.db != want.db || o.tls != nil {
			t.Errorf("New(%q) = %s %q %q %d, want %s %q %q %d", raw, o.addr, o.user, o.password, o.db,
				want.addr, want.user, want.password, want.db)
		}
	}
	o, err := New("rediss://db.local:6380")
	if err != nil || o.tls == nil || o.tls.ServerName != "db.local" {
		t.Errorf("rediss: %+v, %v", o, err)
	}
	for _, bad := range []string{"http://db.local", "redis://db.local/x", "::"} {
		if _, err := New(bad); !errors.Is(err, codegraph.ErrInvalid) {
			t.Errorf("New(%q): err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestUnavailable(t *testing.T) {
	o, err := New("redis://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	ctx := context.Background()
	if err := o.Ping(ctx); !errors.Is(err, codegraph.ErrUnavailable) {
		t.Errorf("Ping of a closed port: %v, want ErrUnavailable", err)
	}
	g, err := o.Open(ctx, "u0123456789abcdef__x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Layers(ctx); !errors.Is(err, codegraph.ErrUnavailable) {
		t.Errorf("Layers: %v, want ErrUnavailable", err)
	}
	if _, err := o.Open(ctx, "a/b"); !errors.Is(err, codegraph.ErrInvalid) {
		t.Errorf("Open with a bad key: %v", err)
	}
	o.Close()
	if _, err := o.Open(ctx, "u0123456789abcdef__x"); !errors.Is(err, codegraph.ErrUnavailable) {
		t.Errorf("Open after Close: %v", err)
	}
}

// TestIndexesAfterDrop: PutLayer creates the indexes, so a dropped graph
// rebuilt under its key searches again.
func TestIndexesAfterDrop(t *testing.T) {
	o := newOpener(t)
	defer o.Close()
	ctx := context.Background()
	g, err := o.Open(ctx, fmt.Sprintf("u%016x__falkor-reindex", rand.Uint64()))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Drop(ctx)
	ff := codegraph.FileFacts{File: codegraph.File{Layer: "base", Path: "a.go", Lang: "go"},
		Symbols: []codegraph.Symbol{{Layer: "base", Path: "a.go", ID: "a#Zebra", Name: "Zebra", Doc: "Stripes."}}}
	for round := 0; round < 2; round++ {
		if err := g.PutLayer(ctx, codegraph.Layer{ID: codegraph.BaseLayerID, Kind: codegraph.LayerBase}); err != nil {
			t.Fatal(err)
		}
		if err := g.ApplyFile(ctx, ff); err != nil {
			t.Fatal(err)
		}
		res, err := g.(*Graph).read(ctx, qIndexes, nil)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, r := range res.rows {
			types, _ := r[1].(map[string]any)
			for _, ts := range types {
				n += len(strs(ts))
			}
		}
		// Range: Layer.id, File.layer/path, Package.layer/name, Symbol.name,
		// qualName, layer, id, Chunk.layer/id; full text: three fields.
		if n != 14 {
			t.Errorf("round %d: %d index entries, want 14: %v", round, n, res.rows)
		}
		hits, err := g.SearchText(ctx, codegraph.BaseView(), codegraph.TextQuery{Query: "stripes"})
		if err != nil || len(hits) != 1 || hits[0].Symbol.ID != "a#Zebra" {
			t.Errorf("round %d: SearchText = %+v, %v", round, hits, err)
		}
		if err := g.Drop(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
