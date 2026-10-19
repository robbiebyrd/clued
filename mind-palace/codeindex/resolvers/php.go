package resolvers

import (
	"context"
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The PHP family. A file's package is its namespace: the `namespace A\B;`
// declaration (Facts.Package); a file whose only top-level definition is
// one braced `namespace A\B { … }` is in that namespace; any other file
// with braced namespaces, and a file with none, is in the global
// namespace, keyed "". A file without a declaration whose name matches a
// class, interface, trait or enum it defines and which lies under a
// Composer PSR-4 root (composer.json's autoload or autoload-dev, the
// nearest one above the file) is in the namespace the root maps its
// directory to, as the autoloader expects (a declaration the parser lost
// to a syntax error). Namespaces compare without case, so the keys are
// lower case.
//
// `use A\B` (and `use A\B as C`) binds the bare name B to the namespace A
// and the qualifier B (or C) to both A (B is a class: B::f, B::X) and A\B
// (B is a namespace: B\f, B\C::g); a group `use A\{B, C as D}` binds each
// clause the same way under its prefix. `use function` and `use const`
// bind the bare name only. The qualifier `namespace` (namespace\f) names
// the file's own namespace. A bare name reaches the file's enclosing
// functions and namespaces (never a class's members: those take $this,
// self:: or a class), the file, its namespace and what `use` imports by
// name, then, for functions and constants only, the global namespace, as
// PHP falls back to it. `require` and `include` (and their _once forms)
// of a path the view holds (a literal path, relative to the file or the
// project root, or __DIR__ / dirname(__DIR__) / dirname(__FILE__)
// concatenated with one) mark the required file's namespace as imported,
// so a member reached through an expression prefers it; they bring no
// bare name into scope, since what the required file defines lives in its
// own namespace. Classes, functions and methods compare without case
// (Rules.CaseInsensitive, applied to every name by the engine).
func init() { Register(codeparse.FamilyPHP, php{}) }

type php struct{}

var phpRules = Rules{Separators: []string{`\`, "::"}, CaseInsensitive: true, MemberFallback: true}

// Rules implements Resolver.
func (php) Rules() Rules { return phpRules }

// PHPComposerFile is the Composer manifest the PHP family reads for its
// PSR-4 roots.
const PHPComposerFile = "composer.json"

// phpNamespaceQualifier is the qualifier of namespace\f(), relative to the
// file's own namespace.
const phpNamespaceQualifier = "namespace"

// Import kinds of the PHP extractor.
const (
	phpUseFunction = "function"
	phpUseConst    = "const"
)

var phpRequireKinds = map[string]bool{"require": true, "require_once": true, "include": true, "include_once": true}

// Package implements Resolver: the file's namespace, lower case.
func (php) Package(ctx context.Context, v View, f *File) (string, error) {
	if f.Facts == nil {
		return "", nil
	}
	if f.Facts.Package != "" {
		return PHPNamespaceKey(f.Facts.Package), nil
	}
	var top []*codeparse.Symbol
	braced := false
	for i := range f.Facts.Symbols {
		s := &f.Facts.Symbols[i]
		if s.Parent != "" {
			continue
		}
		top = append(top, s)
		if s.Kind == codeparse.KindNamespace {
			braced = true
		}
	}
	if braced {
		if len(top) == 1 {
			return PHPNamespaceKey(top[0].Name), nil
		}
		return "", nil
	}
	stem := strings.TrimSuffix(path.Base(f.Path), path.Ext(f.Path))
	for _, s := range top {
		if phpClassLike(s.Kind) && strings.EqualFold(s.Name, stem) {
			ns, ok, err := phpPSR4Namespace(ctx, v, f.Path)
			if err != nil || !ok {
				return "", err
			}
			return PHPNamespaceKey(ns), nil
		}
	}
	return "", nil
}

// PHPNamespaceKey is the package key of a namespace as written: without
// leading or trailing backslashes, lower case ("" for the global
// namespace).
func PHPNamespaceKey(ns string) string { return strings.ToLower(strings.Trim(ns, `\`)) }

// phpClassLike reports whether kind names a class-like declaration, whose
// name a bare reference never reaches through the global fallback.
func phpClassLike(kind string) bool {
	switch kind {
	case codeparse.KindClass, codeparse.KindInterface, codeparse.KindTrait, codeparse.KindEnum:
		return true
	}
	return false
}

// phpPSR4Namespace is the namespace the nearest visible composer.json at
// or above file maps file's directory to; false when none does.
func phpPSR4Namespace(ctx context.Context, v View, file string) (string, bool, error) {
	dir := path.Dir(file)
	for d := dir; ; d = path.Dir(d) {
		manifest := PHPComposerFile
		if d != "." {
			manifest = d + "/" + PHPComposerFile
		}
		if v.Has(manifest) {
			content, ok, err := v.Content(ctx, manifest)
			if err != nil {
				return "", false, err
			}
			if ok {
				rel := dir
				if d != "." {
					rel = strings.TrimPrefix(strings.TrimPrefix(dir, d), "/")
				} else if rel == "." {
					rel = ""
				}
				ns, found := PHPPSR4Namespace(content, rel)
				return ns, found, nil
			}
		}
		if d == "." || d == "/" {
			return "", false, nil
		}
	}
}

// PHPPSR4Namespace maps dir (a directory relative to the composer.json, ""
// for its own) to a namespace by the manifest's PSR-4 roots (autoload and
// autoload-dev): the longest root at or above dir, its prefix joined with
// dir's path below the root. False when no root holds dir or the manifest
// is not JSON.
func PHPPSR4Namespace(composer []byte, dir string) (string, bool) {
	var m struct {
		Autoload struct {
			PSR4 map[string]json.RawMessage `json:"psr-4"`
		} `json:"autoload"`
		AutoloadDev struct {
			PSR4 map[string]json.RawMessage `json:"psr-4"`
		} `json:"autoload-dev"`
	}
	if json.Unmarshal(composer, &m) != nil {
		return "", false
	}
	type root struct{ prefix, dir string }
	var roots []root
	for _, psr4 := range []map[string]json.RawMessage{m.Autoload.PSR4, m.AutoloadDev.PSR4} {
		for prefix, raw := range psr4 {
			var dirs []string
			var one string
			if json.Unmarshal(raw, &one) == nil {
				dirs = []string{one}
			} else if json.Unmarshal(raw, &dirs) != nil {
				continue
			}
			for _, d := range dirs {
				d = path.Clean(strings.TrimPrefix(strings.TrimSpace(d), "./"))
				if d == "." || d == "" {
					d = ""
				}
				roots = append(roots, root{prefix: strings.Trim(prefix, `\`), dir: d})
			}
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		if len(roots[i].dir) != len(roots[j].dir) {
			return len(roots[i].dir) > len(roots[j].dir)
		}
		return roots[i].prefix < roots[j].prefix
	})
	for _, r := range roots {
		var rest string
		switch {
		case r.dir == "":
			rest = dir
		case dir == r.dir:
		case strings.HasPrefix(dir, r.dir+"/"):
			rest = dir[len(r.dir)+1:]
		default:
			continue
		}
		parts := []string{}
		if r.prefix != "" {
			parts = append(parts, r.prefix)
		}
		if rest != "" {
			parts = append(parts, strings.ReplaceAll(rest, "/", `\`))
		}
		return strings.Join(parts, `\`), true
	}
	return "", false
}

// Scope implements Resolver.
func (php) Scope(ctx context.Context, v View, f *File) (Scope, error) {
	var s Scope
	if f.Facts == nil {
		return s, nil
	}
	s.AddQualifier(phpNamespaceQualifier, f.Package)
	if f.Package != "" {
		s.AddOpen("") // functions and constants fall back to the global namespace
	}
	for _, im := range f.Facts.Imports {
		if phpRequireKinds[im.Kind] {
			if pkg, ok, err := phpRequired(ctx, v, f, im.Spec); err != nil {
				return s, err
			} else if ok {
				s.AddOpen(pkg)
			}
			continue
		}
		full := []string{strings.Trim(im.Spec, `\`)}
		if len(im.Names) > 0 {
			full = full[:0]
			for _, n := range im.Names {
				full = append(full, strings.Trim(im.Spec, `\`)+`\`+strings.Trim(n, `\`))
			}
		}
		for _, name := range full {
			parent, last := "", name
			if i := strings.LastIndex(name, `\`); i >= 0 {
				parent, last = name[:i], name[i+1:]
			}
			if last == "" {
				continue
			}
			if im.Alias == "" {
				s.AddName(last, PHPNamespaceKey(parent))
			}
			if im.Kind == phpUseFunction || im.Kind == phpUseConst {
				continue
			}
			q := im.Alias
			if q == "" {
				q = last
			}
			s.AddQualifier(q, PHPNamespaceKey(parent))
			s.AddQualifier(q, PHPNamespaceKey(name))
		}
	}
	return s, nil
}

var (
	phpDirConcat     = regexp.MustCompile(`^__DIR__\s*\.\s*(?:'([^'$\\]*)'|"([^"$\\]*)")$`)
	phpDirnameConcat = regexp.MustCompile(`^dirname\(\s*(__DIR__|__FILE__)\s*(?:,\s*([0-9]{1,2})\s*)?\)\s*\.\s*(?:'([^'$\\]*)'|"([^"$\\]*)")$`)
	phpLiteralPath   = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)
	phpDeclaration   = regexp.MustCompile(`(?m)^\s*namespace\s+\\?([A-Za-z_][A-Za-z0-9_]*(?:\\[A-Za-z_][A-Za-z0-9_]*)*)\s*[;{]`)
)

