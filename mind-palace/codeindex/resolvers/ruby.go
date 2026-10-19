package resolvers

import (
	"context"
	"path"
	"slices"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The Ruby family. Ruby has no package: constants live in one global
// namespace and are qualified by their lexical nesting (Shop::Order,
// Shop::Order::find, Shop::Order#total), which the extractor writes into
// QualName with "::". What a file sees is what the files it loads define,
// so a file's key is its feature: the name a require of it would use, its
// path without the extension below the first load-path directory
// (lib/shop/order.rb → shop/order) or below the root when it sits in none
// (app/models/order.rb → app/models/order). Then:
//   - require "shop/order" and load "shop/order.rb" open the feature of
//     that name; require_relative "line_item" the feature of the path
//     joined to the requiring file's directory. A required file's own
//     requires are followed (everything loaded is global), up to
//     MaxRubyRequires features. A feature outside the project (a gem, the
//     standard library) opens nothing; gem lines bind nothing;
//   - autoload :Ledger, "billing/ledger" imports the name Ledger from that
//     feature;
//   - Module::Class nesting: a bare constant is looked up through the
//     enclosing definitions of the reference (the engine's scope walk),
//     the file, the features it loads, then any definition of that name
//     (Rails and Zeitwerk load constants without a require); a qualified
//     constant is first the definition whose whole qualified name it is
//     (Shop::Mailer.deliver from anywhere: AbsoluteQualifiers, exact),
//     then one whose qualified name ends with it (Mailer.deliver inside
//     module Shop: qualified);
//   - include, extend and prepend are references the extractor gives as
//     extends: they resolve like any constant, so a class links to the
//     module it mixes in, and a bare call of a method of a top-level
//     module a loaded feature defines reaches it (ImportMembers,
//     qualified). A method reached through a receiver resolves as a member
//     (name confidence), the file's first.
//
// Every definition is public to resolution (Ruby's private is a runtime
// check on the receiver), except Rake's tasks and namespaces, which Ruby
// code only names in strings.
func init() { Register(codeparse.FamilyRuby, ruby{}) }

type ruby struct{}

var rubyRules = Rules{Separators: []string{"::", "#", "."}, NameFallback: true, MemberFallback: true, ImportMembers: true, AbsoluteQualifiers: true}

// Ruby profile options (codeparse/queries/ruby/profile.yaml).
const (
	// RubyLoadPathOption is the option naming the load-path directories,
	// comma separated, a require resolves against.
	RubyLoadPathOption = "loadPath"
	// RubyDefaultLoadPath is the load path without a profile.
	RubyDefaultLoadPath = "lib"
	// MaxRubyRequires bounds the features a file's requires open,
	// transitively.
	MaxRubyRequires = 1000
)

// Ruby import kinds (codeparse/queries/ruby/tags.scm).
const (
	rubyRequire         = "require"
	rubyRequireRelative = "require_relative"
	rubyLoad            = "load"
	rubyAutoload        = "autoload"
)

// rubyTaskKind is the kind the profile gives Rake's task form.
const rubyTaskKind = "task"

// Rules implements Resolver.
func (ruby) Rules() Rules { return rubyRules }

// Package implements Resolver: the file's feature.
func (ruby) Package(_ context.Context, _ View, f *File) (string, error) {
	return RubyFeature(f.Path, rubyLoadPath(f)), nil
}

// rubyLoadPath is the profile's load path, RubyDefaultLoadPath without one.
func rubyLoadPath(f *File) []string {
	opt := RubyDefaultLoadPath
	if f.Profile != nil {
		if o, ok := f.Profile.Scope.Options[RubyLoadPathOption]; ok {
			opt = o
		}
	}
	var out []string
	for _, d := range strings.Split(opt, ",") {
		if d = strings.Trim(strings.TrimSpace(d), "/"); d != "" && d != "." {
			out = append(out, d)
		}
	}
	return out
}

// RubyFeature is the feature of the file at p (or of a require's spec):
// the path cleaned, without a .rb or .rake extension, and below the first
// directory of loadPath it passes through (lib/shop/order.rb → shop/order,
// gems/pay/lib/pay.rb → pay).
func RubyFeature(p string, loadPath []string) string {
	p = path.Clean(strings.TrimSpace(p))
	for _, ext := range []string{".rb", ".rake"} {
		if s, ok := strings.CutSuffix(p, ext); ok && s != "" && !strings.HasSuffix(s, "/") {
			p = s
			break
		}
	}
	segs := strings.Split(p, "/")
	cut := len(segs) // the first segment below the earliest load-path directory
	for _, d := range loadPath {
		dir := strings.Split(d, "/")
		for i := 0; i+len(dir) < len(segs) && i+len(dir) < cut; i++ {
			if slices.Equal(segs[i:i+len(dir)], dir) {
				cut = i + len(dir)
				break
			}
		}
	}
	if cut < len(segs) {
		return strings.Join(segs[cut:], "/")
	}
	return p
}

// Scope implements Resolver.
func (ruby) Scope(_ context.Context, v View, f *File) (Scope, error) {
	var s Scope
	if f.Facts == nil {
		return s, nil
	}
	loadPath := rubyLoadPath(f)
	seen := map[string]bool{f.Package: true}
	var queue []string
	open := func(from *File, im codeparse.Import) {
		key, ok := rubyRequired(from, im, loadPath)
		if !ok || seen[key] || len(seen) > MaxRubyRequires {
			return
		}
		seen[key] = true
		if len(v.Package(codeparse.FamilyRuby, key)) == 0 {
			return // outside the project
		}
		s.AddOpen(key)
		queue = append(queue, key)
	}
	for _, im := range f.Facts.Imports {
		if im.Kind == rubyAutoload {
			key := RubyFeature(im.Spec, loadPath)
			if len(v.Package(codeparse.FamilyRuby, key)) == 0 {
				continue
			}
			for _, n := range im.Names {
				s.AddName(n, key)
			}
			continue
		}
		open(f, im)
	}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		for _, g := range v.Package(codeparse.FamilyRuby, key) {
			if g.Facts == nil {
				continue
			}
			for _, im := range g.Facts.Imports {
				open(g, im)
			}
		}
	}
	return s, nil
}

// rubyRequired is the feature an import of from loads: require and load
// name it from the load path (or the root), require_relative from from's
// directory. Other forms (gem, autoload) load nothing now.
func rubyRequired(from *File, im codeparse.Import, loadPath []string) (string, bool) {
	spec := strings.TrimSpace(im.Spec)
	if spec == "" {
		return "", false
	}
	switch im.Kind {
	case rubyRequire, rubyLoad, "":
		return RubyFeature(spec, loadPath), true
	case rubyRequireRelative:
		return RubyFeature(path.Join(from.Dir(), spec), loadPath), true
	}
	return "", false
}

// Visible implements Resolver: everything but Rake's tasks and namespaces.
func (ruby) Visible(_ *File, c Candidate, _ Step) bool {
	switch c.Symbol.Kind {
	case rubyTaskKind, codeparse.KindNamespace:
		return false
	}
	return true
}
