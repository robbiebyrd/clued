export const name       = 'edit-diff-stats';
export const collection = 'hook_events';
export const enabled    = true;

interface Result { lines_added: number; lines_removed: number; net_change: number; }

function countLines(s: string): number {
  return s ? s.split('\n').length : 0;
}

export function matches(doc: Record<string, unknown>): boolean {
  return doc.hook_event_name === 'PostToolUse' && (doc.tool_name === 'Edit' || doc.tool_name === 'Write');
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  // tool_input is forwarded on PostToolUse events by Claude Code.
  const ti = doc.tool_input as Record<string, unknown> | undefined;
  if (doc.tool_name === 'Write') {
    const lines_added = countLines((ti?.content ?? '') as string);
    return { lines_added, lines_removed: 0, net_change: lines_added };
  }
  const lines_added   = countLines((ti?.new_string ?? '') as string);
  const lines_removed = countLines((ti?.old_string ?? '') as string);
  return { lines_added, lines_removed, net_change: lines_added - lines_removed };
}
