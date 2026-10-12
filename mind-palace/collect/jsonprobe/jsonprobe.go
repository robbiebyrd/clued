// Package jsonprobe answers questions about JSON files: the value at a dot
// path, the size of a container, how many elements of an array carry a given
// key/value pair, how many objects anywhere carry a key, and whether an MCP
// server keyword appears in any of the places Claude Code reads MCP
// configuration from.
//
// Every probe returns exactly one plain-text answer and never an error: a
// missing or unparsable file, an empty argument or a path that leads nowhere
// maps to the sentinels of package collect (collect.None for a value,
// collect.Zero for a count, collect.NotConfigured for MCP).
package jsonprobe

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/collect"
)

// Probe reads JSON files relative to a project root and a user home.
type Probe struct {
	Root string // project directory; "" means the working directory
	Home string // user home; "" means os.UserHomeDir()
}

// Field returns the value at dotPath in file as text: a string as is, a
// number as written, a bool as true/false, an object or array as compact
// JSON. A null, a missing path, an unreadable file or an empty argument
// yields collect.None.
func (p Probe) Field(file, dotPath string) string {
	v, ok := p.lookup(file, dotPath)
	if !ok || v == nil {
		return collect.None
	}
	return render(v)
}

// Count returns the length of the array or the number of keys of the object
// at dotPath; anything else yields collect.Zero.
func (p Probe) Count(file, dotPath string) string {
	v, ok := p.lookup(file, dotPath)
	if !ok {
		return collect.Zero
	}
	switch c := v.(type) {
	case []any:
		return strconv.Itoa(len(c))
	case map[string]any:
		return strconv.Itoa(len(c))
	}
	return collect.Zero
}

// CountWhere counts the elements of the array at dotPath that are objects
// holding key with a value that renders (as Field would) to value. A path
// that is not an array, or an empty argument, yields collect.Zero.
func (p Probe) CountWhere(file, dotPath, key, value string) string {
	if key == "" {
		return collect.Zero
	}
	v, ok := p.lookup(file, dotPath)
	if !ok {
		return collect.Zero
	}
	arr, ok := v.([]any)
	if !ok {
		return collect.Zero
	}
	n := 0
	for _, el := range arr {
		obj, ok := el.(map[string]any)
		if !ok {
			continue
		}
		got, ok := obj[key]
		if !ok {
			continue
		}
		if render(got) == value {
			n++
		}
	}
	return strconv.Itoa(n)
}

// CountWithKey counts the objects at any depth of file (inside arrays too)
// that hold key. Each object counts once, whatever its nested objects hold.
func (p Probe) CountWithKey(file, key string) string {
	if key == "" {
		return collect.Zero
	}
	root, ok := p.load(file)
	if !ok {
		return collect.Zero
	}
	return strconv.Itoa(countWithKey(root, key))
}

func countWithKey(v any, key string) int {
	n := 0
	switch c := v.(type) {
	case map[string]any:
		if _, ok := c[key]; ok {
			n++
		}
		for _, child := range c {
			n += countWithKey(child, key)
		}
	case []any:
		for _, child := range c {
			n += countWithKey(child, key)
		}
	}
	return n
}

// MCPConfigured reports whether keyword appears, as a raw substring, in the
// project's .mcp.json, the user's ~/.claude/.mcp.json or
// ~/.claude/settings.json, or any *.mcp.json under ~/.claude/plugins, in that
// order. It answers collect.Configured or collect.NotConfigured and never
// prints file contents.
func (p Probe) MCPConfigured(keyword string) string {
	if keyword == "" {
		return collect.NotConfigured
	}
	needle := []byte(keyword)
	candidates := []string{collect.Resolve(p.Root, ".mcp.json")}
	home := p.home()
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".claude", ".mcp.json"),
			filepath.Join(home, ".claude", "settings.json"),
		)
	}
	for _, path := range candidates {
		if fileContains(path, needle) {
			return collect.Configured
		}
	}
	if home == "" {
		return collect.NotConfigured
	}
	found := false
	_ = filepath.WalkDir(filepath.Join(home, ".claude", "plugins"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".mcp.json") {
			return nil
		}
		if fileContains(path, needle) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if found {
		return collect.Configured
	}
	return collect.NotConfigured
}

func (p Probe) home() string {
	if p.Home != "" {
		return p.Home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func fileContains(path string, needle []byte) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Contains(data, needle)
}

// load decodes file (resolved against Root) with numbers kept as written.
func (p Probe) load(file string) (any, bool) {
	if file == "" {
		return nil, false
	}
	data, err := os.ReadFile(collect.Resolve(p.Root, file))
	if err != nil {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

// lookup loads file and walks dotPath through objects only. The second result
// is false when the file is unusable, the path is empty or it leads nowhere.
func (p Probe) lookup(file, dotPath string) (any, bool) {
	dotPath = strings.TrimPrefix(dotPath, ".")
	if dotPath == "" {
		return nil, false
	}
	v, ok := p.load(file)
	if !ok {
		return nil, false
	}
	for _, seg := range strings.Split(dotPath, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok = obj[seg]
		if !ok {
			return nil, false
		}
	}
	return v, true
}

// render turns a decoded JSON value into its text form: a string as is, a
// number as written, a bool as true/false, null as "null", and an object or
// array as compact JSON with sorted keys.
func render(v any) string {
	switch c := v.(type) {
	case nil:
		return "null"
	case string:
		return c
	case json.Number:
		return c.String()
	case bool:
		return strconv.FormatBool(c)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return collect.None
	}
	return string(b)
}
