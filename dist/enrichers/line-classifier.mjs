import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/line-classifier.ts
var name = "line-classifier";
var collection = "transcript_lines";
var enabled = true;
function matches(_doc) {
  return true;
}
async function enrich(doc) {
  const line = doc.line;
  if (!line || typeof line !== "object") return { type: "unknown" };
  if (line.type === "last-prompt" || line.type === "permission-mode") return { type: "session_meta" };
  const att = line.attachment;
  if (att?.type === "hook_success") return { type: "hook_success" };
  if (att?.type === "hook_error") return { type: "hook_error" };
  if (line.display != null && line.sessionId != null) return { type: "user_prompt" };
  const msg = line.message;
  const content = msg?.content ?? [];
  if (msg?.role === "user" && content.some((b) => b.type === "tool_result")) return { type: "tool_result" };
  if (msg?.role === "assistant") return { type: "assistant" };
  return { type: "unknown" };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
