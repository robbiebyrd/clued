package jsonprobe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/collect"
)

const fixture = `{
  "name": "demo",
  "version": "1.0",
  "private": true,
  "scripts": {"test": "go test ./...", "build": "go build", "lint": "golangci-lint run"},
  "deps": [
    {"name": "a", "kind": "dev"},
    {"name": "b", "kind": "prod"},
    {"name": "c", "kind": "dev", "nested": {"kind": "x"}},
    {"name": "d"}
  ],
  "count": 42,
  "nothing": null,
  "meta": {"owner": {"email": "me@example.com"}},
  "flags": [
    {"on": true},
    {"on": false},
    {"on": "true"},
    "true",
    {"other": true}
  ]
}`

func writeFixture(t *testing.T) Probe {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Probe{Root: dir, Home: dir}
}

func TestField(t *testing.T) {
	p := writeFixture(t)
	cases := []struct {
		path, want string
	}{
		{"name", "demo"},
		{"count", "42"},
		{"version", "1.0"},
		{"private", "true"},
		{"nothing", collect.None},
		{"missing", collect.None},
		{"meta.owner.email", "me@example.com"},
		{".meta.owner.email", "me@example.com"},
		{"name.deeper", collect.None},
		{"scripts", `{"build":"go build","lint":"golangci-lint run","test":"go test ./..."}`},
		{"deps", `[{"kind":"dev","name":"a"},{"kind":"prod","name":"b"},{"kind":"dev","name":"c","nested":{"kind":"x"}},{"name":"d"}]`},
		{"", collect.None},
	}
	for _, c := range cases {
		if got := p.Field("package.json", c.path); got != c.want {
			t.Errorf("Field(%q) = %q, want %q", c.path, got, c.want)
		}
	}
	if got := p.Field("", "name"); got != collect.None {
		t.Errorf("Field with empty file = %q", got)
	}
}

