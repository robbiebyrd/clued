export const name       = 'bash-outcome';
export const collection = 'hook_events';
export const enabled    = true;

interface Result { success: boolean; has_error: boolean; output_lines: number; truncated: boolean; }

export function matches(doc: Record<string, unknown>): boolean {
  return doc.hook_event_name === 'PostToolUse' && doc.tool_name === 'Bash';
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const tr     = doc.tool_response as Record<string, unknown> | undefined;
  const stderr = tr?.stderr;
  const stdout = (tr?.stdout ?? '') as string;
  const success = !stderr || stderr === '';
  return {
    success,
    has_error:    !success,
    output_lines: stdout ? stdout.split('\n').length : 0,
    truncated:    stdout.includes('[truncated]'),
  };
}
