import { readdir, readFile } from 'fs/promises';
import { join, basename } from 'path';
import { fileURLToPath } from 'url';
import { createClient } from './mongo';
import { loadConfig }   from './config';
import { getGitOrigin } from './git';
import type { MongoDb } from './mongo';
import type { Config }  from './config';

// Hyphens are ambiguous with path separators, so project_path is best-effort metadata only.
export function decodeProjectPath(dirName: string): string {
  return '/' + dirName.slice(1).replaceAll('-', '/');
}

async function processSession(
  mongo: MongoDb,
  projectPath: string,
  sessionId: string,
  filePath: string,
): Promise<number> {
  const now = new Date();

  const git_origin = await getGitOrigin(projectPath);

  const $set: Record<string, unknown> = { session_id: sessionId, project_path: projectPath, transcript_path: filePath, last_seen: now };
  if (git_origin) $set.git_origin = git_origin;

  await mongo.sessions.updateOne(
    { session_id: sessionId },
    { $set, $setOnInsert: { started_at: now } },
    { upsert: true }
  );

  const content  = await readFile(filePath, 'utf8');
  const rawLines = content.split('\n').filter(l => l.trim());
  if (rawLines.length === 0) return 0;

  const ops = rawLines.map((raw, seq) => {
    let line: unknown;
    try { line = JSON.parse(raw); } catch { line = { raw }; }
    return {
      updateOne: {
        filter: { session_id: sessionId, seq },
        update: { $set: { session_id: sessionId, seq, line }, $setOnInsert: { created_at: now } },
        upsert: true,
      },
    };
  });

  await mongo.transcriptLines.bulkWrite(ops, { ordered: false });
  return rawLines.length;
}

export async function backfill(config: Pick<Config, 'projectsDir'>, mongo: MongoDb): Promise<void> {
  const dirs = await readdir(config.projectsDir, { withFileTypes: true }).catch(() => []);
  const sessions: Array<{ projectPath: string; sessionId: string; filePath: string }> = [];

  for (const dir of dirs.filter(d => d.isDirectory())) {
    const projectPath = decodeProjectPath(dir.name);
    const dirPath     = join(config.projectsDir, dir.name);
    const files       = await readdir(dirPath).catch(() => []);
    for (const file of files.filter(f => f.endsWith('.jsonl'))) {
      sessions.push({
        projectPath,
        sessionId: basename(file, '.jsonl'),
        filePath:  join(dirPath, file),
      });
    }
  }

  let totalLines = 0;
  for (let i = 0; i < sessions.length; i += 5) {
    const batch   = sessions.slice(i, i + 5);
    const results = await Promise.allSettled(
      batch.map(s => processSession(mongo, s.projectPath, s.sessionId, s.filePath))
    );
    for (let j = 0; j < results.length; j++) {
      if (results[j].status === 'fulfilled')  totalLines += (results[j] as PromiseFulfilledResult<number>).value;
      else console.error(`clued backfill error (${batch[j].filePath}):`, (results[j] as PromiseRejectedResult).reason?.message);
    }
  }

  console.log(`clued backfill: ${sessions.length} sessions, ${totalLines} lines upserted`);
}

// Standalone entry point
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const config = loadConfig();
  const mongo  = await createClient(config);
  try {
    await backfill(config, mongo);
  } finally {
    await mongo.close();
  }
}
