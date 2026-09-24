import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/file-snapshot-extractor.js';

test('name and collection', () => {
  assert.equal(name, 'file-snapshot-extractor');
  assert.equal(collection, 'transcript_lines');
});

test('matches file-history-snapshot with non-empty trackedFileBackups', () => {
  assert.equal(matches({
    line: {
      type: 'file-history-snapshot',
      snapshot: { trackedFileBackups: { 'src/a.ts': {} } },
    },
  }), true);
});

test('does not match when trackedFileBackups is empty', () => {
  assert.equal(matches({
    line: {
      type: 'file-history-snapshot',
      snapshot: { trackedFileBackups: {} },
    },
  }), false);
});

test('does not match wrong line type', () => {
  assert.equal(matches({ line: { type: 'assistant' } }), false);
  assert.equal(matches({ line: { type: 'user' } }), false);
  assert.equal(matches({}), false);
});

test('does not match when snapshot is missing', () => {
  assert.equal(matches({ line: { type: 'file-history-snapshot' } }), false);
});

test('extracts files from trackedFileBackups keys', async () => {
  const r = await enrich({
    line: {
      type: 'file-history-snapshot',
      snapshot: {
        trackedFileBackups: {
          'src/app.ts': { content: '...' },
          'src/util.ts': { content: '...' },
        },
      },
    },
  });
  assert.equal(r.file_count, 2);
  assert.ok(r.files.includes('src/app.ts'));
  assert.ok(r.files.includes('src/util.ts'));
});

test('detects languages from file extensions', async () => {
  const r = await enrich({
    line: {
      type: 'file-history-snapshot',
      snapshot: {
        trackedFileBackups: {
          'src/app.ts': {},
          'src/util.ts': {},
          'README.md': {},
        },
      },
    },
  });
  assert.ok(r.languages.includes('typescript'));
  assert.ok(r.languages.includes('markdown'));
  assert.equal(r.languages.length, 2);
});

test('deduplicates languages', async () => {
  const r = await enrich({
    line: {
      type: 'file-history-snapshot',
      snapshot: {
        trackedFileBackups: {
          'a.ts': {},
          'b.ts': {},
          'c.ts': {},
        },
      },
    },
  });
  assert.deepEqual(r.languages, ['typescript']);
});

test('unknown extension produces no language entry', async () => {
  const r = await enrich({
    line: {
      type: 'file-history-snapshot',
      snapshot: {
        trackedFileBackups: { 'data.parquet': {} },
      },
    },
  });
  assert.equal(r.file_count, 1);
  assert.deepEqual(r.languages, []);
});
