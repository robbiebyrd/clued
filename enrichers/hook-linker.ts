import type { ObjectId } from 'mongodb';
import type { MongoDb } from '../src/mongo';

export const name        = 'hook-linker';
export const collection  = 'transcript_lines';
export const enabled     = true;
export const batchLimit  = 20;

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(
  doc: Record<string, unknown>,
  mongo: MongoDb,
): Promise<{ tool_use_ids: string[]; hook_event_ids: ObjectId[] }> {
  const line       = doc.line as Record<string, unknown> | undefined;
  const session_id = doc.session_id as string | undefined;
  const ids        = new Set<string>();

  const attId = (line?.attachment as Record<string, unknown> | undefined)?.toolUseID;
  if (typeof attId === 'string') ids.add(attId);

  const content = ((line?.message as Record<string, unknown> | undefined)?.content ?? []) as Array<Record<string, unknown>>;
  for (const block of content) {
    if (block.type === 'tool_use' && typeof block.id === 'string') ids.add(block.id);
  }

  const tool_use_ids = [...ids];
  if (tool_use_ids.length === 0) return { tool_use_ids: [], hook_event_ids: [] };

  const filter: Record<string, unknown> = { tool_use_id: { $in: tool_use_ids } };
  if (session_id) filter.session_id = session_id;

  const events = await mongo.hookEvents
    .find(filter, { projection: { _id: 1 } })
    .toArray();

  return { tool_use_ids, hook_event_ids: events.map(e => e._id) };
}
