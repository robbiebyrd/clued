package resolvers

import (
	"context"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The C# family. A file's package is its namespace: the outermost one it
// declares (Facts.Package; a namespace nested in it is part of it, as the
// extractor keeps it out of the qualified names), "" for the global
// namespace. Code in namespace A.B sees its own namespace, then A, then the
// global namespace without qualification, so those are opened.
//
// A using directive opens a namespace (global using as well). An alias
// (using X = A.B; using X = A.B.T;) binds X as a qualifier of the namespace
// it names, or of the namespace holding the type it names. A spec is
// matched against the project's namespaces: itself when it is one, else
// the longest namespace it lies in (a nested namespace or a type of that
// namespace); a spec outside the project is kept as it is, so what it
// qualifies stays unresolved. using static T brings T's static members into
// scope; they are members, which the engine's bare-name steps do not reach,
// so they resolve through the name fallback, as inherited members do. A
// qualifier written from the root binds global to the global namespace and
// global::A.B to the namespace A.B (the longest of the qualifier's leading
// parts the project declares), so global::A.B.T.M() is T.M of A.B. An
// extension method (static string Shout(this string s)) is a member of its
// static class, reached through any receiver by the member fallback.
//
// Visibility follows C#'s accessibility: a top-level type is visible in the
// whole project (public or internal) unless declared file-local; a member
// or nested type when public or protected (Exported), or internal, and
// otherwise (private) only from a file that declares its type or a type
// nested in it. Since a partial type's parts share their qualified name,
// the private members of one part are visible from the files of every
// part. The parts of one partial type declared in one file are one
// definition: a reference reaches the first of them.
func init() { Register(codeparse.FamilyCSharp, csharp{}) }

type csharp struct{}

// "::" separates an extern alias (global::A.B) from what it qualifies; no
// qualified name holds it.
var csharpRules = Rules{Separators: []string{".", "::"}, NameFallback: true, MemberFallback: true, ReceiverMembers: true}

// csharpGlobal is the alias of the global namespace (global::A.B).
const csharpGlobal = "global"

// Rules implements Resolver.
func (csharp) Rules() Rules { return csharpRules }

// Package implements Resolver: the declared (outermost) namespace, "" for
// the global namespace.
func (csharp) Package(_ context.Context, _ View, f *File) (string, error) {
	if f.Facts == nil {
		return "", nil
	}
	return CSharpName(f.Facts.Package), nil
}

// Scope implements Resolver.
func (csharp) Scope(_ context.Context, v View, f *File) (Scope, error) {
	var s Scope
	for ns := f.Package; ns != ""; {
		i := strings.LastIndex(ns, ".")
		if i < 0 {
			ns = ""
		} else {
			ns = ns[:i]
		}
		s.AddOpen(ns)
	}
	if f.Facts == nil {
		return s, nil
	}
	// global::A.B.T: global names the global namespace, and global::A.B
	// the namespace A.B, written from the root.
	for _, r := range f.Facts.Refs {
		rest, ok := strings.CutPrefix(r.Qualifier, csharpGlobal+"::")
		if !ok {
			continue
		}
		s.AddQualifier(csharpGlobal, "")
		for ns := CSharpName(rest); ns != ""; {
			if len(v.Package(codeparse.FamilyCSharp, ns)) > 0 {
				s.AddQualifier(csharpGlobal+"::"+ns, ns)
				break
			}
			i := strings.LastIndex(ns, ".")
			if i < 0 {
				break
			}
			ns = ns[:i]
		}
	}
	for _, im := range f.Facts.Imports {
		spec := CSharpName(im.Spec)
		if spec == "" {
			continue
		}
		switch {
		case im.Kind == "static":
			// Members only: reached by the name fallback.
		case im.Alias != "":
			s.AddQualifier(im.Alias, csharpNamespace(v, spec))
		default:
			s.AddOpen(csharpNamespace(v, spec))
		}
	}
	return s, nil
}

// CSharpName normalizes a namespace or type name as written in a using
// directive or a namespace declaration: without the global:: (or another
// extern alias's) prefix, type arguments and whitespace
// (global::System.Collections.Generic.List<int> →
// System.Collections.Generic.List).
func CSharpName(name string) string {
	if i := strings.LastIndex(name, "::"); i >= 0 {
		name = name[i+2:]
	}
	if i := strings.IndexByte(name, '<'); i >= 0 {
		name = name[:i]
	}
	return strings.Join(strings.Fields(name), "")
}

// csharpNamespace is the package key a using spec names: the spec when the
// project has files of that namespace, else the longest namespace of the
// project the spec lies in (a nested namespace, or a type), else the spec.
func csharpNamespace(v View, spec string) string {
	for ns := spec; ns != ""; {
		if len(v.Package(codeparse.FamilyCSharp, ns)) > 0 {
			return ns
		}
		i := strings.LastIndex(ns, ".")
		if i < 0 {
			break
		}
		ns = ns[:i]
	}
	return spec
}

// csharpTypeKinds are the kinds a partial declaration may have.
var csharpTypeKinds = map[string]bool{
	codeparse.KindClass: true, codeparse.KindStruct: true, codeparse.KindInterface: true, "record": true,
}

// Visible implements Resolver.
func (csharp) Visible(f *File, c Candidate, _ Step) bool {
	sym := c.Symbol
	if csharpLaterPart(c) {
		return false
	}
	if c.TopLevel() {
		return c.File == f || !csharpHasModifier(sym.Signature, "file")
	}
	if sym.Exported || csharpHasModifier(sym.Signature, "internal") {
		return true
	}
	if c.File == f {
		return true
	}
	owner := ""
	for _, s := range c.File.Facts.Symbols {
		if s.ID == sym.Parent {
			owner = s.QualName
			break
		}
	}
	if owner == "" || f.Facts == nil {
		return false
	}
	for _, s := range f.Facts.Symbols {
		if s.QualName == owner || strings.HasPrefix(s.QualName, owner+".") {
			return true
		}
	}
	return false
}

// csharpLaterPart reports whether c is a part of a partial type that its
// file declares again before it (the same qualified name and kind).
func csharpLaterPart(c Candidate) bool {
	sym := c.Symbol
	if !csharpTypeKinds[sym.Kind] {
		return false
	}
	for i := range c.File.Facts.Symbols {
		s := &c.File.Facts.Symbols[i]
		if s == sym || s.ID == sym.ID {
			return false
		}
		if s.QualName == sym.QualName && s.Kind == sym.Kind {
			return true
		}
	}
	return false
}

// csharpModifierWords are the words C# allows before a declaration's
// type or kind keyword.
var csharpModifierWords = map[string]bool{
	"public": true, "private": true, "protected": true, "internal": true, "file": true,
	"static": true, "readonly": true, "sealed": true, "abstract": true, "virtual": true,
	"override": true, "extern": true, "unsafe": true, "volatile": true, "async": true,
	"partial": true, "new": true, "const": true, "required": true, "ref": true, "fixed": true,
}

// csharpHasModifier reports whether a declaration's signature carries
// modifier: one of the modifier words that lead it, after its attributes.
func csharpHasModifier(signature, modifier string) bool {
	s := strings.TrimSpace(signature)
	for strings.HasPrefix(s, "[") {
		depth, end := 0, -1
		for i, r := range s {
			if r == '[' {
				depth++
			} else if r == ']' {
				if depth--; depth == 0 {
					end = i
					break
				}
			}
		}
		if end < 0 {
			return false
		}
		s = strings.TrimSpace(s[end+1:])
	}
	for _, w := range strings.Fields(s) {
		if !csharpModifierWords[w] {
			return false
		}
		if w == modifier {
			return true
		}
	}
	return false
}
