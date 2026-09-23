import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync, rmSync } from 'fs';
import { join, dirname } from 'path';
import { tmpdir } from 'os';
import { fileURLToPath } from 'url';
import { execFileSync } from 'child_process';
import { createClient } from '../../src/mongo';
import { backfill, decodeProjectPath } from '../../src/backfill';
import type { MongoDb } from '../../src/mongo';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '../..');
const TMP = join(tmpdir(), `clued-backfill-test-${process.pid}`);
const TEST_CONFIG = {
  mongoUrl:    process.env.CLUED_MONGO_URL || 'mongodb://localhost:27018',
  dbName:      `clued_bf_test_${Date.now()}`,
  projectsDir: TMP,
};

let mongo: MongoDb;
after(async () => {
  if (mongo) { await mongo.db.dropDatabase(); await mongo.close(); }
  rmSync(TMP, { recursive: true, force: true });
});

test('decodeProjectPath converts encoded dir name to absolute path', () => {
  assert.equal(decodeProjectPath('-Users-alice-Projects-myapp'), '/Users/alice/Projects/myapp');
});

test('decodeProjectPath best-effort on paths with hyphens in components', () => {
  const decoded = decodeProjectPath('-Users-rob-byrd-Projects-foo');
  assert.ok(decoded.startsWith('/'), 'decoded path must be absolute');
});

test('upserts sessions and transcript lines from project directories', async () => {
  const projDir   = join(TMP, '-Users-test-myproject');
  const sessionId = 'aaaabbbb-cccc-dddd-eeee-ffffffffffff';
  mkdirSync(projDir, { recursive: true });
  writeFileSync(join(projDir, `${sessionId}.jsonl`), [
    JSON.stringify({ type: 'human', text: 'hello' }),
    JSON.stringify({ type: 'assistant', text: 'world' }),
  ].join('\n') + '\n');

  mongo = await createClient(TEST_CONFIG);
  await backfill(TEST_CONFIG, mongo);

  const session = await mongo.sessions.findOne({ session_id: sessionId });
  assert.ok(session, 'session doc not found');
  assert.equal((session as Record<string, unknown>).project_path, '/Users/test/myproject');

  const lines = await mongo.transcriptLines.find({ session_id: sessionId }).sort({ seq: 1 }).toArray();
  assert.equal(lines.length, 2);
  assert.equal((lines[0] as unknown as { line: Record<string, unknown> }).line.type, 'human');
  assert.equal((lines[1] as unknown as { line: Record<string, unknown> }).line.type, 'assistant');
});

test('is idempotent — re-running does not duplicate lines', async () => {
  await backfill(TEST_CONFIG, mongo);
  const count = await mongo.transcriptLines.countDocuments({ session_id: 'aaaabbbb-cccc-dddd-eeee-ffffffffffff' });
  assert.equal(count, 2);
});

test('handles empty project directory gracefully', async () => {
  const emptyDir = join(TMP, '-Users-test-empty');
  mkdirSync(emptyDir, { recursive: true });
  await assert.doesNotReject(() => backfill(TEST_CONFIG, mongo));
});

test('backfill sets git_origin when projectPath is a git repo', async () => {
  const gitRepoPath = `/private/tmp/claude/gitrepotest${process.pid}`;
  const dirName     = gitRepoPath.replaceAll('/', '-');
  const projectsDir = join(tmpdir(), `clued-bf-gitprojects-${process.pid}`);
  const projDir     = join(projectsDir, dirName);
  const sessionId   = 'bbbbcccc-dddd-eeee-ffff-000000000000';

  mkdirSync(projDir,     { recursive: true });
  mkdirSync(gitRepoPath, { recursive: true });
  writeFileSync(join(projDir, `${sessionId}.jsonl`),
    JSON.stringify({ type: 'human', text: 'hi' }) + '\n');

  const fakeOrigin = 'https://github.com/test/fake-repo.git';
  execFileSync('git', ['init', '--template=', gitRepoPath], { stdio: 'ignore' });
  execFileSync('git', ['-C', gitRepoPath, 'remote', 'add', 'origin', fakeOrigin], { stdio: 'ignore' });

  const gitConfig = { ...TEST_CONFIG, dbName: `clued_bf_git_test_${Date.now()}`, projectsDir };
  const gitMongo  = await createClient(gitConfig);
  try {
    await backfill(gitConfig, gitMongo);
    const session = await gitMongo.sessions.findOne({ session_id: sessionId });
    assert.ok(session, 'session not found');
    assert.equal((session as Record<string, unknown>).git_origin, fakeOrigin);
  } finally {
    await gitMongo.db.dropDatabase();
    await gitMongo.close();
    rmSync(projectsDir, { recursive: true, force: true });
    rmSync(gitRepoPath, { recursive: true, force: true });
  }
});
