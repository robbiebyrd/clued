export const name       = 'line-classifier';
export const collection = 'transcript_lines';
export const enabled    = true;

type LineType = 'user_prompt' | 'assistant' | 'tool_result' |
               'hook_success' | 'hook_error' | 'session_meta' | 'unknown';

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<{ type: LineType }> {
  const line = doc.line as Record<string, unknown> | null | undefined;
  if (!line || typeof line !== 'object') return { type: 'unknown' };

  if (line.type === 'last-prompt' || line.type === 'permission-mode') return { type: 'session_meta' };

  const att = line.attachment as Record<string, unknown> | undefined;
  if (att?.type === 'hook_success') return { type: 'hook_success' };
  if (att?.type === 'hook_error')   return { type: 'hook_error' };

  if (line.display != null && line.sessionId != null) return { type: 'user_prompt' };

  const msg = line.message as Record<string, unknown> | undefined;
  const content = (msg?.content ?? []) as Array<Record<string, unknown>>;

  if (msg?.role === 'user' && content.some(b => b.type === 'tool_result')) return { type: 'tool_result' };
  if (msg?.role === 'assistant') return { type: 'assistant' };

  return { type: 'unknown' };
}
