import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readHostInfo } from '../src/host';

test('returns a non-empty hostname string', () => {
  const r = readHostInfo();
  assert.equal(typeof r.hostname, 'string');
  assert.ok(r.hostname.length > 0);
});

test('ip is a string or null', () => {
  const r = readHostInfo();
  assert.ok(r.ip === null || typeof r.ip === 'string');
});

test('mac is a string or null', () => {
  const r = readHostInfo();
  assert.ok(r.mac === null || typeof r.mac === 'string');
});

test('ip, when present, looks like an IPv4 address', () => {
  const r = readHostInfo();
  if (r.ip !== null) {
    assert.match(r.ip, /^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$/);
  }
});

test('mac, when present, looks like a MAC address', () => {
  const r = readHostInfo();
  if (r.mac !== null && r.mac !== '00:00:00:00:00:00') {
    assert.match(r.mac, /^([0-9a-f]{2}:){5}[0-9a-f]{2}$/i);
  }
});
