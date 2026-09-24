import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/edit-diff-stats.ts
var name = "edit-diff-stats";
var collection = "hook_events";
var enabled = true;
function countLines(s) {
  return s ? s.split("\n").length : 0;
}
function matches(doc) {
  return doc.hook_event_name === "PostToolUse" && (doc.tool_name === "Edit" || doc.tool_name === "Write");
}
async function enrich(doc) {
  const ti = doc.tool_input;
  if (doc.tool_name === "Write") {
    const lines_added2 = countLines(ti?.content ?? "");
    return { lines_added: lines_added2, lines_removed: 0, net_change: lines_added2 };
  }
  const lines_added = countLines(ti?.new_string ?? "");
  const lines_removed = countLines(ti?.old_string ?? "");
  return { lines_added, lines_removed, net_change: lines_added - lines_removed };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
