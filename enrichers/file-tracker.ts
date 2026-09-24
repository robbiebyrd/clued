import { extToLang } from './lang.js';

export const name       = 'file-tracker';
export const collection = 'hook_events';
export const enabled    = true;

type ArtifactType = 'plan' | 'spec' | 'doc' | 'test' | 'config' | 'code' | null;
type Operation    = 'read' | 'write' | 'edit' | 'glob' | 'ls';
interface Result { file_path: string | null; language: string | null; operation: Operation; artifact_type: ArtifactType; }

const FILE_OPS: Record<string, Operation> = {
  Read: 'read', Write: 'write', Edit: 'edit', Glob: 'glob', LS: 'ls',
};

const CONFIG_EXTS = new Set(['.json', '.yaml', '.yml', '.toml', '.env']);
const SOURCE_EXTS = new Set([
  '.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs',
  '.py', '.rb', '.go', '.rs', '.java', '.c', '.cpp', '.cs',
  '.sh', '.bash', '.html', '.css', '.scss', '.vue', '.svelte', '.sql', '.graphql',
]);

function detectArtifact(fp: string | null): ArtifactType {
  if (!fp) return null;
  const lower = fp.toLowerCase();
  const base  = lower.split('/').pop() ?? '';
  const dot   = base.lastIndexOf('.');
  const ext   = dot >= 0 ? base.slice(dot) : '';

  // Path-segment checks first — directory context beats filename keywords.
  if (/\/tests?\//.test(lower)) return 'test';
  if (/\/specs?\//.test(lower)) return 'spec';
  if (/\/plans?\//.test(lower)) return 'plan';
  // Filename pattern checks (lower confidence than directory context).
  if (/\.(test|spec)\.[^.]+$/.test(lower)) return 'test';
  if (/\b(spec|design|architecture)\b/.test(base)) return 'spec';
  if (/\b(plan|roadmap|todo)\b/.test(base)) return 'plan';
  if (ext === '.md' || ext === '.mdx') return 'doc';
  if (CONFIG_EXTS.has(ext)) return 'config';
  if (SOURCE_EXTS.has(ext)) return 'code';
  return null;
}

export function matches(doc: Record<string, unknown>): boolean {
  return doc.hook_event_name === 'PreToolUse' && typeof FILE_OPS[doc.tool_name as string] === 'string';
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const operation = FILE_OPS[doc.tool_name as string];
  const ti        = doc.tool_input as Record<string, unknown> | undefined;
  const file_path = ((ti?.file_path ?? ti?.path) as string | undefined) ?? null;
  return {
    file_path,
    language:      extToLang(file_path),
    operation,
    artifact_type: detectArtifact(file_path),
  };
}
