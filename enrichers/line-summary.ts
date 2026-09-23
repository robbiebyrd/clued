export const name        = 'line-summary';
export const collection  = 'transcript_lines';
export const enabled     = false;
export const batchLimit  = 10;

const TOKEN_THRESHOLD = 200;
const MODEL           = 'claude-haiku-4-5-20251001';

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

  // Requires @anthropic-ai/sdk — add to package.json dependencies when enabling.
  // import Anthropic from '@anthropic-ai/sdk';
  // const client = new Anthropic();
  // const response = await client.messages.create({
  //   model: MODEL,
  //   max_tokens: 60,
  //   messages: [{ role: 'user', content: `Summarize what Claude is doing in this message in one sentence (max 20 words):\n\n${text}` }],
  // });
  // const summary = (response.content[0] as { type: 'text'; text: string }).text.trim();
  // return { summary, model: MODEL };

  return { summary: null, model: null };
}
