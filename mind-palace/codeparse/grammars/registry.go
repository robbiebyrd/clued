// Package grammars is the registry of the tree-sitter grammars codeparse
// parses with. Each language registers its grammar from its own file in
// this package (go.go, python.go, ...), in an init function:
//
//	package grammars
//
//	import golang "github.com/odvcencio/gotreesitter/grammars/go"
//
//	func init() { Register("go", golang.Language) }
//
// so adding a language never edits a shared file. The name is the one the
// language's profile.yaml names as its grammar (the language ID by default,
// a dialect's name for a dialect). Only gotreesitter's per-language packages
// (grammars/<lang>) may be imported, never its aggregate grammars package:
// that one embeds every grammar it ships, including GPL and MPL tables that
// go-licenses cannot see.
package grammars

import (
	"fmt"
	"sort"
	"sync"

	ts "github.com/odvcencio/gotreesitter"
)

var (
	mu       sync.RWMutex
	registry = map[string]func() *ts.Language{}
)

// Register makes a grammar available under name. language is called on
// first use (gotreesitter decodes a grammar lazily and caches it). It
// panics on an empty name, a nil function or a name registered twice: all
// three are programming errors in an init function.
func Register(name string, language func() *ts.Language) {
	if name == "" || language == nil {
		panic("grammars: Register needs a name and a language function")
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("grammars: %q registered twice", name))
	}
	registry[name] = language
}

// Lookup returns the grammar registered under name.
func Lookup(name string) (*ts.Language, bool) {
	mu.RLock()
	language, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, false
	}
	lang := language()
	return lang, lang != nil
}

// Names returns the registered grammar names, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
