package session

import (
	"net/url"
	"path/filepath"
	"strings"
)

// MemoryProjectKey is the key memory records are filed under
// (memory.project): the git origin normalised to host/owner/repo
// (lower-cased, without scheme, user, port and ".git", so the ssh, https
// and scp forms of one remote agree), or the cleaned project path when
// there is no origin. Worktrees and clones of one repository share the
// origin, so they share memory; an empty origin and path yield "".
func MemoryProjectKey(gitOrigin, projectPath string) string {
	if origin := strings.TrimSpace(gitOrigin); origin != "" {
		host, path := splitOrigin(origin)
		host = strings.ToLower(host)
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host, "]") {
			host = host[:i]
		}
		path = strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
		path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
		var parts []string
		if host != "" {
			parts = append(parts, host)
		}
		for _, p := range strings.Split(path, "/") {
			if p != "" && p != "." && p != ".." {
				parts = append(parts, p)
			}
		}
		if key := strings.Join(parts, "/"); key != "" {
			return key
		}
	}
	if p := strings.TrimSpace(projectPath); p != "" {
		return filepath.ToSlash(filepath.Clean(p))
	}
	return ""
}

// splitOrigin separates a git origin into host and path: URLs (ssh://,
// https://, git://, file://), the scp-like [user@]host:path form and bare
// paths.
func splitOrigin(origin string) (host, path string) {
	if i := strings.Index(origin, "://"); i > 0 {
		if u, err := url.Parse(origin); err == nil {
			return u.Host, u.Path
		}
		return "", origin[i+3:]
	}
	if i := strings.Index(origin, ":"); i > 0 && !strings.Contains(origin[:i], "/") {
		return origin[:i], origin[i+1:]
	}
	return "", origin
}
