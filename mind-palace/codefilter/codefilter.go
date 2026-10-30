// Package codefilter decides, file by file, what of a repository may leave
// for the code graph: the content, only its metadata (path and size), or
// nothing at all. The client collector (session/codesync, plan 0010) and
// the server-side GitHub sync (ghsync, plan 0011) both call Decide, so a
// file is treated the same whichever way it reaches the server. The
// package depends on the standard library only; the machine-local
// excludePaths matcher (redact.Paths) is passed in as a Matcher by the
// client and left out on the server.
package codefilter

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Decision is what may be sent for a file.
type Decision int

const (
	// Skip: the file is not collected at all, not even its path.
	Skip Decision = iota
	// MetadataOnly: the file is recorded with its path and size but
	// without content (sha256 "").
	MetadataOnly
	// Content: the file is collected with its content.
	Content
)

func (d Decision) String() string {
	switch d {
	case Skip:
		return "skip"
	case MetadataOnly:
		return "metadata-only"
	case Content:
		return "content"
	}
	return fmt.Sprintf("Decision(%d)", int(d))
}

// DefaultMaxFileBytes is the content cap per file when Options leaves it
// unset (codeSyncMaxFileBytes' default, and the server's capabilities
// limit).
const DefaultMaxFileBytes int64 = 1 << 20

// SniffBytes is how much of a file's start is searched for a NUL byte.
const SniffBytes = 8 << 10

// Reasons Decide gives besides the pattern-carrying ones ("deny: …",
// "excludePaths: …", "codeSyncExclude: …").
const (
	ReasonInvalidPath = "invalid path"
	ReasonTooLarge    = "too large"
	ReasonBinary      = "binary"
	ReasonNotUTF8     = "invalid utf-8"
)

// Matcher matches an absolute path, or a directory above it, against
// patterns and names the one that matched. redact.Paths (the excludePaths
// config) implements it.
type Matcher interface {
	Match(path string) (pattern string, ok bool)
}

// Options are the per-repository settings Decide applies on top of the
// built-in rules.
type Options struct {
	// Root is the worktree's absolute root, needed only to apply
	// ExcludePaths; the GitHub sync leaves it empty.
	Root string
	// MaxFileBytes caps the content per file; zero or less means
	// DefaultMaxFileBytes.
	MaxFileBytes int64
	// ExcludePaths matches absolute paths (excludePaths); applied to
	// Root joined with the file's path when both are set.
	ExcludePaths Matcher
	// Exclude holds the codeSyncExclude globs, relative to the root.
	Exclude Excludes
}

func (o Options) maxFileBytes() int64 {
	if o.MaxFileBytes <= 0 {
		return DefaultMaxFileBytes
	}
	return o.MaxFileBytes
}

// Decide says what may be sent for the file at rel, a slash-separated path
// relative to the repository root (what git ls-files and the GitHub API
// report), of size bytes. head is the file's content, or at least its
// first SniffBytes; a caller that sends the content passes all of it, so
// the UTF-8 check covers the whole file (a rune cut off at the end of a
// shorter head is not held against it). Rules, in order:
//
//   - an invalid path (empty, absolute, or leaving the root): Skip;
//   - codeSyncExclude, then excludePaths: Skip;
//   - the built-in deny list (Denied), which cannot be switched off: Skip;
//   - over the size cap: MetadataOnly, without looking at head;
//   - a name with a binary format's extension (binaryExts): MetadataOnly,
//     without looking at head, so a caller that has to pay for the content
//     (the GitHub sync) can ask first with a nil head;
//   - a NUL byte in the first SniffBytes, or invalid UTF-8: MetadataOnly;
//   - otherwise Content.
//
// The reason is empty for Content.
func Decide(rel string, size int64, head []byte, opts Options) (Decision, string) {
	clean, ok := cleanRel(rel)
	if !ok {
		return Skip, ReasonInvalidPath
	}
	if pat, ok := opts.Exclude.Match(clean); ok {
		return Skip, "codeSyncExclude: " + pat
	}
	if opts.ExcludePaths != nil && opts.Root != "" {
		if pat, ok := opts.ExcludePaths.Match(filepath.Join(opts.Root, filepath.FromSlash(clean))); ok {
			return Skip, "excludePaths: " + pat
		}
	}
	if pat, ok := Denied(clean); ok {
		return Skip, "deny: " + pat
	}
	if size > opts.maxFileBytes() {
		return MetadataOnly, ReasonTooLarge
	}
	if binaryExts[strings.ToLower(path.Ext(clean))] {
		return MetadataOnly, ReasonBinary
	}
	if reason := sniff(head, int64(len(head)) < size); reason != "" {
		return MetadataOnly, reason
	}
	return Content, ""
}

