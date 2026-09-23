export const name       = 'privacy-redact';
export const collection = 'transcript_lines';
export const enabled    = false;

const PATTERNS: Array<{ label: string; re: RegExp }> = [
  { label: 'api-key', re: /\b(sk-[A-Za-z0-9]{20,})\b/g },
  { label: 'email',   re: /\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z]{2,}\b/gi },
  { label: 'aws-key', re: /\b(AKIA[0-9A-Z]{16})\b/g },
  { label: 'jwt',     re: /\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b/g },
];

export function matches(doc: Record<string, unknown>): boolean {
  return doc.line != null;
}

export async function enrich(doc: Record<string, unknown>): Promise<{ redacted_line: string }> {
  const raw = typeof doc.line === 'string' ? doc.line : JSON.stringify(doc.line);
  let redacted = raw;
  for (const { label, re } of PATTERNS) {
    redacted = redacted.replace(re, `[REDACTED:${label}]`);
  }
  return { redacted_line: redacted };
}
