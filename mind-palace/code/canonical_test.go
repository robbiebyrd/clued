package code

import (
	"strings"
	"testing"
)

func TestCanonical(t *testing.T) {
	sha := strings.Repeat("0123456789abcdef", 4)
	cases := []struct {
		name string
		fn   func(string) (string, bool)
		in   string
		ok   bool
	}{
		{"sha256", CanonicalSHA256, sha, true},
		{"sha256 upper", CanonicalSHA256, strings.ToUpper(sha), false},
		{"sha256 short", CanonicalSHA256, sha[:63], false},
		{"sha256 operator", CanonicalSHA256, "$ne" + sha[3:], false},
		{"git oid sha-1", CanonicalGitOID, sha[:40], true},
		{"git oid sha-256", CanonicalGitOID, sha, true},
		{"git oid empty", CanonicalGitOID, "", false},
		{"git oid 41", CanonicalGitOID, sha[:41], false},
		{"worktree wt", CanonicalWorktreeID, "wt_0123456789abcdef", true},
		{"worktree gh", CanonicalWorktreeID, "gh_1234567890", true},
		{"worktree upper", CanonicalWorktreeID, "wt_0123456789ABCDEF", false},
		{"worktree other prefix", CanonicalWorktreeID, "xx_0123456789abcdef", false},
		{"worktree operator", CanonicalWorktreeID, "$where", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := c.fn(c.in)
			if ok != c.ok {
				t.Fatalf("ok %v, want %v", ok, c.ok)
			}
			if ok && got != c.in {
				t.Fatalf("got %q, want %q", got, c.in)
			}
			if !ok && got != "" {
				t.Fatalf("got %q on failure", got)
			}
		})
	}
}

func TestRebuildRefusesOtherBytes(t *testing.T) {
	if _, ok := rebuild("abc$", hexAlphabet); ok {
		t.Fatal("rebuild accepted a byte outside the alphabet")
	}
	if got, ok := rebuild("", hexAlphabet); !ok || got != "" {
		t.Fatalf("rebuild of empty: %q %v", got, ok)
	}
}
