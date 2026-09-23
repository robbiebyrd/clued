export const name       = 'error-flag';
export const collection = 'transcript_lines';
export const enabled    = true;

type ErrorType = 'hook_error' | 'tool_failure' | 'error_text' | null;
interface Result { is_error: boolean; error_type: ErrorType; }

const ERROR_TEXT_RE = /\b(Error:|ENOENT|EACCES|exception|stack trace|exit code [^0])/;

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const line = doc.line as Record<string, unknown> | undefined;
  if (!line) return { is_error: false, error_type: null };

  const att = line.attachment as Record<string, unknown> | undefined;
  if (att?.type === 'hook_error') return { is_error: true, error_type: 'hook_error' };

  const msg = line.message as Record<string, unknown> | undefined;
  const content = (msg?.content ?? []) as Array<Record<string, unknown>>;

  if (msg?.role === 'user' && content.some(b => b.type === 'tool_result' && b.is_error === true)) {
    return { is_error: true, error_type: 'tool_failure' };
  }

  for (const block of content) {
    if (block.type === 'text' && typeof block.text === 'string' && ERROR_TEXT_RE.test(block.text)) {
      return { is_error: true, error_type: 'error_text' };
    }
  }

  return { is_error: false, error_type: null };
}