// binaryExts are the extensions of formats that are not text; matched
// case-insensitively. Text formats such as .svg stay out: the sniff decides
// those.
var binaryExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true, ".bmp": true, ".ico": true, ".tif": true, ".tiff": true,
	".mp3": true, ".wav": true, ".aif": true, ".aiff": true, ".ogg": true, ".flac": true, ".m4a": true,
	".mp4": true, ".mov": true, ".webm": true, ".avi": true, ".mkv": true,
	".zip": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".7z": true, ".rar": true,
	".pdf": true, ".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".wasm": true, ".exe": true, ".dll": true, ".so": true, ".dylib": true, ".class": true, ".jar": true, ".pyc": true,
}

// IsText reports whether content looks like text: no NUL byte in its
// first SniffBytes and valid UTF-8. truncated says content is only the
// file's start, so a rune cut off at its end is allowed.
func IsText(content []byte, truncated bool) bool {
	return sniff(content, truncated) == ""
}

func sniff(head []byte, truncated bool) string {
	if bytes.IndexByte(head[:min(len(head), SniffBytes)], 0) >= 0 {
		return ReasonBinary
	}
	if truncated {
		head = trimPartialRune(head)
	}
	if !utf8.Valid(head) {
		return ReasonNotUTF8
	}
	return ""
}

// trimPartialRune drops an incomplete UTF-8 sequence at the end of b.
func trimPartialRune(b []byte) []byte {
	for i := 1; i < utf8.UTFMax && i <= len(b); i++ {
		c := b[len(b)-i]
		if !utf8.RuneStart(c) {
			continue
		}
		if !utf8.FullRune(b[len(b)-i:]) {
			return b[:len(b)-i]
		}
		break
	}
	return b
}

// cleanRel validates a root-relative slash path and returns it cleaned.
func cleanRel(rel string) (string, bool) {
	if rel == "" || strings.ContainsRune(rel, 0) || strings.Contains(rel, `\`) {
		return "", false
	}
	if path.IsAbs(rel) || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", false
	}
	clean := path.Clean(rel)
	if clean == "." {
		return "", false
	}
	return clean, true
}

// denyNames are the built-in deny list's file-name globs (plan 0010),
// matched case-insensitively against a file's base name.
var denyNames = []string{
	".env",
	".env.*",
	"*.pem",
	"*.key",
	"*.p12",
	"*.pfx",
	"id_rsa*",
	"id_ed25519*",
	"*.kdbx",
	".npmrc",
	".pypirc",
	".netrc",
	"credentials*.json",
}

// denyUnderConfig is the deny list's one directory rule: a name containing
// "secret" anywhere under a config/ directory.
const denyUnderConfig = "config/**/*secret*"

// Denied reports whether rel (a root-relative slash path) is on the
// built-in deny list of secret-bearing files, and the entry that matched.
// The list is not configurable: .env, .env.*, *.pem, *.key, *.p12, *.pfx,
// id_rsa*, id_ed25519*, *.kdbx, .npmrc, .pypirc, .netrc, credentials*.json
// (by base name, in any directory, ignoring case), and *secret* under
// config/ (any file or directory below a directory named config whose
// name contains "secret").
func Denied(rel string) (pattern string, ok bool) {
	lower := strings.ToLower(path.Clean(rel))
	base := path.Base(lower)
	for _, pat := range denyNames {
		if ok, _ := path.Match(pat, base); ok {
			return pat, true
		}
	}
	parts := strings.Split(lower, "/")
	for i, part := range parts[:len(parts)-1] {
		if part != "config" {
			continue
		}
		for _, below := range parts[i+1:] {
			if strings.Contains(below, "secret") {
				return denyUnderConfig, true
			}
		}
	}
	return "", false
}

// Excludes are codeSyncExclude globs (path.Match syntax), relative to the
// worktree root. A glob without a slash matches a file or directory name
// at any depth ("*.gen.go", "testdata"); a glob with a slash is anchored
// at the root ("web/dist", "/build/*.js", a leading slash ignored). Either
// way a match on a directory excludes everything below it.
type Excludes []string

// NewExcludes checks the syntax of codeSyncExclude globs; a bad one is
// skipped and reported in errs.
func NewExcludes(patterns []string) (e Excludes, errs []error) {
	for i, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat != "/" {
			pat = strings.TrimSuffix(pat, "/")
		}
		pat = strings.TrimPrefix(pat, "/")
		if pat == "" {
			continue
		}
		if _, err := path.Match(pat, ""); err != nil {
			errs = append(errs, fmt.Errorf("codeSyncExclude[%d] %q: %w", i, patterns[i], err))
			continue
		}
		if strings.Contains(pat, "/") {
			pat = path.Clean(pat)
		}
		e = append(e, pat)
	}
	return e, errs
}

// Match reports the first glob that matches rel (a cleaned root-relative
// slash path) or a directory above it.
func (e Excludes) Match(rel string) (pattern string, ok bool) {
	if len(e) == 0 || rel == "" {
		return "", false
	}
	for dir := rel; dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		base := path.Base(dir)
		for _, pat := range e {
			subject := base
			if strings.Contains(pat, "/") {
				subject = dir
			}
			if ok, _ := path.Match(pat, subject); ok {
				return pat, true
			}
		}
	}
	return "", false
}
