package resolvers

import (
	"bufio"
	"bytes"
	"context"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The Go family. A package is a directory: its key is the import path, the
// module path of the nearest go.mod above the file joined with the
// directory below it (the directory itself when no go.mod is visible), so
// an import spec is the key of the package it names. An external test
// package (package foo_test) is a package of its own, keyed <path>_test.
// An import binds its alias, or else the package's name: the package clause
// of the imported files when the project holds them, otherwise the name the
// go command would guess from the path. "." opens the package, "_" binds
// nothing. Bare names never leave their package except through a dot
// import; another package's definitions must be exported. A method declared
// in another file than its type has no parent in its file's facts; it is a
// member of its receiver all the same (ReceiverMembers), so s.Serve()
// reaches it.
func init() { Register(codeparse.FamilyGo, golang{}) }

type golang struct{}

var goRules = Rules{Separators: []string{"."}, MemberFallback: true, ReceiverMembers: true}

// Rules implements Resolver.
func (golang) Rules() Rules { return goRules }

// GoModFile is the module file the Go family reads.
const GoModFile = "go.mod"

// Package implements Resolver: the file's import path.
func (golang) Package(ctx context.Context, v View, f *File) (string, error) {
	key, err := GoImportPath(ctx, v, f.Dir())
	if err != nil {
		return "", err
	}
	if f.Facts != nil && strings.HasSuffix(f.Facts.Package, "_test") {
		key += "_test"
	}
	return key, nil
}

// GoImportPath is the import path of the package in directory dir: the
// module path of the nearest visible go.mod at or above dir, joined with
// dir's path below that go.mod; dir itself when no go.mod is visible.
func GoImportPath(ctx context.Context, v View, dir string) (string, error) {
	for d := dir; ; d = path.Dir(d) {
		mod := GoModFile
		if d != "." {
			mod = d + "/" + GoModFile
		}
		if v.Has(mod) {
			content, ok, err := v.Content(ctx, mod)
			if err != nil {
				return "", err
			}
			if module := GoModulePath(content); ok && module != "" {
				if d == dir {
					return module, nil
				}
				rel := dir
				if d != "." {
					rel = strings.TrimPrefix(dir, d+"/")
				}
				return module + "/" + rel, nil
			}
		}
		if d == "." || d == "/" {
			return dir, nil
		}
	}
}

// GoModulePath returns the module path a go.mod declares, "" when it
// declares none.
func GoModulePath(gomod []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(gomod))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module")
		if !ok || (rest != "" && rest[0] != ' ' && rest[0] != '\t' && rest[0] != '"') {
			continue
		}
		rest = strings.TrimSpace(rest)
		if uq, err := strconv.Unquote(rest); err == nil {
			rest = uq
		}
		return rest
	}
	return ""
}

// Scope implements Resolver.
func (golang) Scope(_ context.Context, v View, f *File) (Scope, error) {
	var s Scope
	if f.Facts == nil {
		return s, nil
	}
	for _, im := range f.Facts.Imports {
		switch im.Alias {
		case "_":
		case ".":
			s.AddOpen(im.Spec)
		case "":
			s.AddQualifier(goPackageName(v, im.Spec), im.Spec)
		default:
			s.AddQualifier(im.Alias, im.Spec)
		}
	}
	return s, nil
}

// goPackageName is the name an import of spec binds without an alias: the
// package clause of the project's files of that package, else
// GoDefaultName.
func goPackageName(v View, spec string) string {
	for _, f := range v.Package(codeparse.FamilyGo, spec) {
		if f.Facts == nil || f.Facts.Package == "" {
			continue
		}
		name := f.Facts.Package
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		return name
	}
	return GoDefaultName(spec)
}

var goMajor = regexp.MustCompile(`^v[0-9]+$`)

// GoDefaultName guesses the package name of an import path the project
// does not hold: its last element, skipping a major-version element (v2)
// and a gopkg.in-style .vN suffix, without a go- prefix or -go suffix, and
// cut at its last remaining '.' or '-' (package names have neither).
func GoDefaultName(spec string) string {
	elems := strings.Split(strings.Trim(spec, "/"), "/")
	name := elems[len(elems)-1]
	if goMajor.MatchString(name) && len(elems) > 1 {
		name = elems[len(elems)-2]
	}
	if i := strings.LastIndex(name, ".v"); i > 0 && goMajor.MatchString(name[i+1:]) {
		name = name[:i]
	}
	name = strings.TrimPrefix(name, "go-")
	name = strings.TrimSuffix(name, "-go")
	if i := strings.LastIndexAny(name, ".-"); i >= 0 && i+1 < len(name) {
		name = name[i+1:]
	}
	return name
}

// Visible implements Resolver: the same package sees everything, another
// package only what is exported.
func (golang) Visible(f *File, c Candidate, _ Step) bool {
	return c.File.Package == f.Package || c.Symbol.Exported
}
