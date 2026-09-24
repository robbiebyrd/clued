import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/message-stats.ts
var name = "message-stats";
var collection = "transcript_lines";
var enabled = true;
var ZERO = { char_count: 0, word_count: 0, block_count: 0, token_estimate: 0 };
function matches(_doc) {
  return true;
}
async function enrich(doc) {
  const line = doc.line;
  if (!line) return ZERO;
  let text = "";
  let block_count = 0;
  if (typeof line.display === "string" && line.sessionId != null) {
    text = line.display;
  } else {
    const msg = line.message;
    const content = msg?.content ?? [];
    block_count = content.length;
    for (const block of content) {
      if (block.type === "text" && typeof block.text === "string") text += block.text;
      if (block.type === "thinking" && typeof block.thinking === "string") text += block.thinking;
    }
  }
  const char_count = text.length;
  const word_count = text.trim() ? text.trim().split(/\s+/).length : 0;
  const token_estimate = Math.ceil(char_count / 4);
  return { char_count, word_count, block_count, token_estimate };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
