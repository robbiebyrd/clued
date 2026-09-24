import { extToLang } from './lang.js';

export const name       = 'file-snapshot-extractor';
export const collection = 'transcript_lines';
export const enabled    = true;

interface Result { files: string[]; languages: string[]; file_count: number; }

export function matches(doc: Record<string, unknown>): boolean {
  const line    = doc.line as Record<string, unknown> | undefined;
  if (line?.type !== 'file-history-snapshot') return false;
  const snapshot = line.snapshot as Record<string, unknown> | undefined;
  const backups  = snapshot?.trackedFileBackups as Record<string, unknown> | undefined;
  return !!backups && Object.keys(backups).length > 0;
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const line    = doc.line as Record<string, unknown>;
  const snapshot = line.snapshot as Record<string, unknown>;
  const backups  = snapshot.trackedFileBackups as Record<string, unknown>;
  const files    = Object.keys(backups);
  const languages = [...new Set(
    files.map(f => extToLang(f)).filter((l): l is string => l !== null),
  )];
  return { files, languages, file_count: files.length };
}
