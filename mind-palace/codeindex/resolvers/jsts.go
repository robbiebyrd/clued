package resolvers

import (
	"context"
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The JavaScript and TypeScript family. A file is its own module: its
// package key is its path, so an import resolves to the one file it loads.
// A relative spec ("./util", "../lib/x.js") is joined with the importing
// file's directory and tried as written, with the TypeScript source of a
// ".js" spec ("./x.js" → x.ts), with each extension of the profile's
// resolveExtensions, then as a directory (its package.json, then its index
// file). Any other spec goes, in order, through the nearest tsconfig.json
// or jsconfig.json ("paths", then "baseUrl", following "extends"), the
// nearest package.json "imports" map ("#internal"), and the package of
// that name in the project: the package being written (self-reference),
// node_modules below the file's ancestors, or a workspace of an ancestor
// package.json; a package's "exports" map is honoured, else its "types",
// "module", "main" or "source" field, else its index. A spec found nowhere
// (react, node:fs) keys an external package the project does not hold, so
// what it names stays unresolved.
//
// An import binds what the file writes: a default or namespace import
// (import X from, import * as X from, const X = require()) binds X as a
// qualifier of the module and as the name of its default export; named
// imports (import {a}, const {a} = require()) bind the names. Re-exports
// are followed: a name imported from a module that re-exports it
// (export {a} from, export * from) comes from the module that defines it,
// and a namespace import also reaches what the module re-exports with
// export *. A re-export itself binds nothing in its own file, nor does a
// side-effect import or a dynamic import() that names nothing. A renamed
// import (TypeScript's query writes "a as b", "default as b", "type a",
// "a: b"; see JSImportName) binds b as a renamed name (Scope.Renamed): a
// reference to b, or b.x, reaches a in its module (and, through a
// re-export that renamed it, export {x as a} from, the original x there);
// default as b binds b to the default export. A module's members are
// reachable through its namespace (ns.T.f; the engine's ImportMembers
// finds module.exports = { f } members by their exported table). Profile options: moduleSystems ("esm", "commonjs") choose which
// forms count; resolveExtensions or extensions, indexFile or indexFiles,
// and tsconfig (the config file's name) tune the lookup. A file of one
// language also tries the other's extensions (a mixed project).
//
// Visibility: a definition that is not at module level (a method, a field,
// Store.prototype.get) is visible from anywhere but a #private one; a
// module-level one in another file is visible when it is exported, when an
// import names it (export {a} and module.exports = {a} written after the
// definition are not flagged exported by the extractor, but an importer
// naming a proves it is), or when its file is a script (no import, no
// export: its top-level names are globals). The name fallback never takes
// a name the file imports explicitly: one bound to a package outside the
// project stays unresolved.
func init() { Register(codeparse.FamilyJSTS, jsts{}) }

type jsts struct{}

var jstsRules = Rules{Separators: []string{"."}, NameFallback: true, MemberFallback: true, ImportMembers: true}

// Rules implements Resolver.
func (jsts) Rules() Rules { return jstsRules }

// Package implements Resolver: the file's path.
func (jsts) Package(_ context.Context, _ View, f *File) (string, error) { return f.Path, nil }

// Import kinds of the jsts extractors.
const (
	jsImportExport  = "export"
	jsImportRequire = "require"
	jsImportDynamic = "dynamic"
)

// JSExternalPrefix starts the package key of a spec the project does not
// hold (npm:react), which no file's key equals.
const JSExternalPrefix = "npm:"

// Profile options the family reads, and their defaults.
const (
	jsOptModuleSystems     = "moduleSystems"
	jsOptResolveExtensions = "resolveExtensions" // JavaScript's, comma-separated
	jsOptExtensions        = "extensions"        // TypeScript's, space-separated
	jsOptIndexFile         = "indexFile"
	jsOptIndexFiles        = "indexFiles"
	jsOptTSConfig          = "tsconfig"

	jsDefaultIndex = "index"
)

// Default extension lists when no profile is loaded, per language; each is
// followed by the family's other extensions, so a JavaScript file still
// finds a TypeScript module of a mixed project.
var (
	jsExtensions = []string{".js", ".mjs", ".cjs", ".jsx", ".json"}
	tsExtensions = []string{".ts", ".tsx", ".d.ts", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".json"}
)

// jsMaxDepth bounds extends chains, re-export chains and nested package
// lookups.
const jsMaxDepth = 16

// Scope implements Resolver.
func (jsts) Scope(ctx context.Context, v View, f *File) (Scope, error) {
	var s Scope
	if f.Facts == nil {
		return s, nil
	}
	r := &jsModules{ctx: ctx, v: v, json: map[string]map[string]json.RawMessage{}}
	esm, cjs := jsModuleSystems(f.Profile)
	for _, im := range f.Facts.Imports {
		switch im.Kind {
		case jsImportExport:
			continue // re-exports bind nothing here; importers follow them
		case jsImportRequire:
			if !cjs {
				continue
			}
		default:
			if !esm {
				continue
			}
		}
		if im.Alias == "" && len(im.Names) == 0 {
			continue // side effects only, or import() whose result is not named
		}
		mods, err := r.resolve(f, im.Spec)
		if err != nil {
			return s, err
		}
		if im.Alias != "" && im.Alias != "_" {
			ns, err := r.namespace(mods)
			if err != nil {
				return s, err
			}
			for _, p := range ns {
				s.AddQualifier(im.Alias, p)
			}
			def, _, err := r.export(mods, "default")
			if err != nil {
				return s, err
			}
			for _, o := range def {
				s.AddName(im.Alias, o.Package)
			}
		}
		for _, written := range im.Names {
			name, local := JSImportName(written)
			if name == "*" || local == "" {
				continue
			}
			from, ns, err := r.export(mods, name)
			if err != nil {
				return s, err
			}
			for _, o := range from {
				switch {
				case name == "default" || o.Name == local:
					s.AddName(local, o.Package)
				default:
					// import {a as b}, or a re-export that renamed it
					// (export {x as a} from): the reference is b, the
					// definition the original name in its module.
					s.AddRenamed(local, o.Package, o.Name)
				}
			}
			for _, p := range ns {
				s.AddQualifier(local, p)
			}
		}
	}
	return s, nil
}

// Visible implements Resolver.
func (jsts) Visible(f *File, c Candidate, step Step) bool {
	if c.File == f || c.File.Package == f.Package {
		return true
	}
	sym := c.Symbol
	if strings.HasPrefix(sym.Name, "#") {
		return false
	}
	moduleLevel := sym.Parent == "" && sym.QualName == sym.Name
	switch step {
	case StepImport, StepOpen:
		return moduleLevel
	case StepName:
		if jsImportsName(f, sym.Name) {
			return false
		}
	}
	if !moduleLevel {
		return true
	}
	return sym.Exported || !jsIsModule(c.File)
}

// jsImportsName reports whether f binds n by an import of its own.
func jsImportsName(f *File, n string) bool {
	if f.Facts == nil {
		return false
	}
	for _, im := range f.Facts.Imports {
		if im.Kind == jsImportExport {
			continue
		}
		if im.Alias == n {
			return true
		}
		for _, written := range im.Names {
			if _, local := JSImportName(written); local == n {
				return true
			}
		}
	}
	return false
}

// JSImportName splits a name an import or export lists as the extractor
// writes it into the name the module exports and the local name it binds:
// "a" → a, a; "a as b" and the destructuring "a: b" → a, b; a TypeScript
// "type a" → a, a. The JavaScript query writes the exported name alone
// ("b as c" as "b"), the TypeScript query the whole specifier.
func JSImportName(written string) (name, local string) {
	w := strings.TrimSpace(written)
	if rest, ok := strings.CutPrefix(w, "type "); ok {
		w = strings.TrimSpace(rest)
	}
	if a, b, ok := strings.Cut(w, " as "); ok {
		return strings.TrimSpace(a), strings.TrimSpace(b)
	}
	if a, b, ok := strings.Cut(w, ":"); ok {
		return strings.TrimSpace(a), strings.TrimSpace(b)
	}
	return w, w
}

// jsIsModule reports whether f is a module, not a script whose top-level
// names are globals: it has an ES import or export, binds a require() to a
// name, or exports a definition. A require() whose result is not bound (a
// UMD wrapper's) or an import() does not make a script a module.
func jsIsModule(f *File) bool {
	if f.Facts == nil {
		return true
	}
	for _, im := range f.Facts.Imports {
		switch im.Kind {
		case jsImportDynamic:
		case jsImportRequire:
			if im.Alias != "" || len(im.Names) > 0 {
				return true
			}
		default:
			return true
		}
	}
	for _, s := range f.Facts.Symbols {
		if s.Exported {
			return true
		}
	}
	return false
}

// jsModuleSystems reads the profile's moduleSystems: which of ES modules
// and CommonJS count (both when unset).
func jsModuleSystems(p *codeparse.Profile) (esm, cjs bool) {
	opt := jsOption(p, jsOptModuleSystems)
	if strings.TrimSpace(opt) == "" {
		return true, true
	}
	for _, m := range strings.Split(opt, ",") {
		switch strings.ToLower(strings.TrimSpace(m)) {
		case "esm", "es", "es6", "module":
			esm = true
		case "commonjs", "cjs":
			cjs = true
		}
	}
	return esm, cjs
}

func jsOption(p *codeparse.Profile, key string) string {
	if p == nil {
		return ""
	}
	return p.Scope.Options[key]
}

// jsExtensionsFor is the extension list for files importing from f.
func jsExtensionsFor(f *File) []string {
	var exts []string
	for _, key := range []string{jsOptResolveExtensions, jsOptExtensions} {
		for _, e := range jsList(jsOption(f.Profile, key)) {
			if !strings.HasPrefix(e, ".") {
				e = "." + e
			}
			exts = appendNew(exts, e)
		}
	}
	first, second := jsExtensions, tsExtensions
	if jsIsTypeScript(f) {
		first, second = tsExtensions, jsExtensions
	}
	for _, e := range first {
		exts = appendNew(exts, e)
	}
	for _, e := range second {
		exts = appendNew(exts, e)
	}
	return exts
}

func jsIsTypeScript(f *File) bool {
	if f.Lang != "" {
		return f.Lang == "typescript"
	}
	switch path.Ext(f.Path) {
	case ".ts", ".tsx", ".mts", ".cts":
		return true
	}
	return false
}

// jsList splits an option's list, separated by commas or spaces.
func jsList(opt string) []string {
	return strings.FieldsFunc(opt, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
}

// jsIndexFiles are the index file names (without extension) of a
// directory imported from f.
func jsIndexFiles(f *File) []string {
	var out []string
	for _, key := range []string{jsOptIndexFile, jsOptIndexFiles} {
		for _, n := range jsList(jsOption(f.Profile, key)) {
			out = appendNew(out, n)
		}
	}
	if len(out) == 0 {
		out = []string{jsDefaultIndex}
	}
	return out
}

// jsConfigNames are the config files whose paths and baseUrl apply to f,
// tried in each directory in order.
func jsConfigNames(f *File) []string {
	var out []string
	for _, n := range jsList(jsOption(f.Profile, jsOptTSConfig)) {
		out = appendNew(out, path.Base(n))
	}
	out = appendNew(out, "tsconfig.json")
	return appendNew(out, "jsconfig.json")
}

// jsModules resolves specs to package keys within one Scope call; it
// caches the JSON files it parses.
type jsModules struct {
	ctx  context.Context
	v    View
	json map[string]map[string]json.RawMessage // path → top-level keys; nil when absent or invalid
}

// resolve returns the package keys spec names from file from: one file of
// the project (with its declaration file beside it), or an external key.
func (r *jsModules) resolve(from *File, spec string) ([]string, error) {
	found, err := r.find(from, spec, 0)
	if err != nil || len(found) > 0 {
		return found, err
	}
	if jsRelative(spec) {
		if p, ok := jsJoin(from.Dir(), spec); ok {
			return []string{p}, nil // not in the project: a key no file has
		}
	}
	return []string{JSExternalPrefix + spec}, nil
}

func jsRelative(spec string) bool {
	return spec == "." || spec == ".." || strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../")
}

// jsJoin joins a project directory and a relative path; false when the
// result leaves the project.
func jsJoin(dir, rel string) (string, bool) {
	p := path.Clean(path.Join(dir, rel))
	if p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") {
		return "", false
	}
	return p, true
}

// find looks spec up in the project; nil when it is not there.
func (r *jsModules) find(from *File, spec string, depth int) ([]string, error) {
	if depth > jsMaxDepth || spec == "" {
		return nil, nil
	}
	if jsRelative(spec) {
		p, ok := jsJoin(from.Dir(), spec)
		if !ok {
			return nil, nil
		}
		return r.file(from, p, depth)
	}
	if strings.HasPrefix(spec, "/") {
		return nil, nil
	}
	if found, err := r.tsconfig(from, spec, depth); err != nil || len(found) > 0 {
		return found, err
	}
	if strings.HasPrefix(spec, "#") {
		return r.subpathImports(from, spec, depth)
	}
	return r.pkg(from, spec, depth)
}

// file resolves the module at project path p (without or with its
// extension, or a directory).
func (r *jsModules) file(from *File, p string, depth int) ([]string, error) {
	if depth > jsMaxDepth {
		return nil, nil
	}
	exts := jsExtensionsFor(from)
	var tries []string
	if jsResolvable(p, exts) {
		tries = append(tries, p)
	}
	ext := path.Ext(p)
	stem := strings.TrimSuffix(p, ext)
	switch ext { // TypeScript's ESM rule: "./x.js" names x.ts
	case ".js", ".jsx":
		tries = append(tries, stem+".ts", stem+".tsx", stem+".d.ts")
	case ".mjs":
		tries = append(tries, stem+".mts", stem+".d.mts")
	case ".cjs":
		tries = append(tries, stem+".cts", stem+".d.cts")
	}
	for _, e := range exts {
		tries = append(tries, p+e)
	}
	for _, t := range tries {
		if r.v.Has(t) {
			return jsWithDeclaration(r.v, t), nil
		}
	}
	// A directory: its package.json, then its index file.
	if pj := p + "/package.json"; r.v.Has(pj) {
		found, err := r.packageEntry(from, p, ".", depth+1)
		if err != nil || len(found) > 0 {
			return found, err
		}
	}
	for _, idx := range jsIndexFiles(from) {
		for _, e := range exts {
			if t := p + "/" + idx + e; r.v.Has(t) {
				return jsWithDeclaration(r.v, t), nil
			}
		}
	}
	return nil, nil
}

// jsResolvable reports whether p already ends with one of exts.
func jsResolvable(p string, exts []string) bool {
	for _, e := range exts {
		if strings.HasSuffix(p, e) && len(p) > len(e) {
			return true
		}
	}
	return false
}

// jsWithDeclaration returns p and its counterpart beside it: the
// declaration file that types a JavaScript file (x.js → x.d.ts), or the
// JavaScript file a declaration file types (x.d.ts → x.js), so both the
// types and the implementation are reachable.
func jsWithDeclaration(v View, p string) []string {
	out := []string{p}
	var others []string
	switch {
	case strings.HasSuffix(p, ".d.ts"):
		stem := strings.TrimSuffix(p, ".d.ts")
		others = []string{stem + ".js", stem + ".jsx"}
	case strings.HasSuffix(p, ".d.mts"):
		others = []string{strings.TrimSuffix(p, ".d.mts") + ".mjs"}
	case strings.HasSuffix(p, ".d.cts"):
		others = []string{strings.TrimSuffix(p, ".d.cts") + ".cjs"}
	default:
		ext := path.Ext(p)
		stem := strings.TrimSuffix(p, ext)
		switch ext {
		case ".js", ".jsx":
			others = []string{stem + ".d.ts"}
		case ".mjs":
			others = []string{stem + ".d.mts"}
		case ".cjs":
			others = []string{stem + ".d.cts"}
		}
	}
	for _, o := range others {
		if v.Has(o) {
			return append(out, o)
		}
	}
	return out
}

// readJSON returns the top-level keys of the JSON (or JSON with comments)
// file at p; nil when it is not visible or does not parse.
func (r *jsModules) readJSON(p string) (map[string]json.RawMessage, error) {
	if m, ok := r.json[p]; ok {
		return m, nil
	}
	r.json[p] = nil
	if !r.v.Has(p) {
		return nil, nil
	}
	content, ok, err := r.v.Content(r.ctx, p)
	if err != nil || !ok {
		return nil, err
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(StripJSONC(content), &m) != nil {
		return nil, nil
	}
	r.json[p] = m
	return m, nil
}

// jsString decodes a JSON string; "" when raw is not one.
func jsString(raw json.RawMessage) string {
	var s string
	if raw == nil || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// jsDirs returns dir and its ancestors, nearest first, "." last.
func jsDirs(dir string) []string {
	var out []string
	for d := dir; ; d = path.Dir(d) {
		out = append(out, d)
		if d == "." || d == "/" {
			return out
		}
	}
}

func jsInDir(dir, name string) string {
	if dir == "." {
		return name
	}
	return dir + "/" + name
}

// tsPaths is the effective "paths" and "baseUrl" of a tsconfig chain.
type tsPaths struct {
	baseURL   string // project directory; "" when unset
	paths     map[string][]string
	pathsBase string // directory the paths' targets are relative to
}

// tsconfig resolves spec through the nearest tsconfig.json or
// jsconfig.json above from: its paths, then its baseUrl.
func (r *jsModules) tsconfig(from *File, spec string, depth int) ([]string, error) {
	for _, d := range jsDirs(from.Dir()) {
		for _, name := range jsConfigNames(from) {
			p := jsInDir(d, name)
			if !r.v.Has(p) {
				continue
			}
			var cfg tsPaths
			if err := r.loadTSConfig(p, &cfg, 0); err != nil {
				return nil, err
			}
			return r.tsResolve(from, spec, cfg, depth)
		}
	}
	return nil, nil
}

// loadTSConfig merges the config at p over what it extends into cfg.
func (r *jsModules) loadTSConfig(p string, cfg *tsPaths, depth int) error {
	if depth > jsMaxDepth {
		return nil
	}
	m, err := r.readJSON(p)
	if err != nil || m == nil {
		return err
	}
	dir := path.Dir(p)
	var bases []string
	if raw, ok := m["extends"]; ok {
		if s := jsString(raw); s != "" {
			bases = []string{s}
		} else {
			_ = json.Unmarshal(raw, &bases)
		}
	}
	for _, b := range bases {
		if bp := r.tsExtends(dir, b); bp != "" {
			if err := r.loadTSConfig(bp, cfg, depth+1); err != nil {
				return err
			}
		}
	}
	var opts struct {
		BaseURL *string             `json:"baseUrl"`
		Paths   map[string][]string `json:"paths"`
	}
	if raw, ok := m["compilerOptions"]; ok && json.Unmarshal(raw, &opts) == nil {
		if opts.BaseURL != nil {
			if b, ok := jsJoin(dir, *opts.BaseURL); ok {
				cfg.baseURL = b
			}
		}
		if opts.Paths != nil {
			cfg.paths = opts.Paths
			cfg.pathsBase = dir
		}
	}
	return nil
}

// tsExtends returns the path of the config an "extends" value names from
// directory dir; "" when the project does not hold it.
func (r *jsModules) tsExtends(dir, ext string) string {
	var tries []string
	if jsRelative(ext) {
		p, ok := jsJoin(dir, ext)
		if !ok {
			return ""
		}
		tries = append(tries, p, p+".json", p+"/tsconfig.json")
	} else {
		for _, d := range jsDirs(dir) {
			p, ok := jsJoin(d, "node_modules/"+ext)
			if ok {
				tries = append(tries, p, p+".json", p+"/tsconfig.json")
			}
		}
	}
	for _, t := range tries {
		if strings.HasSuffix(t, ".json") && r.v.Has(t) {
			return t
		}
	}
	return ""
}

// tsResolve applies a tsconfig's paths (the exact pattern, else the
// wildcard one with the longest prefix) and then its baseUrl.
func (r *jsModules) tsResolve(from *File, spec string, cfg tsPaths, depth int) ([]string, error) {
	base := cfg.pathsBase
	if cfg.baseURL != "" {
		base = cfg.baseURL
	}
	if targets, star, ok := jsMatchPattern(cfg.paths, spec); ok {
		for _, t := range targets {
			t = strings.Replace(t, "*", star, 1)
			p, ok := jsJoin(base, t)
			if !ok {
				continue
			}
			found, err := r.file(from, p, depth+1)
			if err != nil || len(found) > 0 {
				return found, err
			}
		}
	}
	if cfg.baseURL != "" {
		if p, ok := jsJoin(cfg.baseURL, spec); ok {
			return r.file(from, p, depth+1)
		}
	}
	return nil, nil
}

// jsMatchPattern picks the key of patterns that spec matches: an exact key
// first, else the one-"*" key with the longest prefix; it returns that key's
// targets and what "*" matched.
func jsMatchPattern(patterns map[string][]string, spec string) (targets []string, star string, ok bool) {
	if t, exact := patterns[spec]; exact {
		return t, "", true
	}
	keys := make([]string, 0, len(patterns))
	for k := range patterns {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	best := -1
	for _, k := range keys {
		i := strings.Index(k, "*")
		if i < 0 || strings.Count(k, "*") != 1 {
			continue
		}
		prefix, suffix := k[:i], k[i+1:]
		if len(spec) < len(prefix)+len(suffix) || !strings.HasPrefix(spec, prefix) || !strings.HasSuffix(spec, suffix) {
			continue
		}
		if len(prefix) > best {
			best = len(prefix)
			targets, star, ok = patterns[k], spec[len(prefix):len(spec)-len(suffix)], true
		}
	}
	return targets, star, ok
}

// subpathImports resolves a "#name" spec through the nearest package.json
// "imports" map.
func (r *jsModules) subpathImports(from *File, spec string, depth int) ([]string, error) {
	for _, d := range jsDirs(from.Dir()) {
		pj := jsInDir(d, "package.json")
		m, err := r.readJSON(pj)
		if err != nil {
			return nil, err
		}
		if m == nil {
			continue
		}
		raw, ok := m["imports"]
		if !ok {
			return nil, nil
		}
		var imports map[string]json.RawMessage
		if json.Unmarshal(raw, &imports) != nil {
			return nil, nil
		}
		target, ok := jsMatchExports(imports, spec, jsConditions(from))
		if !ok {
			return nil, nil
		}
		if !jsRelative(target) {
			return r.find(from, target, depth+1)
		}
		p, ok := jsJoin(d, target)
		if !ok {
			return nil, nil
		}
		return r.file(from, p, depth+1)
	}
	return nil, nil
}

// jsConditions are the export conditions honoured for files importing
// from f, in order.
func jsConditions(f *File) []string {
	if jsIsTypeScript(f) {
		return []string{"types", "import", "module", "require", "node", "default"}
	}
	return []string{"import", "module", "require", "node", "browser", "default"}
}

// jsPackageName splits a bare spec into its package name and the subpath
// below it ("@a/b/c" → "@a/b", "./c"; "lodash" → "lodash", ".").
func jsPackageName(spec string) (name, sub string) {
	parts := strings.SplitN(spec, "/", 3)
	n := 1
	if strings.HasPrefix(spec, "@") && len(parts) > 1 {
		n = 2
	}
	if len(parts) <= n {
		return spec, "."
	}
	name = strings.Join(parts[:n], "/")
	return name, "./" + strings.TrimPrefix(spec, name+"/")
}

// pkg resolves a bare spec to a package of the project: the package being
// written, one under an ancestor's node_modules, or a workspace.
func (r *jsModules) pkg(from *File, spec string, depth int) ([]string, error) {
	name, sub := jsPackageName(spec)
	if name == "" || strings.Contains(name, ":") {
		return nil, nil // node:fs, a URL
	}
	dirs := jsDirs(from.Dir())
	for _, d := range dirs { // self-reference
		m, err := r.readJSON(jsInDir(d, "package.json"))
		if err != nil {
			return nil, err
		}
		if m != nil && jsString(m["name"]) == name {
			return r.packageEntry(from, d, sub, depth+1)
		}
	}
	for _, d := range dirs {
		dir, ok := jsJoin(d, "node_modules/"+name)
		if !ok {
			continue
		}
		if r.v.Has(dir + "/package.json") {
			return r.packageEntry(from, dir, sub, depth+1)
		}
		if p, ok := jsJoin(dir, sub); ok {
			if found, err := r.file(from, p, depth+1); err != nil || len(found) > 0 {
				return found, err
			}
		}
	}
	for _, d := range dirs { // workspaces of an ancestor package.json
		m, err := r.readJSON(jsInDir(d, "package.json"))
		if err != nil {
			return nil, err
		}
		if m == nil {
			continue
		}
		for _, dir := range jsWorkspaceDirs(d, m["workspaces"], name) {
			wm, err := r.readJSON(jsInDir(dir, "package.json"))
			if err != nil {
				return nil, err
			}
			if wm != nil && jsString(wm["name"]) == name {
				return r.packageEntry(from, dir, sub, depth+1)
			}
		}
	}
	return nil, nil
}

// jsWorkspaceDirs returns the directories a workspaces field may hold
// package name in: each pattern without a wildcard, and each "dir/*" (or
// "dir/**") with the package's unscoped name, then its scope and name
// joined by "-", as the directory. The view cannot list a directory, so a
// workspace whose directory is named otherwise is not found.
func jsWorkspaceDirs(root string, raw json.RawMessage, name string) []string {
	if raw == nil {
		return nil
	}
	var patterns []string
	if json.Unmarshal(raw, &patterns) != nil {
		var obj struct {
			Packages []string `json:"packages"`
		}
		if json.Unmarshal(raw, &obj) != nil {
			return nil
		}
		patterns = obj.Packages
	}
	short := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		short = name[i+1:]
	}
	var names []string
	names = appendNew(names, short)
	names = appendNew(names, strings.ReplaceAll(strings.TrimPrefix(name, "@"), "/", "-"))
	var out []string
	for _, pat := range patterns {
		pat = strings.TrimSuffix(strings.TrimPrefix(pat, "./"), "/")
		if strings.HasPrefix(pat, "!") {
			continue
		}
		prefix, rest, wild := strings.Cut(pat, "*")
		if !wild {
			if p, ok := jsJoin(root, pat); ok {
				out = appendNew(out, p)
			}
			continue
		}
		if strings.Trim(rest, "*") != "" || !strings.HasSuffix(prefix, "/") && prefix != "" {
			continue
		}
		for _, n := range names {
			if p, ok := jsJoin(root, prefix+n); ok {
				out = appendNew(out, p)
			}
		}
	}
	return out
}

// packageEntry resolves subpath ("." or "./x") of the package in project
// directory dir: through its "exports" when it has them, else its entry
// fields or the path below it.
func (r *jsModules) packageEntry(from *File, dir, sub string, depth int) ([]string, error) {
	if depth > jsMaxDepth {
		return nil, nil
	}
	m, err := r.readJSON(jsInDir(dir, "package.json"))
	if err != nil {
		return nil, err
	}
	if raw, ok := m["exports"]; ok && string(raw) != "null" {
		exports := jsExportsMap(raw)
		target, ok := jsMatchExports(exports, sub, jsConditions(from))
		if !ok {
			return nil, nil
		}
		p, ok := jsJoin(dir, target)
		if !ok {
			return nil, nil
		}
		return r.file(from, p, depth+1)
	}
	if sub != "." {
		p, ok := jsJoin(dir, sub)
		if !ok {
			return nil, nil
		}
		return r.file(from, p, depth+1)
	}
	fields := []string{"module", "main", "source"}
	if jsIsTypeScript(from) {
		fields = []string{"types", "typings", "module", "main", "source"}
	}
	for _, field := range fields {
		if t := jsString(m[field]); t != "" {
			if p, ok := jsJoin(dir, t); ok {
				if found, err := r.file(from, p, depth+1); err != nil || len(found) > 0 {
					return found, err
				}
			}
		}
	}
	for _, d := range []string{dir, jsInDir(dir, "src")} {
		for _, idx := range jsIndexFiles(from) {
			for _, e := range jsExtensionsFor(from) {
				if t := d + "/" + idx + e; r.v.Has(t) {
					return jsWithDeclaration(r.v, t), nil
				}
			}
		}
	}
	return nil, nil
}

// jsExportsMap normalizes an "exports" value to a subpath map: a string,
// an array or an object of conditions is the "." entry.
func jsExportsMap(raw json.RawMessage) map[string]json.RawMessage {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		subpaths := len(obj) > 0
		for k := range obj {
			if !strings.HasPrefix(k, ".") {
				subpaths = false
			}
		}
		if subpaths {
			return obj
		}
	}
	return map[string]json.RawMessage{".": raw}
}

// jsMatchExports finds key in an exports (or imports) map, exactly or by a
// one-"*" pattern with the longest prefix, and returns its target under
// conditions.
func jsMatchExports(m map[string]json.RawMessage, key string, conditions []string) (string, bool) {
	if raw, ok := m[key]; ok {
		return jsExportTarget(raw, "", conditions, 0)
	}
	bestLen := -1
	var bestRaw json.RawMessage
	bestStar := ""
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		i := strings.Index(k, "*")
		if i < 0 || strings.Count(k, "*") != 1 {
			continue
		}
		prefix, suffix := k[:i], k[i+1:]
		if len(key) < len(prefix)+len(suffix) || !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
			continue
		}
		if len(prefix) > bestLen {
			bestLen, bestRaw, bestStar = len(prefix), m[k], key[len(prefix):len(key)-len(suffix)]
		}
	}
	if bestLen < 0 {
		return "", false
	}
	return jsExportTarget(bestRaw, bestStar, conditions, 0)
}

