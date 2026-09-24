import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/hook-linker.ts
var name = "hook-linker";
var collection = "transcript_lines";
var enabled = true;
var batchLimit = 20;
function matches(_doc) {
  return true;
}
async function enrich(doc, mongo) {
  const line = doc.line;
  const session_id = doc.session_id;
  const ids = /* @__PURE__ */ new Set();
  const attId = line?.attachment?.toolUseID;
  if (typeof attId === "string") ids.add(attId);
  const content = line?.message?.content ?? [];
  for (const block of content) {
    if (block.type === "tool_use" && typeof block.id === "string") ids.add(block.id);
  }
  const tool_use_ids = [...ids];
  if (tool_use_ids.length === 0) return { tool_use_ids: [], hook_event_ids: [] };
  const filter = { tool_use_id: { $in: tool_use_ids } };
  if (session_id) filter.session_id = session_id;
  const events = await mongo.hookEvents.find(filter, { projection: { _id: 1 } }).toArray();
  return { tool_use_ids, hook_event_ids: events.map((e) => e._id) };
}
export {
  batchLimit,
  collection,
  enabled,
  enrich,
  matches,
  name
};
