import fs from 'fs';
import { join } from 'path';
import type { MongoDb } from './mongo';
import { tailFile } from './tailer';

// Calls onChange(filename, fullPath) for every file in dirPath whenever a new
// file appears or an existing file's mtime changes. Retries on 500ms interval
// until the directory exists, then uses fs.watch + 2s poll as a safety net
// (required on macOS/kqueue where fs.watch on a directory does not reliably
// fire for newly created files inside it).
function watchDir(
  dirPath: string,
  onChange: (filename: string, fullPath: string) => void,
): void {
  const mtimes = new Map<string, number>();

  const check = () => {
    let entries: fs.Dirent[];
    try { entries = fs.readdirSync(dirPath, { withFileTypes: true }); }
    catch { return; }
    for (const entry of entries) {
      if (!entry.isFile()) continue;
      let mtime: number;
      try { mtime = fs.statSync(join(dirPath, entry.name)).mtimeMs; }
      catch { continue; }
      if (mtimes.get(entry.name) !== mtime) {
        mtimes.set(entry.name, mtime);
        onChange(entry.name, join(dirPath, entry.name));
      }
    }
  };

  let watching = false;
  const tryWatch = () => {
    if (watching || !fs.existsSync(dirPath)) return;
    watching = true;
    try {
      const w = fs.watch(dirPath, () => check());
      w.on('error', () => {}); // poll fallback covers any watch errors
    } catch { /* poll-only fallback */ }
    check();
  };

  const readyInterval = setInterval(() => {
    if (!fs.existsSync(dirPath)) return;
    clearInterval(readyInterval);
    tryWatch();
  }, 500);
  tryWatch();

  setInterval(check, 2000);
}

export function watchArtifactDirs(
  session_id: string,
  sessionDir: string,
  fileHistoryPath: string,
  mongo: MongoDb,
  account_id: string,
  host: unknown,
): void {
  const subagentsDir   = join(sessionDir, 'subagents');
  const toolResultsDir = join(sessionDir, 'tool-results');

  // Subagents dir: tail JSONL files, read meta.json
  const subagentSeqs = new Map<string, { value: number }>();
  watchDir(subagentsDir, (filename, fullPath) => {
    if (filename.endsWith('.jsonl')) {
      const subagent_id = filename.replace(/\.jsonl$/, '');
      // Guard: only start one tailer per subagent. watchDir fires onChange again
      // when mtime changes; a second tailer would reset seq to 0 and violate the
      // unique index.
      if (subagentSeqs.has(subagent_id)) return;
      const seqRef = { value: 0 };
      subagentSeqs.set(subagent_id, seqRef);
      tailFile(fullPath, raw => {
        let line: unknown;
        try { line = JSON.parse(raw); } catch { line = { raw }; }
        const seq = seqRef.value++;
        mongo.subagentLines.updateOne(
          { session_id, subagent_id, seq },
          { $set: { session_id, subagent_id, seq, line, account_id, host },
            $setOnInsert: { created_at: new Date() } },
          { upsert: true }
        ).catch(() => {});
      });
    } else if (filename.endsWith('.meta.json')) {
      let content: string;
      try { content = fs.readFileSync(fullPath, 'utf8'); } catch { return; }
      mongo.blobs.updateOne(
        { session_id, blob_type: 'subagent-meta', name: filename },
        { $set: { content, encoding: 'utf8', account_id },
          $setOnInsert: { created_at: new Date() } },
        { upsert: true }
      ).catch(() => {});
    }
  });

  // Tool-results dir: read blobs as utf8
  watchDir(toolResultsDir, (filename, fullPath) => {
    let content: string;
    try { content = fs.readFileSync(fullPath, 'utf8'); } catch { return; }
    mongo.blobs.updateOne(
      { session_id, blob_type: 'tool-result', name: filename },
      { $set: { content, encoding: 'utf8', account_id },
        $setOnInsert: { created_at: new Date() } },
      { upsert: true }
    ).catch(() => {});
  });

  // File-history dir: read blobs as base64
  watchDir(fileHistoryPath, (filename, fullPath) => {
    let buf: Buffer;
    try { buf = fs.readFileSync(fullPath); } catch { return; }
    mongo.blobs.updateOne(
      { session_id, blob_type: 'file-history', name: filename },
      { $set: { content: buf.toString('base64'), encoding: 'base64', account_id },
        $setOnInsert: { created_at: new Date() } },
      { upsert: true }
    ).catch(() => {});
  });
}
