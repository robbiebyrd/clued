export const name       = 'tool-classifier';
export const collection = 'hook_events';
export const enabled    = true;

type Category = 'file_read' | 'file_write' | 'file_edit' | 'shell_exec' | 'web_fetch' | 'code_search' | 'agent_dispatch' | 'other';
interface Result { category: Category; is_read_only: boolean; }

export function matches(doc: Record<string, unknown>): boolean {
  return doc.hook_event_name === 'PreToolUse';
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const tool = doc.tool_name as string | undefined;
  if (tool === 'Read')                                         return { category: 'file_read',      is_read_only: true  };
  if (tool === 'Write')                                        return { category: 'file_write',     is_read_only: false };
  if (tool === 'Edit')                                         return { category: 'file_edit',      is_read_only: false };
  if (tool === 'Bash')                                         return { category: 'shell_exec',     is_read_only: false };
  if (tool === 'WebFetch' || tool === 'WebSearch')             return { category: 'web_fetch',      is_read_only: true  };
  if (tool === 'Grep' || tool === 'Glob' || tool === 'LS')    return { category: 'code_search',    is_read_only: true  };
  if (tool === 'Agent' || tool === 'Task')                     return { category: 'agent_dispatch', is_read_only: false };
  return { category: 'other', is_read_only: false };
}
