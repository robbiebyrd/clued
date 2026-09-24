export const EXT_LANG: Record<string, string> = {
  '.ts': 'typescript', '.tsx': 'typescript',
  '.js': 'javascript', '.jsx': 'javascript', '.mjs': 'javascript', '.cjs': 'javascript',
  '.py': 'python', '.rb': 'ruby', '.go': 'go', '.rs': 'rust',
  '.java': 'java', '.c': 'c', '.cpp': 'cpp', '.cs': 'csharp',
  '.sh': 'bash', '.bash': 'bash',
  '.md': 'markdown', '.mdx': 'markdown',
  '.json': 'json', '.yaml': 'yaml', '.yml': 'yaml', '.toml': 'toml',
  '.env': 'dotenv',
  '.html': 'html', '.css': 'css', '.scss': 'css', '.sass': 'css',
  '.vue': 'vue', '.svelte': 'svelte',
  '.sql': 'sql', '.graphql': 'graphql',
};

export function extToLang(filePath: string | null): string | null {
  if (!filePath) return null;
  const dot = filePath.lastIndexOf('.');
  if (dot < 0) return null;
  return EXT_LANG[filePath.slice(dot)] ?? null;
}
