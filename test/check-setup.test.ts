import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'fs';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';
import { tmpdir } from 'os';
import { spawnSync } from 'child_process';

const ROOT        = join(dirname(fileURLToPath(import.meta.url)), '..');
const CHECK_SETUP = join(ROOT, 'hooks/check-setup');
const INSTALL_CMD = 'go install github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest';

function run(env: Record<string, string>) {
  return spawnSync('/bin/bash', [CHECK_SETUP], { env: { ...process.env, ...env }, encoding: 'utf8' });
}
function homeWithConfig(): string {
  const home = mkdtempSync(join(tmpdir(), 'clued-check-setup-'));
  const dir = join(home, '.claude/plugins/data/clued');
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'config.json'), '{}');
  return home;
}
function pathWithStubBinary(): string {
  const bin = mkdtempSync(join(tmpdir(), 'clued-bin-'));
  writeFileSync(join(bin, 'mind-palace'), '#!/bin/sh\n', { mode: 0o755 });
  return bin;
}

test('check-setup tells the user to install mind-palace when it is not on PATH', () => {
  const home = homeWithConfig();
  const r = run({ HOME: home, PATH: '/usr/bin:/bin' });
  rmSync(home, { recursive: true, force: true });
  const msg = (JSON.parse(r.stdout) as { systemMessage: string }).systemMessage;
  assert.ok(msg.includes(INSTALL_CMD), `install command missing from: ${msg}`);
  assert.ok(!msg.includes('/clued-setup'), 'config is present, so no setup nag expected');
});

test('check-setup is silent when config exists and mind-palace is on PATH', () => {
  const home = homeWithConfig();
  const bin = pathWithStubBinary();
  const r = run({ HOME: home, PATH: `${bin}:/usr/bin:/bin` });
  rmSync(home, { recursive: true, force: true });
  rmSync(bin, { recursive: true, force: true });
  assert.equal(r.stdout, '');
});

test('check-setup reports both problems in one JSON message', () => {
  const home = mkdtempSync(join(tmpdir(), 'clued-check-setup-'));
  const r = run({ HOME: home, PATH: '/usr/bin:/bin' });
  rmSync(home, { recursive: true, force: true });
  const msg = (JSON.parse(r.stdout) as { systemMessage: string }).systemMessage;
  assert.ok(msg.includes('/clued-setup'));
  assert.ok(msg.includes(INSTALL_CMD));
});
