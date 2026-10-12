// Package envprobe answers questions about the development environment of a
// project: which languages it uses, what coverage artifacts and frontend
// markers it carries, which build tool serves it, which local port answers,
// and whether a command is on PATH.
//
// Every probe returns exactly one plain-text answer and never an error; a
// failure maps to one of the collect sentinels.
package envprobe

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/collect"
)

// Probe inspects one project directory.
type Probe struct {
	Root   string         // project directory; "" means the working directory
	Runner collect.Runner // nil means collect.ExecRunner{Timeout: 5 * time.Second}
}

// runner returns the configured Runner or the default one.
func (p Probe) runner() collect.Runner {
	if p.Runner != nil {
		return p.Runner
	}
	return collect.ExecRunner{Timeout: 5 * time.Second}
}

// exists reports whether name (a file or directory) is present in Root.
func (p Probe) exists(name string) bool {
	_, err := os.Stat(collect.Resolve(p.Root, name))
	return err == nil
}

// anyExists reports whether at least one of names is present in Root.
func (p Probe) anyExists(names ...string) bool {
	for _, n := range names {
		if p.exists(n) {
			return true
		}
	}
	return false
}

// hasSuffix reports whether an entry directly in Root ends with one of exts.
func (p Probe) hasSuffix(exts ...string) bool {
	entries, err := os.ReadDir(collect.Resolve(p.Root, "."))
	if err != nil {
		return false
	}
	for _, e := range entries {
		for _, ext := range exts {
			if strings.HasSuffix(e.Name(), ext) {
				return true
			}
		}
	}
	return false
}

// CommandExists returns the resolved path of name on PATH, or "" when it is
// absent or name is empty.
func (p Probe) CommandExists(name string) string {
	if name == "" {
		return ""
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

// DetectStack lists the languages whose marker files are in Root, separated
// by spaces, or collect.Unknown when none is found.
func (p Probe) DetectStack() string {
	var stack []string
	if p.exists("go.mod") {
		stack = append(stack, "Go")
	}
	if p.exists("package.json") {
		stack = append(stack, "TS")
	}
	if p.exists("Cargo.toml") {
		stack = append(stack, "Rust")
	}
	if p.anyExists("pom.xml", "build.gradle") {
		stack = append(stack, "Java")
	}
	if p.hasSuffix(".csproj", ".sln") {
		stack = append(stack, "C#")
	}
	if p.anyExists("requirements.txt", "pyproject.toml") {
		stack = append(stack, "Python")
	}
	if len(stack) == 0 {
		return collect.Unknown
	}
	return strings.Join(stack, " ")
}

// DetectCoverage lists the coverage artifacts present in Root, separated by
// spaces, or "none found".
func (p Probe) DetectCoverage() string {
	var found []string
	if p.exists("coverage.out") {
		found = append(found, "Go:coverage.out")
	}
	if p.exists("coverage") {
		found = append(found, "JS:coverage/")
	}
	if p.exists("cobertura.xml") {
		found = append(found, "cobertura.xml")
	}
	if p.exists("lcov.info") {
		found = append(found, "lcov.info")
	}
	if len(found) == 0 {
		return "none found"
	}
	return strings.Join(found, " ")
}

// DetectFrontend answers collect.Yes when package.json or tsconfig.json is in
// Root, collect.No otherwise.
func (p Probe) DetectFrontend() string {
	if p.anyExists("package.json", "tsconfig.json") {
		return collect.Yes
	}
	return collect.No
}

// cssFrameworks is the detection order of DetectCSSFramework.
var cssFrameworks = []string{"tailwindcss", "styled-components", "@emotion/", "sass", "less"}

// DetectCSSFramework names the first known CSS framework mentioned in
// package.json, or collect.Unknown when none is or the file is unreadable.
func (p Probe) DetectCSSFramework() string {
	data, err := os.ReadFile(collect.Resolve(p.Root, "package.json"))
	if err != nil {
		return collect.Unknown
	}
	text := string(data)
	for _, fw := range cssFrameworks {
		if strings.Contains(text, fw) {
			return fw
		}
	}
	return collect.Unknown
}

// buildTool is one project kind DetectBuildTool recognises: its marker file,
// its label line and the version commands to run.
type buildTool struct {
	marker   string
	label    string
	commands [][]string
}

// buildTools is the detection order of DetectBuildTool.
var buildTools = []buildTool{
	{"Cargo.toml", "=== Rust Project ===", [][]string{{"cargo", "--version"}, {"rustc", "--version"}}},
	{"package.json", "=== Node Project ===", [][]string{{"node", "--version"}, {"npm", "--version"}}},
	{"go.mod", "=== Go Project ===", [][]string{{"go", "version"}}},
	{"pom.xml", "=== Java/Maven Project ===", nil},
	{"build.gradle", "=== Java/Gradle Project ===", nil},
}

// DetectBuildTool reports the first project kind whose marker file is in
// Root as a label line followed by the output of its version commands; a
// command that fails or prints nothing is skipped. It answers
// collect.NoBuildTool when no marker file is present.
func (p Probe) DetectBuildTool(ctx context.Context) string {
	for _, tool := range buildTools {
		if !p.exists(tool.marker) {
			continue
		}
		lines := []string{tool.label}
		for _, cmd := range tool.commands {
			out, err := p.runner().Run(ctx, p.Root, cmd[0], cmd[1:]...)
			if err != nil || out == "" {
				continue
			}
			lines = append(lines, out)
		}
		return strings.Join(lines, "\n")
	}
	return collect.NoBuildTool
}

// DetectPort returns the first of ports that accepts a TCP connection on
// 127.0.0.1, in decimal, or collect.NotDetected when none does.
func (p Probe) DetectPort(ports []int) string {
	for _, port := range ports {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
		if err != nil {
			continue
		}
		conn.Close()
		return strconv.Itoa(port)
	}
	return collect.NotDetected
}
