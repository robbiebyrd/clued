import { build } from 'esbuild';

const BANNER = "import { createRequire } from 'module'; const require = createRequire(import.meta.url);";

const shared = {
  bundle:   true,
  platform: 'node'  as const,
  format:   'esm'   as const,
  banner:   { js: BANNER },
};

await Promise.all([
  build({ ...shared, entryPoints: ['src/daemon.ts'],              outfile: 'dist/daemon.mjs'                          }),
  build({ ...shared, entryPoints: ['src/backfill.ts'],            outfile: 'dist/backfill.mjs'                        }),
  build({ ...shared, entryPoints: ['src/mcp.ts'],                 outfile: 'dist/mcp.mjs'                             }),
  build({ ...shared, bundle: false, entryPoints: ['enrichers/bash-binaries.ts'],  outfile: 'dist/enrichers/bash-binaries.mjs'  }),
  build({ ...shared, bundle: false, entryPoints: ['enrichers/privacy-redact.ts'], outfile: 'dist/enrichers/privacy-redact.mjs' }),
]);
