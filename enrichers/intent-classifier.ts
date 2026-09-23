export const name       = 'intent-classifier';
export const collection = 'transcript_lines';
export const enabled    = true;

type Intent = 'question' | 'instruction' | 'feedback' | 'correction' | 'approval' | 'slash_command' | null;

const QUESTION_OPENER = /^\b(what|how|why|when|where|is|are|can|does|should|could|would)\b/i;
const INSTRUCTION     = /\b(fix|add|remove|change|update|make|create|write|implement|refactor|delete|rename)\b/i;
const APPROVAL        = /\b(looks good|lgtm|go for it|proceed|approved|sounds good)\b/i;
const CORRECTION      = /^(no|wrong|incorrect|that's not|don't do|shouldn't)\b/i;

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<{ intent: Intent }> {
  const line    = doc.line as Record<string, unknown> | undefined;
  const display = line?.display;
  if (typeof display !== 'string') return { intent: null };

  const d = display.trim();
  if (!d) return { intent: null };

  if (d.startsWith('/')) return { intent: 'slash_command' };
  if (d.endsWith('?') || QUESTION_OPENER.test(d)) return { intent: 'question' };
  if (INSTRUCTION.test(d)) return { intent: 'instruction' };

  const lower = d.toLowerCase();
  if (APPROVAL.test(lower) || lower === 'yes' || lower === 'ok' || lower === 'sure') {
    return { intent: 'approval' };
  }
  if (CORRECTION.test(d)) return { intent: 'correction' };

  return { intent: 'feedback' };
}
