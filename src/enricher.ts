import { readdir } from 'fs/promises';
import { join, extname } from 'path';
import { pathToFileURL } from 'url';
import type { MongoDb } from './mongo';
import type { Config } from './config';

export interface Enricher {
  name:       string;
  collection: string;
  enabled?:   boolean;
  matches(doc: Record<string, unknown>): boolean;
  enrich(doc: Record<string, unknown>): Promise<unknown>;
}

export async function loadEnrichers(
  enrichersDir: string,
  config: Pick<Config, 'disabledEnrichers'>,
): Promise<Enricher[]> {
  let files: string[];
  try {
    files = await readdir(enrichersDir);
  } catch {
    return [];
  }

  const EXTS = new Set(['.ts', '.mjs']);
  const enrichers: Enricher[] = [];
  for (const file of files.filter(f => EXTS.has(extname(f)))) {
    const mod = await import(pathToFileURL(join(enrichersDir, file)).href) as Enricher;
    if (mod.enabled === false) continue;
    if ((config.disabledEnrichers ?? []).includes(mod.name)) continue;
    enrichers.push(mod);
  }
  return enrichers;
}

export function startEnrichmentLoop(
  mongo: MongoDb,
  enrichers: Enricher[],
): { stop: () => void } {
  const timer = setInterval(async () => {
    for (const enricher of enrichers) {
      const coll      = mongo.db.collection(enricher.collection);
      const failedKey = `enriched.${enricher.name}_failed`;
      const doneKey   = `enriched.${enricher.name}`;
      const docs = await coll
        .find({ [doneKey]: { $exists: false }, [failedKey]: { $exists: false } })
        .limit(100)
        .toArray()
        .catch((err: Error) => { console.error('clued enricher query failed:', err.message); return []; });

      for (const doc of docs) {
        const d = doc as Record<string, unknown>;
        if (!enricher.matches(d)) continue;
        try {
          const result = await enricher.enrich(d);
          await coll.updateOne({ _id: doc._id }, { $set: { [doneKey]: result } });
        } catch (err) {
          const e = err as Error;
          console.error(`clued enricher "${enricher.name}" failed on ${doc._id}:`, e.message);
          await coll.updateOne({ _id: doc._id }, {
            $set: { [failedKey]: { message: e.message, at: new Date() } },
          });
        }
      }
    }
  }, 5000);

  return { stop: () => clearInterval(timer) };
}
