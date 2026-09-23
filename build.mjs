import { build } from 'esbuild';

const BANNER = "import { createRequire } from 'module'; const require = createRequire(import.meta.url);";

const shared = {
  bundle:   true,
  platform: 'node',
  format:   'esm',
  banner:   { js: BANNER },
};

await Promise.all([
  build({ ...shared, entryPoints: ['src/daemon.mjs'],   outfile: 'dist/daemon.mjs'   }),
  build({ ...shared, entryPoints: ['src/backfill.mjs'], outfile: 'dist/backfill.mjs' }),
  build({ ...shared, entryPoints: ['src/mcp.mjs'],      outfile: 'dist/mcp.mjs'      }),
]);
