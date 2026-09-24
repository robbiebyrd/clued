import { test } from 'node:test';
import assert from 'node:assert/strict';
import { extToLang } from '../enrichers/lang.js';

test('returns null for null input', () => {
  assert.equal(extToLang(null), null);
});

test('returns null for no-extension path', () => {
  assert.equal(extToLang('Makefile'), null);
});

test('maps .ts to typescript', () => {
  assert.equal(extToLang('src/foo.ts'), 'typescript');
});

test('maps .tsx to typescript', () => {
  assert.equal(extToLang('components/Button.tsx'), 'typescript');
});

test('maps .py to python', () => {
  assert.equal(extToLang('script.py'), 'python');
});

test('maps .json to json', () => {
  assert.equal(extToLang('package.json'), 'json');
});

test('maps .yaml to yaml', () => {
  assert.equal(extToLang('config.yaml'), 'yaml');
});

test('maps .yml to yaml', () => {
  assert.equal(extToLang('.github/ci.yml'), 'yaml');
});

test('returns null for unknown extension', () => {
  assert.equal(extToLang('archive.zip'), null);
});
