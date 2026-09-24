import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/bash-outcome.ts
var name = "bash-outcome";
var collection = "hook_events";
var enabled = true;
function matches(doc) {
  return doc.hook_event_name === "PostToolUse" && doc.tool_name === "Bash";
}
async function enrich(doc) {
  const tr = doc.tool_response;
  const stderr = tr?.stderr;
  const stdout = tr?.stdout ?? "";
  const success = !stderr || stderr === "";
  return {
    success,
    has_error: !success,
    output_lines: stdout ? stdout.split("\n").length : 0,
    truncated: stdout.includes("[truncated]")
  };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
