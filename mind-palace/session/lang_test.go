package session

import "testing"

func TestExtToLang(t *testing.T) {
	cases := []struct{ path, want string }{
		{"", ""},
		{"Makefile", ""},
		{"src/foo.ts", "typescript"},
		{"components/Button.tsx", "typescript"},
		{"script.py", "python"},
		{"package.json", "json"},
		{"config.yaml", "yaml"},
		{".github/ci.yml", "yaml"},
		{"archive.zip", ""},
	}
	for _, c := range cases {
		if got := ExtToLang(c.path); got != c.want {
			t.Errorf("ExtToLang(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
