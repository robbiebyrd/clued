import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/tool-use-summary.ts
var name = "tool-use-summary";
var collection = "transcript_lines";
var enabled = true;
function matches(_doc) {
  return true;
}
async function enrich(doc) {
  const msg = doc.line?.message;
  if (msg?.role !== "assistant") return { tools: [], has_thinking: false };
  const content = msg.content ?? [];
  const tools = [];
  let has_thinking = false;
  for (const block of content) {
    if (block.type === "thinking") {
      has_thinking = true;
    } else if (block.type === "tool_use") {
      tools.push({
        id: block.id,
        name: block.name,
        inputKeys: Object.keys(block.input ?? {})
      });
    }
  }
  return { tools, has_thinking };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
