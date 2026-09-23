import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { writeFileSync, mkdirSync, rmSync } from 'fs';
import { join } from 'path';
import { tmpdir, homedir } from 'os';

const TMP = join(tmpdir(), `clued-test-config-${process.pid}`);
mkdirSync(TMP, { recursive: true });

after(() => rmSync(TMP, { recursive: true, force: true }));

function withEnv(vars, fn) {
  const saved = {};
  for (const [k, v] of Object.entries(vars)) {
    saved[k] = process.env[k];
    if (v === undefined) delete process.env[k];
    else process.env[k] = v;
  }
  try { return fn(); }
  finally {
    for (const [k, v] of Object.entries(saved)) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
  }
}

const ENV_KEYS = ['CLUED_MONGO_URL', 'CLUED_DB_NAME', 'CLUED_PORT', 'CLUED_MCP_PORT', 'CLUED_PROJECTS_DIR'];
function cleanEnv(fn) {
  return withEnv(Object.fromEntries(ENV_KEYS.map(k => [k, undefined])), fn);
}

const { loadConfig } = await import('../src/config.mjs');

test('returns defaults when no config file and no env vars', () => {
  cleanEnv(() => {
    const cfg = loadConfig(join(TMP, 'nonexistent.json'));
    assert.equal(cfg.port, 8085);
    assert.equal(cfg.dbName, 'claude_sessions');
    assert.match(cfg.mongoUrl, /^mongodb:\/\//);
    assert.equal(Array.isArray(cfg.disabledEnrichers), true);
  });
});

test('config file values override defaults', () => {
  const cfgPath = join(TMP, 'config-file.json');
  writeFileSync(cfgPath, JSON.stringify({ port: 9999, dbName: 'my_db' }));
  cleanEnv(() => {
    const cfg = loadConfig(cfgPath);
    assert.equal(cfg.port, 9999);
    assert.equal(cfg.dbName, 'my_db');
    assert.equal(cfg.mongoUrl, 'mongodb://localhost:27018'); // default preserved
  });
});

test('env vars override config file', () => {
  const cfgPath = join(TMP, 'config-env.json');
  writeFileSync(cfgPath, JSON.stringify({ port: 9999 }));
  withEnv({ CLUED_PORT: '7777', CLUED_DB_NAME: 'env_db', CLUED_MONGO_URL: undefined, CLUED_PROJECTS_DIR: undefined }, () => {
    const cfg = loadConfig(cfgPath);
    assert.equal(cfg.port, 7777);
    assert.equal(cfg.dbName, 'env_db');
  });
});

test('projectsDir expands ~ to home directory', () => {
  const cfgPath = join(TMP, 'config-tilde.json');
  writeFileSync(cfgPath, JSON.stringify({ projectsDir: '~/.claude/projects' }));
  cleanEnv(() => {
    const cfg = loadConfig(cfgPath);
    assert.ok(cfg.projectsDir.startsWith(homedir()), `expected ${cfg.projectsDir} to start with ${homedir()}`);
    assert.ok(!cfg.projectsDir.includes('~'));
  });
});

test('mongoUrl expands ~ to home directory', () => {
  const cfgPath = join(TMP, 'config-mongo-tilde.json');
  writeFileSync(cfgPath, JSON.stringify({ mongoUrl: '~/mongo/local.db' }));
  cleanEnv(() => {
    const cfg = loadConfig(cfgPath);
    assert.ok(cfg.mongoUrl.startsWith(homedir()), `expected ${cfg.mongoUrl} to start with ${homedir()}`);
    assert.ok(!cfg.mongoUrl.includes('~'));
  });
});

test('mcpPort defaults to 8086', () => {
  cleanEnv(() => {
    const cfg = loadConfig(join(TMP, 'nonexistent.json'));
    assert.equal(cfg.mcpPort, 8086);
  });
});

test('CLUED_MCP_PORT env var overrides mcpPort', () => {
  withEnv({ CLUED_MCP_PORT: '9086', CLUED_MONGO_URL: undefined, CLUED_DB_NAME: undefined,
            CLUED_PORT: undefined, CLUED_PROJECTS_DIR: undefined }, () => {
    const cfg = loadConfig(join(TMP, 'nonexistent.json'));
    assert.equal(cfg.mcpPort, 9086);
  });
});
