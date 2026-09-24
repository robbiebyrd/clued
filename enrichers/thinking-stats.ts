export const name       = 'thinking-stats';
export const collection = 'transcript_lines';
export const enabled    = true;

interface Result { thinking_chars: number; text_chars: number; }

export function matches(doc: Record<string, unknown>): boolean {
  return (doc.line as Record<string, unknown> | undefined)?.type === 'assistant';
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const line = doc.line as Record<string, unknown>;
  const msg  = line.message as Record<string, unknown> | undefined;
  if (msg?.role !== 'assistant') return { thinking_chars: 0, text_chars: 0 };

  const content = (msg.content ?? []) as Array<Record<string, unknown>>;
  let thinking_chars = 0;
  let text_chars     = 0;

  for (const block of content) {
    if (block.type === 'thinking' && typeof block.thinking === 'string') {
      thinking_chars += block.thinking.length;
    } else if (block.type === 'text' && typeof block.text === 'string') {
      text_chars += block.text.length;
    }
  }

  return { thinking_chars, text_chars };
}
