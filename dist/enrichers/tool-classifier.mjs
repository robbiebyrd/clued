import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/tool-classifier.ts
var name = "tool-classifier";
var collection = "hook_events";
var enabled = true;
function matches(doc) {
  return doc.hook_event_name === "PreToolUse";
}
async function enrich(doc) {
  const tool = doc.tool_name;
  if (tool === "Read") return { category: "file_read", is_read_only: true };
  if (tool === "Write") return { category: "file_write", is_read_only: false };
  if (tool === "Edit") return { category: "file_edit", is_read_only: false };
  if (tool === "Bash") return { category: "shell_exec", is_read_only: false };
  if (tool === "WebFetch" || tool === "WebSearch") return { category: "web_fetch", is_read_only: true };
  if (tool === "Grep" || tool === "Glob" || tool === "LS") return { category: "code_search", is_read_only: true };
  if (tool === "Agent" || tool === "Task") return { category: "agent_dispatch", is_read_only: false };
  return { category: "other", is_read_only: false };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
