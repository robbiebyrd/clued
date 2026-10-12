package session

import "strings"

var extLang = map[string]string{
	".ts": "typescript", ".tsx": "typescript",
	".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".py": "python", ".rb": "ruby", ".go": "go", ".rs": "rust",
	".java": "java", ".c": "c", ".cpp": "cpp", ".cs": "csharp",
	".sh": "bash", ".bash": "bash",
	".md": "markdown", ".mdx": "markdown",
	".json": "json", ".yaml": "yaml", ".yml": "yaml", ".toml": "toml",
	".env":  "dotenv",
	".html": "html", ".css": "css", ".scss": "css", ".sass": "css",
	".vue": "vue", ".svelte": "svelte",
	".sql": "sql", ".graphql": "graphql",
}

// ExtToLang maps a file path's extension (from its last dot) to a language
// name. It returns "" when the path has no extension or the extension is unknown.
func ExtToLang(path string) string {
	dot := strings.LastIndex(path, ".")
	if dot < 0 {
		return ""
	}
	return extLang[path[dot:]]
}
