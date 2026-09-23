export const name       = 'code-language-detector';
export const collection = 'transcript_lines';
export const enabled    = true;

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<{ languages: string[] }> {
  const msg = (doc.line as Record<string, unknown> | undefined)?.message as Record<string, unknown> | undefined;
  if (msg?.role !== 'assistant') return { languages: [] };

  const content = (msg.content ?? []) as Array<Record<string, unknown>>;
  const seen = new Set<string>();

  for (const block of content) {
    if (block.type !== 'text' || typeof block.text !== 'string') continue;
    // Regex created per-call to avoid module-scope gm flag lastIndex footgun
    const fenceRe = /^```(\w+)\s*$/gm;
    for (const m of block.text.matchAll(fenceRe)) {
      seen.add(m[1].toLowerCase());
    }
  }

  return { languages: [...seen] };
}
