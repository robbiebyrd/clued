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

// enrichers/file-tracker.ts
var name = "file-tracker";
var collection = "hook_events";
var enabled = true;
var FILE_OPS = {
  Read: "read",
  Write: "write",
  Edit: "edit",
  Glob: "glob",
  LS: "ls"
};
var CONFIG_EXTS = /* @__PURE__ */ new Set([".json", ".yaml", ".yml", ".toml", ".env"]);
var SOURCE_EXTS = /* @__PURE__ */ new Set([
  ".ts",
  ".tsx",
  ".js",
  ".jsx",
  ".mjs",
  ".cjs",
  ".py",
  ".rb",
  ".go",
  ".rs",
  ".java",
  ".c",
  ".cpp",
  ".cs",
  ".sh",
  ".bash",
  ".html",
  ".css",
  ".scss",
  ".vue",
  ".svelte",
  ".sql",
  ".graphql"
]);
function detectArtifact(fp) {
  if (!fp) return null;
  const lower = fp.toLowerCase();
  const base = lower.split("/").pop() ?? "";
  const dot = base.lastIndexOf(".");
  const ext = dot >= 0 ? base.slice(dot) : "";
  if (/\/tests?\//.test(lower)) return "test";
  if (/\/specs?\//.test(lower)) return "spec";
  if (/\/plans?\//.test(lower)) return "plan";
  if (/\.(test|spec)\.[^.]+$/.test(lower)) return "test";
  if (/\b(spec|design|architecture)\b/.test(base)) return "spec";
  if (/\b(plan|roadmap|todo)\b/.test(base)) return "plan";
  if (ext === ".md" || ext === ".mdx") return "doc";
  if (CONFIG_EXTS.has(ext)) return "config";
  if (SOURCE_EXTS.has(ext)) return "code";
  return null;
}
function matches(doc) {
  return doc.hook_event_name === "PreToolUse" && typeof FILE_OPS[doc.tool_name] === "string";
}
async function enrich(doc) {
  const operation = FILE_OPS[doc.tool_name];
  const ti = doc.tool_input;
  const file_path = ti?.file_path ?? ti?.path ?? null;
  return {
    file_path,
    language: extToLang(file_path),
    operation,
    artifact_type: detectArtifact(file_path)
  };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
