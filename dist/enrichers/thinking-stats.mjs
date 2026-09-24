import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/thinking-stats.ts
var name = "thinking-stats";
var collection = "transcript_lines";
var enabled = true;
function matches(doc) {
  return doc.line?.type === "assistant";
}
async function enrich(doc) {
  const line = doc.line;
  const msg = line.message;
  if (msg?.role !== "assistant") return { thinking_chars: 0, text_chars: 0 };
  const content = msg.content ?? [];
  let thinking_chars = 0;
  let text_chars = 0;
  for (const block of content) {
    if (block.type === "thinking" && typeof block.thinking === "string") {
      thinking_chars += block.thinking.length;
    } else if (block.type === "text" && typeof block.text === "string") {
      text_chars += block.text.length;
    }
  }
  return { thinking_chars, text_chars };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
