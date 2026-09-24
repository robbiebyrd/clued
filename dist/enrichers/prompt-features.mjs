import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/prompt-features.ts
var name = "prompt-features";
var collection = "hook_events";
var enabled = true;
var QUESTION_OPENER = /^\b(what|how|why|when|where|is|are|can|does|should|could|would)\b/i;
var INSTRUCTION = /\b(fix|add|remove|change|update|make|create|write|implement|refactor|delete|rename)\b/i;
var APPROVAL = /\b(looks good|lgtm|go for it|proceed|approved|sounds good)\b/i;
var CORRECTION = /^(no|wrong|incorrect|that's not|don't do|shouldn't)\b/i;
var CONTINUATION = /^(also|and also|now also|one more)\b/i;
function classifyIntent(prompt) {
  if (prompt.endsWith("?") || QUESTION_OPENER.test(prompt)) return "question";
  if (INSTRUCTION.test(prompt)) return "instruction";
  const lower = prompt.toLowerCase();
  if (APPROVAL.test(lower) || lower === "yes" || lower === "ok" || lower === "sure") return "approval";
  if (CORRECTION.test(prompt)) return "correction";
  return null;
}
function matches(doc) {
  return doc.hook_event_name === "UserPromptSubmit";
}
async function enrich(doc) {
  const prompt = (doc.prompt ?? "").trim();
  const word_count = prompt ? prompt.split(/\s+/).length : 0;
  const is_slash_command = prompt.startsWith("/");
  const command_name = is_slash_command ? prompt.split(/\s+/)[0].slice(1) || null : null;
  const intent = is_slash_command ? null : classifyIntent(prompt);
  const looks_like_task_start = intent === "instruction" && word_count >= 6 && !CONTINUATION.test(prompt);
  return { word_count, is_slash_command, command_name, intent, looks_like_task_start };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
