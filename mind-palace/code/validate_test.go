package code

import (
	"strings"
	"testing"
)

func TestValidPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"file", "main.go", true},
		{"nested", "cmd/mind-palace/main.go", true},
		{"dotfile", ".gitignore", true},
		{"dot directory", ".github/workflows/ci.yml", true},
		{"dots in name", "a/..b/c..", true},
		{"unicode", "docs/übersicht.md", true},
		{"space", "a dir/a file.txt", true},
		{"max length", strings.Repeat("a", MaxPathBytes), true},
		{"empty", "", false},
		{"too long", strings.Repeat("a", MaxPathBytes+1), false},
		{"dot", ".", false},
		{"dotdot", "..", false},
		{"escapes", "../etc/passwd", false},
		{"escapes inside", "a/../../b", false},
		{"not clean dotdot", "a/../b", false},
		{"not clean dot", "./a", false},
		{"not clean inner dot", "a/./b", false},
		{"doubled slash", "a//b", false},
		{"trailing slash", "a/", false},
		{"absolute", "/etc/passwd", false},
		{"backslash", `a\b`, false},
		{"windows absolute", `C:\x`, false},
		{"nul", "a\x00b", false},
		{"invalid utf-8", "a\xffb", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidPath(c.in); got != c.want {
				t.Errorf("ValidPath(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestValidSHA256(t *testing.T) {
	sha := strings.Repeat("0123456789abcdef", 4)
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"sha-256", sha, true},
		{"empty-content hash", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", true},
		{"empty", "", false},
		{"short", sha[:63], false},
		{"long", sha + "0", false},
		{"sha-1 length", sha[:40], false},
		{"uppercase", strings.ToUpper(sha), false},
		{"non-hex", sha[:63] + "g", false},
		{"prefixed", "sha256:" + sha[:57], false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidSHA256(c.in); got != c.want {
				t.Errorf("ValidSHA256(%q) = %v, want %v", c.in, got, c.want)
			}
			if got := ValidSHA(c.in); got != c.want {
				t.Errorf("ValidSHA(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestValidGitOID(t *testing.T) {
	hex := strings.Repeat("0123456789abcdef", 4)
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"sha-1", hex[:40], true},
		{"sha-256", hex, true},
		{"empty", "", false},
		{"short sha", hex[:7], false},
		{"39", hex[:39], false},
		{"41", hex[:41], false},
		{"63", hex[:63], false},
		{"65", hex + "0", false},
		{"uppercase", strings.ToUpper(hex[:40]), false},
		{"non-hex", hex[:39] + "z", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidGitOID(c.in); got != c.want {
				t.Errorf("ValidGitOID(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestValidWorktreeID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"client", "wt_0123456789abcdef", true},
		{"github", "gh_123456789", true},
		{"github one digit", "gh_1", true},
		{"github max", "gh_9223372036854775807", true},
		{"empty", "", false},
		{"no prefix", "0123456789abcdef", false},
		{"other prefix", "xx_0123456789abcdef", false},
		{"client short", "wt_0123456789abcde", false},
		{"client long", "wt_0123456789abcdef0", false},
		{"client uppercase", "wt_0123456789ABCDEF", false},
		{"client non-hex", "wt_0123456789abcdeg", false},
		{"client bare", "wt_", false},
		{"github bare", "gh_", false},
		{"github leading zero", "gh_0123", false},
		{"github zero", "gh_0", false},
		{"github hex", "gh_12ab", false},
		{"github negative", "gh_-12", false},
		{"github too long", "gh_12345678901234567890", false},
		{"uppercase prefix", "WT_0123456789abcdef", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidWorktreeID(c.in); got != c.want {
				t.Errorf("ValidWorktreeID(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestValidRef(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"main", "main", true},
		{"nested", "story/0053-mar", true},
		{"dots inside", "release-1.2.3", true},
		{"at sign", "user@feature", true},
		{"unicode", "fix/ümlaut", true},
		{"detached short", "detached@0123abc", true},
		{"detached sha-1", "detached@" + strings.Repeat("a", 40), true},
		{"detached sha-256", "detached@" + strings.Repeat("a", 64), true},
		{"max length", strings.Repeat("a", MaxRefBytes), true},
		{"empty", "", false},
		{"too long", strings.Repeat("a", MaxRefBytes+1), false},
		{"at alone", "@", false},
		{"leading dash", "-x", false},
		{"leading slash", "/main", false},
		{"trailing slash", "main/", false},
		{"trailing dot", "main.", false},
		{"lock suffix", "main.lock", false},
		{"lock component", "a.lock/b", false},
		{"dot component", "a/.b", false},
		{"leading dot", ".hidden", false},
		{"dotdot", "a..b", false},
		{"doubled slash", "a//b", false},
		{"reflog syntax", "main@{1}", false},
		{"space", "my branch", false},
		{"tab", "a\tb", false},
		{"nul", "a\x00b", false},
		{"del", "a\x7fb", false},
		{"tilde", "a~1", false},
		{"caret", "a^", false},
		{"colon", "a:b", false},
		{"question", "a?", false},
		{"star", "a*", false},
		{"bracket", "a[b", false},
		{"backslash", `a\b`, false},
		{"invalid utf-8", "a\xff", false},
		{"detached too short", "detached@012345", false},
		{"detached too long", "detached@" + strings.Repeat("a", 65), false},
		{"detached uppercase", "detached@ABCDEF0", false},
		{"detached empty", "detached@", false},
		{"detached non-hex", "detached@feature", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidRef(c.in); got != c.want {
				t.Errorf("ValidRef(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
