import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { writeFileSync, mkdirSync, rmSync } from 'fs';
import { join } from 'path';
import { tmpdir, homedir } from 'os';
import { loadConfig } from '../src/config';

const TMP = join(tmpdir(), `clued-test-config-${process.pid}`);
mkdirSync(TMP, { recursive: true });

after(() => rmSync(TMP, { recursive: true, force: true }));

function withEnv(vars: Record<string, string | undefined>, fn: () => void): void {
  const saved: Record<string, string | undefined> = {};
  for (const [k, v] of Object.entries(vars)) {
    saved[k] = process.env[k];
    if (v === undefined) delete process.env[k];
    else process.env[k] = v;
  }
  try { fn(); }
  finally {
    for (const [k, v] of Object.entries(saved)) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
  }
}

const ENV_KEYS = ['CLUED_MONGO_URL', 'CLUED_DB_NAME', 'CLUED_PORT', 'CLUED_MCP_PORT', 'CLUED_PROJECTS_DIR', 'CLUED_CLAUDE_APP_CONFIG_PATH'];
function cleanEnv(fn: () => void): void {
  withEnv(Object.fromEntries(ENV_KEYS.map(k => [k, undefined])), fn);
}

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

test('claudeAppConfigPath has a default value containing Claude', () => {
  cleanEnv(() => {
    const cfg = loadConfig(join(TMP, 'nonexistent.json'));
    assert.ok(cfg.claudeAppConfigPath.includes('Claude'), `expected path to include 'Claude', got ${cfg.claudeAppConfigPath}`);
    assert.ok(cfg.claudeAppConfigPath.endsWith('config.json'));
  });
});

test('CLUED_CLAUDE_APP_CONFIG_PATH env var overrides claudeAppConfigPath', () => {
  withEnv({ CLUED_CLAUDE_APP_CONFIG_PATH: '/custom/path/config.json', CLUED_MONGO_URL: undefined,
            CLUED_DB_NAME: undefined, CLUED_PORT: undefined, CLUED_PROJECTS_DIR: undefined, CLUED_MCP_PORT: undefined }, () => {
    const cfg = loadConfig(join(TMP, 'nonexistent.json'));
    assert.equal(cfg.claudeAppConfigPath, '/custom/path/config.json');
  });
});

test('walPath is co-located with the config file', () => {
  const cfgPath = join(TMP, 'subdir', 'config.json');
  cleanEnv(() => {
    const cfg = loadConfig(cfgPath);
    assert.equal(cfg.walPath, join(TMP, 'subdir', 'events.wal'));
  });
});

test('claudeAppConfigPath expands ~ to home directory', () => {
  const cfgPath = join(TMP, 'config-app-tilde.json');
  writeFileSync(cfgPath, JSON.stringify({ claudeAppConfigPath: '~/Library/Application Support/Claude/config.json' }));
  cleanEnv(() => {
    const cfg = loadConfig(cfgPath);
    assert.ok(cfg.claudeAppConfigPath.startsWith(homedir()), `expected ${cfg.claudeAppConfigPath} to start with ${homedir()}`);
    assert.ok(!cfg.claudeAppConfigPath.includes('~'));
  });
});
