// Package fsprobe answers questions about the file system under a root
// directory: whether a path exists, which files a glob matches, the first
// lines of a file, a line count, a recursive find and a content search.
//
// Every probe returns exactly one plain-text answer and never an error:
// a missing or unreadable path maps to a sentinel of the collect package.
package fsprobe

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/robbiebyrd/clued/mind-palace/collect"
)

// Probe answers file-system questions relative to Root.
type Probe struct {
	// Root is the directory relative paths resolve against; "" means the
	// process working directory.
	Root string
}

// FileExists answers collect.Yes when path is a regular file. A directory
// is collect.No.
func (p Probe) FileExists(path string) string {
	info, err := os.Stat(collect.Resolve(p.Root, path))
	if err == nil && info.Mode().IsRegular() {
		return collect.Yes
	}
	return collect.No
}

// DirExists answers collect.Yes when path is a directory.
func (p Probe) DirExists(path string) string {
	info, err := os.Stat(collect.Resolve(p.Root, path))
	if err == nil && info.IsDir() {
		return collect.Yes
	}
	return collect.No
}

// CountFiles answers the number of distinct files matched by all globs.
func (p Probe) CountFiles(globs []string) string {
	return strconv.Itoa(len(p.globAll(globs)))
}

// ListFiles answers the files matched by the globs, one per line, in glob
// order with duplicates removed; collect.None when nothing matched. A
// positive limit keeps only the first limit entries.
func (p Probe) ListFiles(globs []string, limit int) string {
	files := p.globAll(globs)
	if len(files) == 0 {
		return collect.None
	}
	if limit > 0 && len(files) > limit {
		files = files[:limit]
	}
	return strings.Join(files, "\n")
}

