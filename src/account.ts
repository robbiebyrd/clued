import { readFileSync } from 'fs';

export function readAccountId(path: string): string {
  try {
    const raw  = readFileSync(path, 'utf8');
    const data = JSON.parse(raw) as { lastKnownAccountUuid?: string };
    const id   = data.lastKnownAccountUuid ?? 'unknown';
    if (id === 'unknown') console.warn('clued: account ID unavailable — isolation is degraded');
    return id;
  } catch {
    console.warn('clued: account ID unavailable — isolation is degraded');
    return 'unknown';
  }
}
