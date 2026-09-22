import { readdir } from 'fs/promises';
import { join } from 'path';
import { pathToFileURL } from 'url';

export async function loadEnrichers(enrichersDir, config) {
  let files;
  try {
    files = await readdir(enrichersDir);
  } catch {
    return [];
  }

  const enrichers = [];
  for (const file of files.filter(f => f.endsWith('.mjs'))) {
    const mod = await import(pathToFileURL(join(enrichersDir, file)).href);
    if (mod.enabled === false) continue;
    if ((config.disabledEnrichers ?? []).includes(mod.name)) continue;
    enrichers.push(mod);
  }
  return enrichers;
}

export function startEnrichmentLoop(mongo, enrichers) {
  const timer = setInterval(async () => {
    for (const enricher of enrichers) {
      const coll = mongo.db.collection(enricher.collection);
      const failedKey = `enriched.${enricher.name}_failed`;
      const doneKey   = `enriched.${enricher.name}`;
      const docs = await coll
        .find({ [doneKey]: { $exists: false }, [failedKey]: { $exists: false } })
        .limit(100)
        .toArray()
        .catch(() => []);

      for (const doc of docs) {
        if (!enricher.matches(doc)) continue;
        try {
          const result = await enricher.enrich(doc);
          await coll.updateOne({ _id: doc._id }, { $set: { [doneKey]: result } });
        } catch (err) {
          console.error(`clued enricher "${enricher.name}" failed on ${doc._id}:`, err.message);
          await coll.updateOne({ _id: doc._id }, {
            $set: { [failedKey]: { message: err.message, at: new Date() } },
          });
        }
      }
    }
  }, 5000);

  return { stop: () => clearInterval(timer) };
}
