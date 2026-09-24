import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// enrichers/skill-detector.ts
var name = "skill-detector";
var collection = "transcript_lines";
var enabled = true;
function matches(_doc) {
  return true;
}
async function enrich(doc) {
  const msg = doc.line?.message;
  if (msg?.role !== "assistant") return { skills: [] };
  const content = msg.content ?? [];
  const skills = [];
  for (const block of content) {
    if (block.type !== "tool_use" || block.name !== "Skill") continue;
    const input = block.input;
    const skillName = input?.skill;
    if (typeof skillName !== "string") continue;
    const ref = { name: skillName };
    if (typeof input?.args === "string") ref.args = input.args;
    skills.push(ref);
  }
  return { skills };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
