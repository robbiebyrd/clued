export const name       = 'message-stats';
export const collection = 'transcript_lines';
export const enabled    = true;

interface Result { char_count: number; word_count: number; block_count: number; token_estimate: number; }

const ZERO: Result = { char_count: 0, word_count: 0, block_count: 0, token_estimate: 0 };

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const line = doc.line as Record<string, unknown> | undefined;
  if (!line) return ZERO;

  let text = '';
  let block_count = 0;

  if (typeof line.display === 'string' && line.sessionId != null) {
    text = line.display;
  } else {
    const msg = line.message as Record<string, unknown> | undefined;
    const content = (msg?.content ?? []) as Array<Record<string, unknown>>;
    block_count = content.length;
    for (const block of content) {
      if (block.type === 'text' && typeof block.text === 'string') text += block.text;
      if (block.type === 'thinking' && typeof block.thinking === 'string') text += block.thinking;
    }
  }

  const char_count     = text.length;
  const word_count     = text.trim() ? text.trim().split(/\s+/).length : 0;
  const token_estimate = Math.ceil(char_count / 4);

  return { char_count, word_count, block_count, token_estimate };
}
