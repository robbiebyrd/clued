package envprobe

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/collect"
)

// touch creates empty marker files under root.
func touch(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(root, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scriptedRunner records every call and answers with canned stdout per
// command name; a command without an entry fails.
type scriptedRunner struct {
	out   map[string]string
	calls []string
}

func (r *scriptedRunner) Run(_ context.Context, dir, name string, args ...string) (string, error) {
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " "))
	if s, ok := r.out[name]; ok {
		return s, nil
	}
	return "", errors.New("not scripted: " + name)
}

type failingRunner struct{}

func (failingRunner) Run(context.Context, string, string, ...string) (string, error) {
	return "", errors.New("boom")
}

func TestDetectStack(t *testing.T) {
	t.Run("go and ts", func(t *testing.T) {
		root := t.TempDir()
		touch(t, root, "go.mod", "package.json")
		if got := (Probe{Root: root}).DetectStack(); got != "Go TS" {
			t.Fatalf("got %q, want %q", got, "Go TS")
		}
	})
	t.Run("rust java csharp python", func(t *testing.T) {
		root := t.TempDir()
		touch(t, root, "Cargo.toml", "pom.xml", "App.sln", "pyproject.toml")
		if got := (Probe{Root: root}).DetectStack(); got != "Rust Java C# Python" {
			t.Fatalf("got %q, want %q", got, "Rust Java C# Python")
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := (Probe{Root: t.TempDir()}).DetectStack(); got != collect.Unknown {
			t.Fatalf("got %q, want %q", got, collect.Unknown)
		}
	})
}

func TestDetectCoverage(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "coverage.out", "cobertura.xml", "lcov.info")
	if err := os.Mkdir(filepath.Join(root, "coverage"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := "Go:coverage.out JS:coverage/ cobertura.xml lcov.info"
	if got := (Probe{Root: root}).DetectCoverage(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := (Probe{Root: t.TempDir()}).DetectCoverage(); got != "none found" {
		t.Fatalf("got %q, want %q", got, "none found")
	}
}

func TestDetectFrontend(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "tsconfig.json")
	if got := (Probe{Root: root}).DetectFrontend(); got != collect.Yes {
		t.Fatalf("got %q, want %q", got, collect.Yes)
	}
	if got := (Probe{Root: t.TempDir()}).DetectFrontend(); got != collect.No {
		t.Fatalf("got %q, want %q", got, collect.No)
	}
}

func TestDetectCSSFramework(t *testing.T) {
	t.Run("order", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "package.json", `{"devDependencies":{"sass":"1","tailwindcss":"3"}}`)
		if got := (Probe{Root: root}).DetectCSSFramework(); got != "tailwindcss" {
			t.Fatalf("got %q, want %q", got, "tailwindcss")
		}
	})
	t.Run("less", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "package.json", `{"dependencies":{"less":"4"}}`)
		if got := (Probe{Root: root}).DetectCSSFramework(); got != "less" {
			t.Fatalf("got %q, want %q", got, "less")
		}
	})
	t.Run("no package.json", func(t *testing.T) {
		if got := (Probe{Root: t.TempDir()}).DetectCSSFramework(); got != collect.Unknown {
			t.Fatalf("got %q, want %q", got, collect.Unknown)
		}
	})
}

func TestDetectBuildTool(t *testing.T) {
	ctx := context.Background()
	t.Run("rust", func(t *testing.T) {
		root := t.TempDir()
		touch(t, root, "Cargo.toml", "package.json", "go.mod")
		r := &scriptedRunner{out: map[string]string{"cargo": "cargo 1.80.0", "rustc": "rustc 1.80.0"}}
		got := (Probe{Root: root, Runner: r}).DetectBuildTool(ctx)
		want := "=== Rust Project ===\ncargo 1.80.0\nrustc 1.80.0"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if want := []string{"cargo --version", "rustc --version"}; strings.Join(r.calls, ",") != strings.Join(want, ",") {
			t.Fatalf("calls %v, want %v", r.calls, want)
		}
	})
	t.Run("node", func(t *testing.T) {
		root := t.TempDir()
		touch(t, root, "package.json", "go.mod")
		r := &scriptedRunner{out: map[string]string{"node": "v22.0.0", "npm": "10.0.0"}}
		got := (Probe{Root: root, Runner: r}).DetectBuildTool(ctx)
		want := "=== Node Project ===\nv22.0.0\n10.0.0"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if want := []string{"node --version", "npm --version"}; strings.Join(r.calls, ",") != strings.Join(want, ",") {
			t.Fatalf("calls %v, want %v", r.calls, want)
		}
	})
	t.Run("go", func(t *testing.T) {
		root := t.TempDir()
		touch(t, root, "go.mod")
		r := &scriptedRunner{out: map[string]string{"go": "go version go1.26.0 linux/amd64"}}
		got := (Probe{Root: root, Runner: r}).DetectBuildTool(ctx)
		want := "=== Go Project ===\ngo version go1.26.0 linux/amd64"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if want := []string{"go version"}; strings.Join(r.calls, ",") != strings.Join(want, ",") {
			t.Fatalf("calls %v, want %v", r.calls, want)
		}
	})
	t.Run("failing runner keeps label", func(t *testing.T) {
		root := t.TempDir()
		touch(t, root, "go.mod")
		got := (Probe{Root: root, Runner: failingRunner{}}).DetectBuildTool(ctx)
		if got != "=== Go Project ===" {
			t.Fatalf("got %q, want %q", got, "=== Go Project ===")
		}
	})
	t.Run("maven", func(t *testing.T) {
		root := t.TempDir()
		touch(t, root, "pom.xml", "build.gradle")
		got := (Probe{Root: root, Runner: failingRunner{}}).DetectBuildTool(ctx)
		if got != "=== Java/Maven Project ===" {
			t.Fatalf("got %q, want %q", got, "=== Java/Maven Project ===")
		}
	})
	t.Run("gradle", func(t *testing.T) {
		root := t.TempDir()
		touch(t, root, "build.gradle")
		got := (Probe{Root: root, Runner: failingRunner{}}).DetectBuildTool(ctx)
		if got != "=== Java/Gradle Project ===" {
			t.Fatalf("got %q, want %q", got, "=== Java/Gradle Project ===")
		}
	})
	t.Run("none", func(t *testing.T) {
		got := (Probe{Root: t.TempDir(), Runner: failingRunner{}}).DetectBuildTool(ctx)
		if got != collect.NoBuildTool {
			t.Fatalf("got %q, want %q", got, collect.NoBuildTool)
		}
	})
}

func TestDetectPort(t *testing.T) {
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := closed.Addr().(*net.TCPAddr).Port
	closed.Close()

	open, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	openPort := open.Addr().(*net.TCPAddr).Port

	got := Probe{}.DetectPort([]int{closedPort, openPort})
	if got != strconv.Itoa(openPort) {
		t.Fatalf("got %q, want %q", got, strconv.Itoa(openPort))
	}
	if got := (Probe{}).DetectPort([]int{closedPort}); got != collect.NotDetected {
		t.Fatalf("got %q, want %q", got, collect.NotDetected)
	}
	if got := (Probe{}).DetectPort(nil); got != collect.NotDetected {
		t.Fatalf("got %q, want %q", got, collect.NotDetected)
	}
}

func TestCommandExists(t *testing.T) {
	if got := (Probe{}).CommandExists("go"); got == "" {
		t.Fatal("expected go to be found on PATH")
	}
	if got := (Probe{}).CommandExists("definitely-not-a-command-xyz"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if got := (Probe{}).CommandExists(""); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
