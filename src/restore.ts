import { mkdirSync, createWriteStream, writeFileSync } from 'fs';
import { join, basename, dirname } from 'path';
import type { MongoDb } from './mongo';

export interface RestoreSessionArgs {
  session_id:    string;
  project_path?: string;
  projects_dir?: string;
}

export interface RestoreResult {
  files_written: number;
  bytes_written: number;
  missing:       string[];
}

export async function restoreSession(
  { session_id, project_path, projects_dir }: RestoreSessionArgs,
  account_id: string,
  mongo: MongoDb,
  fileHistoryDir: string,
): Promise<RestoreResult> {
  const session = await mongo.sessions.findOne({ session_id, account_id });
  if (!session) throw new Error(`session not found: ${session_id}`);

  // Derive project directory name — prefer transcript_path (already encoded, no re-encoding needed),
  // then an explicit project_path override, then fall back to encoding the stored project_path.
  let projDirName: string;
  if (project_path) {
    projDirName = '-' + project_path.replace(/^\//, '').replaceAll('/', '-');
  } else if (session.transcript_path) {
    projDirName = basename(dirname(session.transcript_path as string));
  } else if (session.project_path) {
    projDirName = '-' + (session.project_path as string).replace(/^\//, '').replaceAll('/', '-');
  } else {
    throw new Error(`session ${session_id} has no path information to derive target directory`);
  }

  const targetProjDir  = join(projects_dir ?? '', projDirName);
  const sessionDir     = join(targetProjDir, session_id);
  const subagentsDir   = join(sessionDir, 'subagents');
  const toolResultsDir = join(sessionDir, 'tool-results');
  const sessionFhDir   = join(fileHistoryDir, session_id);

  mkdirSync(targetProjDir,  { recursive: true });
  mkdirSync(subagentsDir,   { recursive: true });
  mkdirSync(toolResultsDir, { recursive: true });
  mkdirSync(sessionFhDir,   { recursive: true });

  let files_written = 0;
  let bytes_written = 0;
  const missing: string[] = [];

  // 1. Main transcript JSONL
  const total = await mongo.transcriptLines.countDocuments({ session_id, account_id });
  if (total === 0) {
    missing.push('transcript_lines');
  } else {
    const jsonlPath = join(targetProjDir, `${session_id}.jsonl`);
    const ws = createWriteStream(jsonlPath, { flags: 'w' });
    const BATCH = 500;
    for (let skip = 0; skip < total; skip += BATCH) {
      const lines = await mongo.transcriptLines
        .find({ session_id, account_id }, { projection: { _id: 0, line: 1 } })
        .sort({ seq: 1 }).skip(skip).limit(BATCH).toArray();
      for (const doc of lines) {
        const row = JSON.stringify(doc.line) + '\n';
        ws.write(row);
        bytes_written += Buffer.byteLength(row);
      }
    }
    await new Promise<void>((resolve, reject) => { ws.end((err: Error | null | undefined) => err ? reject(err) : resolve()); });
    files_written++;
  }

  // 2. Subagent JSONLs
  const subagentIds = await mongo.subagentLines.distinct('subagent_id', { session_id, account_id }) as string[];
  for (const subagent_id of subagentIds) {
    const lines = await mongo.subagentLines
      .find({ session_id, subagent_id, account_id }, { projection: { _id: 0, line: 1 } })
      .sort({ seq: 1 }).toArray();
    const content = lines.map(d => JSON.stringify(d.line)).join('\n') + '\n';
    writeFileSync(join(subagentsDir, `${subagent_id}.jsonl`), content);
    bytes_written += Buffer.byteLength(content);
    files_written++;
  }

  // 3. Blobs (subagent-meta, tool-result, file-history)
  const blobs = await mongo.blobs.find({ session_id, account_id }).toArray();
  const seenBlobTypes = new Set<string>();

  for (const blobDoc of blobs) {
    const b        = blobDoc as Record<string, unknown>;
    const blobType = b.blob_type as string;
    const name     = b.name     as string;
    const content  = b.content  as string;
    const encoding = b.encoding as string;
    seenBlobTypes.add(blobType);

    let outPath: string;
    if      (blobType === 'subagent-meta')  outPath = join(subagentsDir,   name);
    else if (blobType === 'tool-result')    outPath = join(toolResultsDir, name);
    else if (blobType === 'file-history')   outPath = join(sessionFhDir,   name);
    else continue;

    const buf = encoding === 'base64' ? Buffer.from(content, 'base64') : Buffer.from(content, 'utf8');
    writeFileSync(outPath, buf);
    bytes_written += buf.length;
    files_written++;
  }

  for (const expected of ['subagent-meta', 'tool-result', 'file-history']) {
    if (!seenBlobTypes.has(expected)) missing.push(expected);
  }

  return { files_written, bytes_written, missing };
}