// PHPRequirePaths returns the project paths a require or include spec of
// the file at file may name, most likely first: a literal path relative to
// the file's directory and then to the project root, or __DIR__,
// dirname(__DIR__[, n]) or dirname(__FILE__[, n]) concatenated with a
// literal. Nil for any other expression or a path leaving the project.
func PHPRequirePaths(file, spec string) []string {
	dir := path.Dir(file)
	var cands []string
	switch {
	case phpDirConcat.MatchString(spec):
		m := phpDirConcat.FindStringSubmatch(spec)
		cands = []string{path.Join(dir, m[1]+m[2])}
	case phpDirnameConcat.MatchString(spec):
		m := phpDirnameConcat.FindStringSubmatch(spec)
		levels := 1
		if m[2] != "" {
			n, err := strconv.Atoi(m[2])
			if err != nil || n < 1 {
				return nil
			}
			levels = n
		}
		if m[1] == "__FILE__" {
			levels-- // dirname(__FILE__) is __DIR__
		}
		d := dir
		for range levels {
			if d == "." {
				return nil
			}
			d = path.Dir(d)
		}
		cands = []string{path.Join(d, m[3]+m[4])}
	case phpLiteralPath.MatchString(spec) && !strings.HasPrefix(spec, "/"):
		cands = []string{path.Join(dir, spec), path.Clean(spec)}
	}
	var out []string
	for _, c := range cands {
		if c == "." || c == ".." || strings.HasPrefix(c, "../") || path.IsAbs(c) || slices.Contains(out, c) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// phpRequired returns the package key of the PHP file a require or include
// of spec in f reaches in the view: the key Package gave it, found among
// the keys its first namespace declaration (or braced namespace), its
// PSR-4 root or the global namespace give.
func phpRequired(ctx context.Context, v View, f *File, spec string) (string, bool, error) {
	for _, p := range PHPRequirePaths(f.Path, spec) {
		if !v.Has(p) {
			continue
		}
		keys := []string{""}
		content, ok, err := v.Content(ctx, p)
		if err != nil {
			return "", false, err
		}
		if ok {
			if m := phpDeclaration.FindSubmatch(content); m != nil {
				keys = append(keys, PHPNamespaceKey(string(m[1])))
			}
		}
		if ns, ok, err := phpPSR4Namespace(ctx, v, p); err != nil {
			return "", false, err
		} else if ok {
			keys = append(keys, PHPNamespaceKey(ns))
		}
		for _, k := range keys {
			for _, pf := range v.Package(codeparse.FamilyPHP, k) {
				if pf.Path == p {
					return k, true, nil
				}
			}
		}
	}
	return "", false, nil
}

// Visible implements Resolver: a definition of the same namespace always,
// another namespace's only when exported (public). A bare name never
// reaches a class's members (StepScope), and the global fallback
// (StepOpen) reaches the global namespace's functions and constants only.
func (php) Visible(f *File, c Candidate, step Step) bool {
	switch step {
	case StepScope:
		if phpMember(c) {
			return false
		}
	case StepOpen:
		if c.File.Package != "" || phpClassLike(c.Symbol.Kind) {
			return false
		}
	}
	return c.File.Package == f.Package || c.Symbol.Exported
}

// phpMember reports whether c is a member of a class-like (a method, a
// property, a class constant): its parent is neither a namespace nor a
// function.
func phpMember(c Candidate) bool {
	if c.Symbol.Parent == "" {
		return false
	}
	for i := range c.File.Facts.Symbols {
		p := &c.File.Facts.Symbols[i]
		if p.ID == c.Symbol.Parent {
			return p.Kind != codeparse.KindNamespace && p.Kind != codeparse.KindFunction
		}
	}
	return true
}
