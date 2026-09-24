export const name       = 'prompt-features';
export const collection = 'hook_events';
export const enabled    = true;

type Intent = 'instruction' | 'question' | 'correction' | 'approval' | null;
interface Result {
  word_count:             number;
  is_slash_command:       boolean;
  command_name:           string | null;
  intent:                 Intent;
  looks_like_task_start:  boolean;
}

const QUESTION_OPENER = /^\b(what|how|why|when|where|is|are|can|does|should|could|would)\b/i;
const INSTRUCTION     = /\b(fix|add|remove|change|update|make|create|write|implement|refactor|delete|rename)\b/i;
const APPROVAL        = /\b(looks good|lgtm|go for it|proceed|approved|sounds good)\b/i;
const CORRECTION      = /^(no|wrong|incorrect|that's not|don't do|shouldn't)\b/i;
const CONTINUATION    = /^(also|and also|now also|one more)\b/i;

function classifyIntent(prompt: string): Intent {
  if (prompt.endsWith('?') || QUESTION_OPENER.test(prompt)) return 'question';
  if (INSTRUCTION.test(prompt)) return 'instruction';
  const lower = prompt.toLowerCase();
  if (APPROVAL.test(lower) || lower === 'yes' || lower === 'ok' || lower === 'sure') return 'approval';
  if (CORRECTION.test(prompt)) return 'correction';
  return null;
}

export function matches(doc: Record<string, unknown>): boolean {
  return doc.hook_event_name === 'UserPromptSubmit';
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const prompt  = ((doc.prompt ?? '') as string).trim();
  const word_count          = prompt ? prompt.split(/\s+/).length : 0;
  const is_slash_command    = prompt.startsWith('/');
  const command_name        = is_slash_command ? (prompt.split(/\s+/)[0].slice(1) || null) : null;
  const intent              = is_slash_command ? null : classifyIntent(prompt);
  const looks_like_task_start = intent === 'instruction' && word_count >= 6 && !CONTINUATION.test(prompt);
  return { word_count, is_slash_command, command_name, intent, looks_like_task_start };
}