// jsExportTarget picks the target of an exports entry: a string (with "*"
// replaced by star), the first of an array that resolves, or the first
// honoured condition of an object that resolves; null excludes it.
func jsExportTarget(raw json.RawMessage, star string, conditions []string, depth int) (string, bool) {
	if depth > jsMaxDepth {
		return "", false
	}
	if s := jsString(raw); s != "" {
		return strings.ReplaceAll(s, "*", star), true
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		for _, item := range list {
			if t, ok := jsExportTarget(item, star, conditions, depth+1); ok {
				return t, true
			}
		}
		return "", false
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return "", false
	}
	for _, c := range conditions {
		if v, ok := obj[c]; ok {
			if t, ok := jsExportTarget(v, star, conditions, depth+1); ok {
				return t, true
			}
		}
	}
	return "", false
}

// export returns the definitions a name exported by mods comes from, as
// the name each module defines it under: each module itself with n, and
// the modules it re-exports that name from (export {n} from, export {m as
// n} from, which names m there, and export * for a name other than
// default), transitively. ns are the modules a re-export binds the name to
// as a namespace (export * as n from).
func (r *jsModules) export(mods []string, n string) (from []Original, ns []string, err error) {
	seen := map[string]bool{}
	var walk func(key, n string, depth int) error
	walk = func(key, n string, depth int) error {
		if seen[key+"\x00"+n] || depth > jsMaxDepth {
			return nil
		}
		seen[key+"\x00"+n] = true
		if o := (Original{Package: key, Name: n}); !slices.Contains(from, o) {
			from = append(from, o)
		}
		for _, mf := range r.v.Package(codeparse.FamilyJSTS, key) {
			if mf.Facts == nil {
				continue
			}
			for _, im := range mf.Facts.Imports {
				if im.Kind != jsImportExport {
					continue
				}
				next := ""
				switch {
				case im.Alias != "" && len(im.Names) == 0:
					if im.Alias != n {
						continue
					}
					targets, err := r.resolve(mf, im.Spec)
					if err != nil {
						return err
					}
					more, err := r.namespace(targets)
					if err != nil {
						return err
					}
					for _, p := range more {
						ns = appendNew(ns, p)
					}
					continue
				case len(im.Names) == 0:
					if n == "default" {
						continue // export * leaves the default out
					}
					next = n
				default:
					for _, written := range im.Names {
						if orig, exported := JSImportName(written); exported == n {
							next = orig
							break
						}
					}
					if next == "" {
						continue
					}
				}
				targets, err := r.resolve(mf, im.Spec)
				if err != nil {
					return err
				}
				for _, t := range targets {
					if err := walk(t, next, depth+1); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	for _, m := range mods {
		if err := walk(m, n, 0); err != nil {
			return nil, nil, err
		}
	}
	return from, ns, nil
}

// namespace returns the modules a namespace import of mods reaches: mods
// and, transitively, every module they re-export names from without a
// namespace of their own (export * from, export {a} from).
func (r *jsModules) namespace(mods []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	var walk func(key string, depth int) error
	walk = func(key string, depth int) error {
		if seen[key] || depth > jsMaxDepth {
			return nil
		}
		seen[key] = true
		out = append(out, key)
		for _, mf := range r.v.Package(codeparse.FamilyJSTS, key) {
			if mf.Facts == nil {
				continue
			}
			for _, im := range mf.Facts.Imports {
				if im.Kind != jsImportExport || im.Alias != "" {
					continue
				}
				targets, err := r.resolve(mf, im.Spec)
				if err != nil {
					return err
				}
				for _, t := range targets {
					if err := walk(t, depth+1); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	for _, m := range mods {
		if err := walk(m, 0); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// StripJSONC removes the comments and trailing commas of JSON with
// comments (tsconfig.json, jsconfig.json), leaving strings intact.
func StripJSONC(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inString := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			out = append(out, c)
			switch c {
			case '\\':
				if i+1 < len(b) {
					i++
					out = append(out, b[i])
				}
			case '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			if i < len(b) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && (b[i] != '*' || b[i+1] != '/') {
				i++
			}
			i++ // at the closing '/'
		case c == ']' || c == '}':
			j := len(out) - 1
			for j >= 0 && (out[j] == ' ' || out[j] == '\t' || out[j] == '\n' || out[j] == '\r') {
				j--
			}
			if j >= 0 && out[j] == ',' {
				out = append(out[:j], out[j+1:]...)
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
