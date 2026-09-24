import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/attachment-extractor.js';

test('name and collection', () => {
  assert.equal(name, 'attachment-extractor');
  assert.equal(collection, 'transcript_lines');
});

test('matches attachment line type only', () => {
  assert.equal(matches({ line: { type: 'attachment' } }), true);
  assert.equal(matches({ line: { type: 'assistant' } }), false);
  assert.equal(matches({ line: { type: 'user' } }), false);
  assert.equal(matches({}), false);
});

test('extracts file_path and language from attachment', async () => {
  const r = await enrich({
    line: {
      type: 'attachment',
      attachment: { file_path: 'src/util.ts', content: 'export const x = 1;' },
    },
  });
  assert.equal(r.file_path, 'src/util.ts');
  assert.equal(r.language, 'typescript');
  assert.equal(r.size_chars, 19);
});

test('language is null for unknown extension', async () => {
  const r = await enrich({
    line: {
      type: 'attachment',
      attachment: { file_path: 'data.parquet', content: 'binary' },
    },
  });
  assert.equal(r.language, null);
});

test('file_path is null when absent', async () => {
  const r = await enrich({
    line: { type: 'attachment', attachment: { content: 'some text' } },
  });
  assert.equal(r.file_path, null);
  assert.equal(r.size_chars, 9);
});

test('size_chars is null when content is absent', async () => {
  const r = await enrich({
    line: { type: 'attachment', attachment: { file_path: 'foo.ts' } },
  });
  assert.equal(r.size_chars, null);
});

test('all nulls for empty attachment', async () => {
  const r = await enrich({ line: { type: 'attachment', attachment: {} } });
  assert.deepEqual(r, { file_path: null, language: null, size_chars: null });
});
