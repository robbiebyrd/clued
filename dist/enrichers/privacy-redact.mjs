import { createRequire } from 'module'; const require = createRequire(import.meta.url);
const name = "privacy-redact";
const collection = "transcript_lines";
const enabled = false;
const PATTERNS = [
  { label: "api-key", re: /\b(sk-[A-Za-z0-9]{20,})\b/g },
  { label: "email", re: /\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z]{2,}\b/gi },
  { label: "aws-key", re: /\b(AKIA[0-9A-Z]{16})\b/g },
  { label: "jwt", re: /\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b/g }
];
function matches(doc) {
  return doc.line != null;
}
async function enrich(doc) {
  const raw = typeof doc.line === "string" ? doc.line : JSON.stringify(doc.line);
  let redacted = raw;
  for (const { label, re } of PATTERNS) {
    redacted = redacted.replace(re, `[REDACTED:${label}]`);
  }
  return { redacted_line: redacted };
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
