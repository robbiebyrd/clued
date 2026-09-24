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

// mcp.mjs is distributed via npm (node_modules/mongodb installed by npm).
// daemon.mjs runs from the plugin cache which has no node_modules, so mongodb
// must be bundled in. backfill.mjs runs in the same context as the daemon.
const withExternalMongo = { ...shared, external: ['mongodb'] };

const enricherEntries = readdirSync('enrichers')
  .filter(f => f.endsWith('.ts'))
  .map(f => ({
    entryPoints: [`enrichers/${f}`],
    outfile:     `dist/enrichers/${f.replace(/\.ts$/, '.mjs')}`,
  }));

await Promise.all([
  build({ ...shared,             entryPoints: ['src/daemon.ts'],   outfile: 'dist/daemon.mjs'   }),
  build({ ...withExternalMongo, entryPoints: ['src/backfill.ts'], outfile: 'dist/backfill.mjs' }),
  build({ ...withExternalMongo, entryPoints: ['src/mcp.ts'],      outfile: 'dist/mcp.mjs', banner: { js: SHEBANG_BANNER } }),
  ...enricherEntries.map(e => build({ ...withExternalMongo, ...e })),
]);

// npm bin scripts must be executable
chmodSync('dist/mcp.mjs', 0o755);