// globAll merges the matches of every glob in order, dropping duplicates.
func (p Probe) globAll(globs []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, g := range globs {
		for _, f := range Glob(p.Root, g) {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// FileHead answers the first lines lines of the file (10 when lines <= 0)
// joined by "\n" without a trailing newline. It reads only as far as it
// needs; a last line without a newline still counts, and "\r" is kept.
// An unreadable path answers collect.NoFile(path).
func (p Probe) FileHead(path string, lines int) string {
	if lines <= 0 {
		lines = 10
	}
	f, err := os.Open(collect.Resolve(p.Root, path))
	if err != nil {
		return collect.NoFile(path)
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.IsDir() {
		return collect.NoFile(path)
	}
	r := bufio.NewReader(f)
	var out []string
	for len(out) < lines {
		line, err := r.ReadString('\n')
		if line != "" {
			out = append(out, strings.TrimSuffix(line, "\n"))
		}
		if err != nil {
			if err != io.EOF && len(out) == 0 {
				return collect.NoFile(path)
			}
			break
		}
	}
	return strings.Join(out, "\n")
}

// CountLines answers the number of newline bytes in the file (wc -l
// semantics); an unreadable path answers collect.Zero.
func (p Probe) CountLines(path string) string {
	f, err := os.Open(collect.Resolve(p.Root, path))
	if err != nil {
		return collect.Zero
	}
	defer f.Close()
	n := 0
	buf := make([]byte, 64*1024)
	for {
		k, err := f.Read(buf)
		n += bytes.Count(buf[:k], []byte{'\n'})
		if err != nil {
			if err != io.EOF {
				return collect.Zero
			}
			break
		}
	}
	return strconv.Itoa(n)
}

// FindFiles walks dir (relative to Root; "" means Root itself) and answers
// the regular files whose name matches any of the names patterns (every
// file when names is empty), as root-relative forward-slashed paths,
// sorted and joined by "\n"; collect.None when nothing matched. Direct
// children are at depth 1; maxDepth <= 0 is unlimited. Directories whose
// name starts with "." are skipped; files starting with "." are reported
// when they match. With countOnly the answer is the decimal count; a
// positive limit keeps only the first limit entries after sorting.
func (p Probe) FindFiles(dir string, names []string, maxDepth, limit int, countOnly bool) string {
	if dir == "" {
		dir = "."
	}
	root := collect.Resolve(p.Root, ".")
	start := collect.Resolve(p.Root, dir)
	var found []string
	var walk func(d string, depth int)
	walk = func(d string, depth int) {
		if maxDepth > 0 && depth > maxDepth {
			return
		}
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range entries {
			full := filepath.Join(d, e.Name())
			if e.IsDir() {
				if !strings.HasPrefix(e.Name(), ".") {
					walk(full, depth+1)
				}
				continue
			}
			if !e.Type().IsRegular() || !matchAny(e.Name(), names) {
				continue
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				rel = full
			}
			found = append(found, collect.DisplayPath(rel))
		}
	}
	walk(start, 1)
	sort.Strings(found)
	if countOnly {
		return strconv.Itoa(len(found))
	}
	if len(found) == 0 {
		return collect.None
	}
	if limit > 0 && len(found) > limit {
		found = found[:limit]
	}
	return strings.Join(found, "\n")
}

// matchAny reports whether name matches one of the patterns; no patterns
// matches everything.
func matchAny(name string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pat := range patterns {
		if MatchName(name, pat) {
			return true
		}
	}
	return false
}

// CountFilesWithContent answers the number of files matched by glob whose
// content matches the Go regexp pattern. An empty glob or pattern, an
// invalid regexp and no match all answer collect.Zero; unreadable files
// are skipped.
func (p Probe) CountFilesWithContent(glob, pattern string) string {
	if glob == "" || pattern == "" {
		return collect.Zero
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return collect.Zero
	}
	n := 0
	for _, f := range Glob(p.Root, glob) {
		data, err := os.ReadFile(collect.Resolve(p.Root, f))
		if err == nil && re.Match(data) {
			n++
		}
	}
	return strconv.Itoa(n)
}

// Pwd answers the absolute, cleaned Root, or the working directory when
// Root is "".
func (p Probe) Pwd() string {
	if p.Root == "" {
		if wd, err := os.Getwd(); err == nil {
			return wd
		}
		return "."
	}
	abs, err := filepath.Abs(p.Root)
	if err != nil {
		return filepath.Clean(p.Root)
	}
	return abs
}

// Glob returns the regular files under root that match pattern, sorted,
// with forward slashes. A pattern segment may use "*" (any run of
// characters within one name), "?" (one character) and "**" (zero or more
// directories). Names starting with "." match a wildcard only when the
// pattern segment itself starts with "."; "**" never descends into a
// hidden directory. Literal segments are walked directly without listing
// their parent. A relative pattern yields paths relative to root; an
// absolute or "~" pattern yields absolute paths. Symbolic links are not
// followed and unreadable directories are skipped.
func Glob(root, pattern string) []string {
	pattern = collect.ExpandHome(pattern)
	if pattern == "" {
		return nil
	}
	var base, prefix string
	if filepath.IsAbs(pattern) {
		vol := filepath.VolumeName(pattern)
		base = vol + string(filepath.Separator)
		prefix = collect.DisplayPath(base)
		pattern = pattern[len(vol):]
	} else {
		base = collect.Resolve(root, ".")
	}
	var segs []string
	for _, s := range strings.Split(filepath.ToSlash(pattern), "/") {
		if s != "" && s != "." {
			segs = append(segs, s)
		}
	}
	if len(segs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	globWalk(base, prefix, segs, func(display string) {
		if !seen[display] {
			seen[display] = true
			out = append(out, display)
		}
	})
	sort.Strings(out)
	return out
}

// globWalk matches segs under dir, reporting the display path of every
// regular file that matches all of them.
func globWalk(dir, prefix string, segs []string, emit func(string)) {
	if len(segs) == 0 {
		if info, err := os.Lstat(dir); err == nil && info.Mode().IsRegular() {
			emit(prefix)
		}
		return
	}
	seg, rest := segs[0], segs[1:]
	switch {
	case seg == "**":
		globWalk(dir, prefix, rest, emit)
		for _, e := range readDir(dir) {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				globWalk(filepath.Join(dir, e.Name()), joinDisplay(prefix, e.Name()), segs, emit)
			}
		}
	case !hasWildcard(seg):
		globWalk(filepath.Join(dir, seg), joinDisplay(prefix, seg), rest, emit)
	default:
		hiddenOK := strings.HasPrefix(seg, ".")
		for _, e := range readDir(dir) {
			name := e.Name()
			if !hiddenOK && strings.HasPrefix(name, ".") {
				continue
			}
			if !MatchName(name, seg) {
				continue
			}
			if len(rest) == 0 {
				if e.Type().IsRegular() {
					emit(joinDisplay(prefix, name))
				}
				continue
			}
			if e.IsDir() {
				globWalk(filepath.Join(dir, name), joinDisplay(prefix, name), rest, emit)
			}
		}
	}
}

// readDir lists dir, treating an unreadable directory as empty.
func readDir(dir string) []os.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	return entries
}

// joinDisplay appends one name to a forward-slashed display path.
func joinDisplay(prefix, name string) string {
	if prefix == "" || strings.HasSuffix(prefix, "/") {
		return prefix + name
	}
	return prefix + "/" + name
}

// hasWildcard reports whether a pattern segment holds a glob metacharacter.
func hasWildcard(seg string) bool {
	return strings.ContainsAny(seg, "*?")
}

var (
	patternMu    sync.Mutex
	patternCache = map[string]*regexp.Regexp{}
)

// MatchName reports whether the whole name matches one pattern segment:
// "*" matches any run of characters except "/", "?" one such character
// and "**" anything at all; every other character is literal.
func MatchName(name, pattern string) bool {
	return compilePattern(pattern).MatchString(name)
}

// compilePattern translates a glob segment into an anchored regexp,
// caching the result.
func compilePattern(pattern string) *regexp.Regexp {
	patternMu.Lock()
	defer patternMu.Unlock()
	if re, ok := patternCache[pattern]; ok {
		return re
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; {
		case c == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re := regexp.MustCompile(b.String())
	patternCache[pattern] = re
	return re
}