func TestCount(t *testing.T) {
	p := writeFixture(t)
	cases := []struct {
		path, want string
	}{
		{"deps", "4"},
		{"scripts", "3"},
		{".scripts", "3"},
		{"count", collect.Zero},
		{"name", collect.Zero},
		{"nothing", collect.Zero},
		{"missing", collect.Zero},
		{"", collect.Zero},
	}
	for _, c := range cases {
		if got := p.Count("package.json", c.path); got != c.want {
			t.Errorf("Count(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestCountWhere(t *testing.T) {
	p := writeFixture(t)
	cases := []struct {
		path, key, value, want string
	}{
		{"deps", "kind", "dev", "2"},
		{".deps", "kind", "prod", "1"},
		{"deps", "kind", "nope", collect.Zero},
		{"deps", "absent", "dev", collect.Zero},
		{"scripts", "kind", "dev", collect.Zero},
		{"count", "kind", "dev", collect.Zero},
		{"missing", "kind", "dev", collect.Zero},
		{"flags", "on", "true", "2"}, // bool true and string "true" both render as true
		{"flags", "on", "false", "1"},
		{"deps", "nested", `{"kind":"x"}`, "1"},
		{"", "kind", "dev", collect.Zero},
		{"deps", "", "dev", collect.Zero},
	}
	for _, c := range cases {
		if got := p.CountWhere("package.json", c.path, c.key, c.value); got != c.want {
			t.Errorf("CountWhere(%q, %q, %q) = %q, want %q", c.path, c.key, c.value, got, c.want)
		}
	}
}

func TestCountWithKey(t *testing.T) {
	p := writeFixture(t)
	cases := []struct {
		key, want string
	}{
		{"kind", "4"},
		{"name", "5"}, // the root object holds name too
		{"email", "1"},
		{"owner", "1"},
		{"absent", collect.Zero},
		{"", collect.Zero},
	}
	for _, c := range cases {
		if got := p.CountWithKey("package.json", c.key); got != c.want {
			t.Errorf("CountWithKey(%q) = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestMissingAndInvalidFiles(t *testing.T) {
	p := writeFixture(t)
	for _, file := range []string{"absent.json", "bad.json"} {
		if got := p.Field(file, "name"); got != collect.None {
			t.Errorf("Field(%s) = %q", file, got)
		}
		if got := p.Count(file, "deps"); got != collect.Zero {
			t.Errorf("Count(%s) = %q", file, got)
		}
		if got := p.CountWhere(file, "deps", "kind", "dev"); got != collect.Zero {
			t.Errorf("CountWhere(%s) = %q", file, got)
		}
		if got := p.CountWithKey(file, "kind"); got != collect.Zero {
			t.Errorf("CountWithKey(%s) = %q", file, got)
		}
	}
}

func TestFieldResolvesAbsolutePath(t *testing.T) {
	p := writeFixture(t)
	abs := filepath.Join(p.Root, "package.json")
	if got := (Probe{}).Field(abs, "name"); got != "demo" {
		t.Errorf("Field(abs) = %q", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMCPConfigured(t *testing.T) {
	t.Run("project mcp.json", func(t *testing.T) {
		p := Probe{Root: t.TempDir(), Home: t.TempDir()}
		mustWrite(t, filepath.Join(p.Root, ".mcp.json"), `{"mcpServers":{"mind-palace":{}}}`)
		if got := p.MCPConfigured("mind-palace"); got != collect.Configured {
			t.Errorf("got %q", got)
		}
	})
	t.Run("home settings.json", func(t *testing.T) {
		p := Probe{Root: t.TempDir(), Home: t.TempDir()}
		mustWrite(t, filepath.Join(p.Home, ".claude", "settings.json"), `{"mcpServers":{"mind-palace":{}}}`)
		if got := p.MCPConfigured("mind-palace"); got != collect.Configured {
			t.Errorf("got %q", got)
		}
	})
	t.Run("home mcp.json", func(t *testing.T) {
		p := Probe{Root: t.TempDir(), Home: t.TempDir()}
		mustWrite(t, filepath.Join(p.Home, ".claude", ".mcp.json"), `{"mcpServers":{"mind-palace":{}}}`)
		if got := p.MCPConfigured("mind-palace"); got != collect.Configured {
			t.Errorf("got %q", got)
		}
	})
	t.Run("plugin file", func(t *testing.T) {
		p := Probe{Root: t.TempDir(), Home: t.TempDir()}
		mustWrite(t, filepath.Join(p.Home, ".claude", "plugins", "x", "y", "z.mcp.json"), `{"mcpServers":{"mind-palace":{}}}`)
		mustWrite(t, filepath.Join(p.Home, ".claude", "plugins", "x", "other.json"), `{"mcpServers":{"decoy":{}}}`)
		if got := p.MCPConfigured("mind-palace"); got != collect.Configured {
			t.Errorf("got %q", got)
		}
		if got := p.MCPConfigured("decoy"); got != collect.NotConfigured {
			t.Errorf("non-.mcp.json plugin file should be ignored, got %q", got)
		}
	})
	t.Run("absent", func(t *testing.T) {
		p := Probe{Root: t.TempDir(), Home: t.TempDir()}
		mustWrite(t, filepath.Join(p.Root, ".mcp.json"), `{"mcpServers":{"other":{}}}`)
		if got := p.MCPConfigured("mind-palace"); got != collect.NotConfigured {
			t.Errorf("got %q", got)
		}
	})
	t.Run("empty keyword", func(t *testing.T) {
		p := Probe{Root: t.TempDir(), Home: t.TempDir()}
		mustWrite(t, filepath.Join(p.Root, ".mcp.json"), `{}`)
		if got := p.MCPConfigured(""); got != collect.NotConfigured {
			t.Errorf("got %q", got)
		}
	})
	t.Run("nothing on disk", func(t *testing.T) {
		p := Probe{Root: t.TempDir(), Home: t.TempDir()}
		if got := p.MCPConfigured("mind-palace"); got != collect.NotConfigured {
			t.Errorf("got %q", got)
		}
	})
}
