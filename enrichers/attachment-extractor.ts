import { extToLang } from './lang.js';

export const name       = 'attachment-extractor';
export const collection = 'transcript_lines';
export const enabled    = true;

interface Result { file_path: string | null; language: string | null; size_chars: number | null; }

export function matches(doc: Record<string, unknown>): boolean {
  return (doc.line as Record<string, unknown> | undefined)?.type === 'attachment';
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const line    = doc.line as Record<string, unknown>;
  const att     = (line.attachment as Record<string, unknown> | undefined) ?? {};
  const file_path = (att.file_path as string | undefined) ?? null;
  const content   = (att.content as string | undefined) ?? null;
  return {
    file_path,
    language:   extToLang(file_path),
    size_chars: typeof content === 'string' ? content.length : null,
  };
}
