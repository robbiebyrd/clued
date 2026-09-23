export const name       = 'skill-detector';
export const collection = 'transcript_lines';
export const enabled    = true;

interface SkillRef { name: string; args?: string; }

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<{ skills: SkillRef[] }> {
  const msg = (doc.line as Record<string, unknown> | undefined)?.message as Record<string, unknown> | undefined;
  if (msg?.role !== 'assistant') return { skills: [] };

  const content = (msg.content ?? []) as Array<Record<string, unknown>>;
  const skills: SkillRef[] = [];

  for (const block of content) {
    if (block.type !== 'tool_use' || block.name !== 'Skill') continue;
    const input = block.input as Record<string, unknown> | undefined;
    const skillName = input?.skill;
    if (typeof skillName !== 'string') continue;
    const ref: SkillRef = { name: skillName };
    if (typeof input?.args === 'string') ref.args = input.args;
    skills.push(ref);
  }

  return { skills };
}
