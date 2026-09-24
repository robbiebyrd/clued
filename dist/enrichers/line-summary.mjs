import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/line-summary.ts
var name = "line-summary";
var collection = "transcript_lines";
var enabled = false;
var batchLimit = 10;
var TOKEN_THRESHOLD = 200;
function matches(_doc) {
  return true;
}
async function enrich(doc) {
  const msg = doc.line?.message;
  if (msg?.role !== "assistant") return { summary: null, model: null };
  const content = msg.content ?? [];
  const text = content.filter((b) => b.type === "text" && typeof b.text === "string").map((b) => b.text).join("");
  if (Math.ceil(text.length / 4) <= TOKEN_THRESHOLD) return { summary: null, model: null };
  return { summary: null, model: null };
}
export {
  batchLimit,
  collection,
  enabled,
  enrich,
  matches,
  name
};
