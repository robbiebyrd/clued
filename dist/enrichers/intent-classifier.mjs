import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/intent-classifier.ts
var name = "intent-classifier";
var collection = "transcript_lines";
var enabled = true;
var QUESTION_OPENER = /^\b(what|how|why|when|where|is|are|can|does|should|could|would)\b/i;
var INSTRUCTION = /\b(fix|add|remove|change|update|make|create|write|implement|refactor|delete|rename)\b/i;
var APPROVAL = /\b(looks good|lgtm|go for it|proceed|approved|sounds good)\b/i;
var CORRECTION = /^(no|wrong|incorrect|that's not|don't do|shouldn't)\b/i;
function matches(_doc) {
  return true;
}
async function enrich(doc) {
  const line = doc.line;
  const display = line?.display;
  if (typeof display !== "string") return { intent: null };
  const d = display.trim();
  if (!d) return { intent: null };
  if (d.startsWith("/")) return { intent: "slash_command" };
  if (d.endsWith("?") || QUESTION_OPENER.test(d)) return { intent: "question" };
  if (INSTRUCTION.test(d)) return { intent: "instruction" };
  const lower = d.toLowerCase();
  if (APPROVAL.test(lower) || lower === "yes" || lower === "ok" || lower === "sure") {
    return { intent: "approval" };
  }
  if (CORRECTION.test(d)) return { intent: "correction" };
  return { intent: "feedback" };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
