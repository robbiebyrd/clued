import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/lang.ts
var EXT_LANG = {
  ".ts": "typescript",
  ".tsx": "typescript",
  ".js": "javascript",
  ".jsx": "javascript",
  ".mjs": "javascript",
  ".cjs": "javascript",
  ".py": "python",
  ".rb": "ruby",
  ".go": "go",
  ".rs": "rust",
  ".java": "java",
  ".c": "c",
  ".cpp": "cpp",
  ".cs": "csharp",
  ".sh": "bash",
  ".bash": "bash",
  ".md": "markdown",
  ".mdx": "markdown",
  ".json": "json",
  ".yaml": "yaml",
  ".yml": "yaml",
  ".toml": "toml",
  ".env": "dotenv",
  ".html": "html",
  ".css": "css",
  ".scss": "css",
  ".sass": "css",
  ".vue": "vue",
  ".svelte": "svelte",
  ".sql": "sql",
  ".graphql": "graphql"
};
function extToLang(filePath) {
  if (!filePath) return null;
  const dot = filePath.lastIndexOf(".");
  if (dot < 0) return null;
  return EXT_LANG[filePath.slice(dot)] ?? null;
}

// enrichers/file-snapshot-extractor.ts
var name = "file-snapshot-extractor";
var collection = "transcript_lines";
var enabled = true;
function matches(doc) {
  const line = doc.line;
  if (line?.type !== "file-history-snapshot") return false;
  const snapshot = line.snapshot;
  const backups = snapshot?.trackedFileBackups;
  return !!backups && Object.keys(backups).length > 0;
}
async function enrich(doc) {
  const line = doc.line;
  const snapshot = line.snapshot;
  const backups = snapshot.trackedFileBackups;
  const files = Object.keys(backups);
  const languages = [...new Set(
    files.map((f) => extToLang(f)).filter((l) => l !== null)
  )];
  return { files, languages, file_count: files.length };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
