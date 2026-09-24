import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/error-flag.ts
var name = "error-flag";
var collection = "transcript_lines";
var enabled = true;
var ERROR_TEXT_RE = /\b(Error:|ENOENT|EACCES|exception|stack trace|exit code [^0])/;
function matches(_doc) {
  return true;
}
async function enrich(doc) {
  const line = doc.line;
  if (!line) return { is_error: false, error_type: null };
  const att = line.attachment;
  if (att?.type === "hook_error") return { is_error: true, error_type: "hook_error" };
  const msg = line.message;
  const content = msg?.content ?? [];
  if (msg?.role === "user" && content.some((b) => b.type === "tool_result" && b.is_error === true)) {
    return { is_error: true, error_type: "tool_failure" };
  }
  for (const block of content) {
    if (block.type === "text" && typeof block.text === "string" && ERROR_TEXT_RE.test(block.text)) {
      return { is_error: true, error_type: "error_text" };
    }
  }
  return { is_error: false, error_type: null };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
