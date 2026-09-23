export const name       = 'tool-use-summary';
export const collection = 'transcript_lines';
export const enabled    = true;

interface ToolSummary { id: string; name: string; inputKeys: string[]; }
interface Result { tools: ToolSummary[]; has_thinking: boolean; }

const EMPTY: Result = { tools: [], has_thinking: false };

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const msg = (doc.line as Record<string, unknown> | undefined)?.message as Record<string, unknown> | undefined;
  if (msg?.role !== 'assistant') return EMPTY;

  const content = (msg.content ?? []) as Array<Record<string, unknown>>;
  const tools: ToolSummary[] = [];
  let has_thinking = false;

  for (const block of content) {
    if (block.type === 'thinking') {
      has_thinking = true;
    } else if (block.type === 'tool_use') {
      tools.push({
        id:        block.id as string,
        name:      block.name as string,
        inputKeys: Object.keys((block.input as Record<string, unknown>) ?? {}),
      });
    }
  }

  return { tools, has_thinking };
}
