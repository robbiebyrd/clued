#!/usr/bin/env node
import('../dist/mcp.mjs').catch(e => { console.error(e); process.exit(1); });
