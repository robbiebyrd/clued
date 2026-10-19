package resolvers_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"reflect"
	"sort"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeindex"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// jstsProject indexes files (path → content) with the real JavaScript and
// TypeScript extractors and the resolution engine, and returns the callees
// of path#id as "path#id confidence".
func jstsProject(t *testing.T, files map[string]string) func(path, id string) []string {
	t.Helper()
	ctx := context.Background()
	const project = "u0123456789abcdef__jsts-resolver"
	store := memcode.New()
	graphs := memgraph.New()
	eng, err := codeparse.Default()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := codeindex.New(codeindex.Options{
		Projects: func(context.Context) ([]string, error) { return []string{project}, nil },
		Code:     func(context.Context, string) (code.Store, error) { return store, nil },
		Graphs:   graphs,
		Cursors:  codeindex.NewMemCursors(nil),
		Apply:    codeindex.NewResolver(codeindex.ResolverOptions{Profiles: eng.Profiles()}),
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
	head := hex.EncodeToString(sha256.New().Sum(nil))[:40]
	ch := code.Change{Worktree: wt, Ref: "main", Head: head, Upstream: head, Full: true}
	for p, content := range files {
		lang := "javascript"
		if ext := path.Ext(p); ext == ".ts" || ext == ".tsx" || ext == ".mts" || ext == ".cts" {
			lang = "typescript"
		}
		sum := sha256.Sum256([]byte(content))
		sha := hex.EncodeToString(sum[:])
		if err := store.PutBlob(ctx, code.Blob{SHA256: sha, Size: int64(len(content)), Content: []byte(content)}); err != nil {
			t.Fatal(err)
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: sha, Size: int64(len(content)), GitOID: sha[:40], Mode: "100644", Lang: lang})
	}
	if _, err := store.Commit(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Sync(ctx, project); err != nil {
		t.Fatal(err)
	}
	g, err := graphs.Open(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	return func(p, id string) []string {
		t.Helper()
		rows, err := g.Neighbors(ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(p, id), Relation: codegraph.RelCallees})
		if err != nil {
			t.Fatalf("callees of %s#%s: %v", p, id, err)
		}
		out := []string{}
		for _, r := range rows {
			out = append(out, r.To.ID+" "+r.Confidence)
		}
		sort.Strings(out)
		return out
	}
}

// TestResolve_jsts_RenamedAndMembers: a renamed import (import {a as b},
// and one a re-export renamed, export {a as b} from) reaches the original
// definition, and a member of the class a module exports is qualified
// through the import's qualifier, whatever the importer calls it
// (ImportMembers; it was a name match).
func TestResolve_jsts_RenamedAndMembers(t *testing.T) {
	callees := jstsProject(t, map[string]string{
		"src/fs.ts":    "export function writeFile(p: string): void {}\nexport function readFile(p: string): string { return p }\n",
		"src/parse.ts": "export function parse(s: string): number { return 1 }\n",
		"src/index.ts": "export { parse as parseEntry } from \"./parse\";\n",
		"src/client.ts": "export default class Client {\n  static create(): Client { return new Client() }\n}\n",
		"src/main.ts": `import { writeFile as write, readFile } from "./fs";
import { parseEntry } from "./index";

export function main(): void {
  write("a");
  readFile("b");
  parseEntry("c");
}
`,
		"src/old.js": `import api from "./client";

function run() {
  api.create();
}
`,
	})
	if got, want := callees("src/main.ts", "main"), []string{
		"src/fs.ts#readFile exact",
		"src/fs.ts#writeFile exact",
		"src/parse.ts#parse exact",
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("main:\n got %v\nwant %v", got, want)
	}
	if got, want := callees("src/old.js", "run"), []string{
		"src/client.ts#Client.create qualified",
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("run:\n got %v\nwant %v", got, want)
	}
}
