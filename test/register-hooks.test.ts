import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, rmSync, writeFileSync, readFileSync } from 'fs';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';
import { tmpdir } from 'os';
import { execFileSync } from 'child_process';

const ROOT            = join(dirname(fileURLToPath(import.meta.url)), '..');
const REGISTER_HOOKS  = join(ROOT, 'hooks/register-hooks');
const HOOKS_DIR       = join(ROOT, 'hooks');
const TMP_DIR         = join(tmpdir(), `clued-reg-hooks-test-${process.pid}`);
const SETTINGS_PATH   = join(TMP_DIR, 'settings.json');

function run(extraEnv: Record<string, string> = {}) {
  execFileSync('bash', [REGISTER_HOOKS], {
    env: { ...process.env, CLAUDE_CONFIG_DIR: TMP_DIR, ...extraEnv },
    stdio: 'pipe',
  });
  return JSON.parse(readFileSync(SETTINGS_PATH, 'utf8')) as Record<string, unknown>;
}

before(() => mkdirSync(TMP_DIR, { recursive: true }));
after(() => rmSync(TMP_DIR, { recursive: true, force: true }));

test('register-hooks creates hooks key in empty settings.json', () => {
  writeFileSync(SETTINGS_PATH, '{}');
  const settings = run();
  assert.ok(settings.hooks, 'hooks key missing');
});

test('register-hooks writes all required event types', () => {
  writeFileSync(SETTINGS_PATH, '{}');
  const settings = run();
  const hooks = settings.hooks as Record<string, unknown>;
  for (const ev of ['SessionStart', 'PreToolUse', 'PostToolUse', 'UserPromptSubmit', 'Stop', 'SessionEnd', 'Notification', 'SubagentStop']) {
    assert.ok(hooks[ev], `${ev} missing`);
  }
});

test('register-hooks points to correct absolute paths', () => {
  writeFileSync(SETTINGS_PATH, '{}');
  const settings = run();
  const hooks = settings.hooks as Record<string, { hooks: { command: string }[] }[]>;

  const relayCmd = hooks['PreToolUse'][0].hooks[0].command;
  assert.equal(relayCmd, join(HOOKS_DIR, 'event-relay'));

  const sessionCmds = hooks['SessionStart'][0].hooks.map(h => h.command);
  assert.ok(sessionCmds.some(c => c.endsWith('check-setup')), 'check-setup missing from SessionStart');
  assert.ok(sessionCmds.some(c => c.endsWith('session-start')), 'session-start missing from SessionStart');
});

test('register-hooks is idempotent — no duplicate entries', () => {
  writeFileSync(SETTINGS_PATH, '{}');
  run();
  const settings = run(); // second run
  const hooks = settings.hooks as Record<string, unknown[]>;
  for (const ev of ['PreToolUse', 'PostToolUse', 'SessionStart']) {
    assert.equal((hooks[ev] as unknown[]).length, 1, `${ev} has duplicate entries after second run`);
  }
});

test('register-hooks replaces stale clued entries with updated paths', () => {
  const stale = {
    hooks: {
      PreToolUse: [{ hooks: [{ type: 'command', command: '/old/path/hooks/event-relay', async: true }] }],
    },
  };
  writeFileSync(SETTINGS_PATH, JSON.stringify(stale));
  const settings = run();
  const hooks = settings.hooks as Record<string, { hooks: { command: string }[] }[]>;
  assert.equal(hooks['PreToolUse'].length, 1);
  assert.equal(hooks['PreToolUse'][0].hooks[0].command, join(HOOKS_DIR, 'event-relay'));
});

test('register-hooks preserves non-clued hooks in the same event type', () => {
  const existing = {
    hooks: {
      PreToolUse: [{ hooks: [{ type: 'command', command: '/other/plugin/hook' }] }],
    },
  };
  writeFileSync(SETTINGS_PATH, JSON.stringify(existing));
  const settings = run();
  const hooks = settings.hooks as Record<string, { hooks: { command: string }[] }[]>;
  assert.equal(hooks['PreToolUse'].length, 2, 'non-clued entry should be preserved');
  const commands = hooks['PreToolUse'].map(e => e.hooks[0].command);
  assert.ok(commands.includes('/other/plugin/hook'), 'other plugin hook missing');
  assert.ok(commands.includes(join(HOOKS_DIR, 'event-relay')), 'clued relay missing');
});

test('register-hooks preserves unrelated settings keys', () => {
  writeFileSync(SETTINGS_PATH, JSON.stringify({ model: 'sonnet', spinnerTipsEnabled: false }));
  const settings = run();
  assert.equal((settings as Record<string, unknown>).model, 'sonnet');
  assert.equal((settings as Record<string, unknown>).spinnerTipsEnabled, false);
});

test('register-hooks succeeds when settings.json does not exist', () => {
  rmSync(SETTINGS_PATH, { force: true });
  const settings = run();
  assert.ok(settings.hooks, 'hooks key missing when starting from scratch');
});
