import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'fs';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');
type Hook  = { type: string; command: string; async?: boolean };
type Group = { matcher?: string; hooks: Hook[] };
const hooks = (JSON.parse(readFileSync(join(ROOT, 'hooks.json'), 'utf8')) as { hooks: Record<string, Group[]> }).hooks;

test('hooks.json registers the plan guard as a blocking Write|Edit PreToolUse hook', () => {
  const guard = hooks.PreToolUse.find(g => g.hooks.some(h => h.command.endsWith('/hooks/guard-superpowers-plans')));
  assert.ok(guard, 'guard group missing from PreToolUse');
  assert.equal(guard!.matcher, 'Write|Edit');
  assert.equal(guard!.hooks.length, 1);
  assert.equal(guard!.hooks[0].type, 'command');
  assert.equal(guard!.hooks[0].command, '${CLAUDE_PLUGIN_ROOT}/hooks/guard-superpowers-plans');
  assert.equal(guard!.hooks[0].async, undefined, 'a blocking hook must not be async');
});
