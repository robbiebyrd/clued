import { build } from 'esbuild';
import { readdirSync, chmodSync } from 'fs';

const BANNER = "import { createRequire } from 'module'; const require = createRequire(import.meta.url);";
const SHEBANG_BANNER = `#!/usr/bin/env node\n${BANNER}`;

const shared = {
  bundle:   true,
  platform: 'node'  as const,
  format:   'esm'   as const,
  banner:   { js: BANNER },
};

const enricherEntries = readdirSync('enrichers')
  .filter(f => f.endsWith('.ts'))
  .map(f => ({
    entryPoints: [`enrichers/${f}`],
    outfile:     `dist/enrichers/${f.replace(/\.ts$/, '.mjs')}`,
  }));

await Promise.all([
  build({ ...shared, entryPoints: ['src/daemon.ts'],   outfile: 'dist/daemon.mjs'   }),
  build({ ...shared, entryPoints: ['src/backfill.ts'], outfile: 'dist/backfill.mjs' }),
  build({ ...shared, entryPoints: ['src/mcp.ts'],      outfile: 'dist/mcp.mjs', banner: { js: SHEBANG_BANNER } }),
  ...enricherEntries.map(e => build({ ...shared, ...e })),
]);

// npm bin scripts must be executable
chmodSync('dist/mcp.mjs', 0o755);
