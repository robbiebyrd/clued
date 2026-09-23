import { appendFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'fs';
import { dirname } from 'path';

export function appendToWal(walPath: string, event: Record<string, unknown>): void {
  mkdirSync(dirname(walPath), { recursive: true });
  appendFileSync(walPath, JSON.stringify(event) + '\n');
}

export async function flushWal(
  walPath: string,
  insert: (doc: Record<string, unknown>) => Promise<void>,
): Promise<void> {
  if (!existsSync(walPath)) return;

  const lines = readFileSync(walPath, 'utf8').split('\n').filter(Boolean);
  if (lines.length === 0) return;

  const failed: string[] = [];
  for (const line of lines) {
    let doc: Record<string, unknown>;
    try {
      doc = JSON.parse(line) as Record<string, unknown>;
    } catch {
      continue; // skip malformed lines
    }
    try {
      await insert(doc);
    } catch {
      failed.push(line);
    }
  }

  writeFileSync(walPath, failed.length > 0 ? failed.join('\n') + '\n' : '');
}
