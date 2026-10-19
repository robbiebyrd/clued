package code

import (
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// MaxPathBytes is the longest Entry.Path, in bytes.
const MaxPathBytes = 1024

// MaxRefBytes is the longest ref, in bytes.
const MaxRefBytes = 1024

// ValidPath reports whether p can name a file in a tree: non-empty, at most
// MaxPathBytes, valid UTF-8 with no NUL, slash-separated (no backslash),
// already clean (path.Clean(p) == p, so no "." or ".." elements, no doubled
// or trailing slash) and local (filepath.IsLocal on the cleaned path: not
// absolute, not escaping the root). "." itself names no file and is refused.
// Paths that reach the server are checked with it at the point of use.
func ValidPath(p string) bool {
	if p == "" || len(p) > MaxPathBytes || !utf8.ValidString(p) {
		return false
	}
	if strings.ContainsAny(p, "\x00\\") {
		return false
	}
	clean := path.Clean(p)
	if clean != p || clean == "." {
		return false
	}
	return filepath.IsLocal(filepath.FromSlash(clean))
}

// ValidSHA256 reports whether s is a SHA-256 in lowercase hex (64
// characters), the form of Blob.SHA256 and a non-empty Entry.SHA256.
func ValidSHA256(s string) bool {
	return len(s) == 64 && lowerHex(s)
}

// ValidSHA is ValidSHA256, under the name plan 0010 gives it.
func ValidSHA(s string) bool { return ValidSHA256(s) }

// ValidGitOID reports whether s is a git object id in lowercase hex: 40
// characters (SHA-1 repositories) or 64 (SHA-256 repositories). It is the
// form of Entry.GitOID, Change.Head and Change.Upstream; callers that allow
// those to be empty check for "" first.
func ValidGitOID(s string) bool {
	return (len(s) == 40 || len(s) == 64) && lowerHex(s)
}

// ValidWorktreeID reports whether id is a worktree id: "wt_" plus 16
// lowercase hex characters (a client's checkout) or "gh_" plus a GitHub
// repository id (decimal digits, no leading zero, at most 19).
func ValidWorktreeID(id string) bool {
	if rest, ok := strings.CutPrefix(id, "wt_"); ok {
		return len(rest) == 16 && lowerHex(rest)
	}
	if rest, ok := strings.CutPrefix(id, "gh_"); ok {
		if rest == "" || len(rest) > 19 || rest[0] == '0' {
			return false
		}
		for i := 0; i < len(rest); i++ {
			if rest[i] < '0' || rest[i] > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// ValidRef reports whether ref can name a tree: a branch name that git
// accepts (the rules of git check-ref-format --branch) or
// "detached@<sha>" with 7 to 64 lowercase hex characters, at most
// MaxRefBytes. A branch name is refused when it is empty or "@", starts with
// "-" or "/", ends with "/", "." or ".lock", contains "..", "//", "@{", a
// space, a control character or one of ~ ^ : ? * [ \, or has a
// slash-separated component that starts with "." or ends with ".lock".
func ValidRef(ref string) bool {
	if ref == "" || len(ref) > MaxRefBytes || !utf8.ValidString(ref) {
		return false
	}
	if sha, ok := strings.CutPrefix(ref, "detached@"); ok {
		return len(sha) >= 7 && len(sha) <= 64 && lowerHex(sha)
	}
	if ref == "@" || strings.HasPrefix(ref, "-") || strings.HasPrefix(ref, "/") ||
		strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") {
		return false
	}
	if strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.Contains(ref, "@{") {
		return false
	}
	for _, r := range ref {
		if r < 0x20 || r == 0x7f || r == ' ' || strings.ContainsRune("~^:?*[\\", r) {
			return false
		}
	}
	for _, c := range strings.Split(ref, "/") {
		if strings.HasPrefix(c, ".") || strings.HasSuffix(c, ".lock") {
			return false
		}
	}
	return true
}

// lowerHex reports whether s is non-empty and only 0-9 and a-f.
func lowerHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
