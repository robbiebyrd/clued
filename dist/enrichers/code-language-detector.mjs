import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/code-language-detector.ts
var name = "code-language-detector";
var collection = "transcript_lines";
var enabled = true;
function matches(_doc) {
  return true;
}
async function enrich(doc) {
  const msg = doc.line?.message;
  if (msg?.role !== "assistant") return { languages: [] };
  const content = msg.content ?? [];
  const seen = /* @__PURE__ */ new Set();
  for (const block of content) {
    if (block.type !== "text" || typeof block.text !== "string") continue;
    const fenceRe = /^```(\w+)\s*$/gm;
    for (const m of block.text.matchAll(fenceRe)) {
      seen.add(m[1].toLowerCase());
    }
  }
  return { languages: [...seen] };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
