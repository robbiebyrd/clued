export const name        = 'line-summary';
export const collection  = 'transcript_lines';
export const enabled     = false;
export const batchLimit  = 10;

const TOKEN_THRESHOLD = 200;

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<{ summary: string | null; model: string | null }> {
  const msg = (doc.line as Record<string, unknown> | undefined)?.message as Record<string, unknown> | undefined;
  if (msg?.role !== 'assistant') return { summary: null, model: null };

  const content = (msg.content ?? []) as Array<Record<string, unknown>>;
  const text    = content
    .filter(b => b.type === 'text' && typeof b.text === 'string')
    .map(b => b.text as string)
    .join('');

  if (Math.ceil(text.length / 4) <= TOKEN_THRESHOLD) return { summary: null, model: null };

  // Add @anthropic-ai/sdk to package.json and implement the API call here when enabling.
  return { summary: null, model: null };
}
