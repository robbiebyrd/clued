import { readFileSync } from 'fs';
import { homedir } from 'os';
import { join } from 'path';

const DEFAULT_CONFIG_PATH = join(homedir(), '.claude', 'plugins', 'data', 'clued', 'config.json');

const DEFAULTS = {
  mongoUrl:          'mongodb://localhost:27018',
  dbName:            'claude_sessions',
  port:              8085,
  mcpPort:           8086,
  projectsDir:       join(homedir(), '.claude', 'projects'),
  disabledEnrichers: [],
};

function expandHome(val) {
  if (typeof val !== 'string') return val;
  return val.startsWith('~/') ? join(homedir(), val.slice(2)) : val;
}

export function loadConfig(configPath = DEFAULT_CONFIG_PATH) {
  let fileConfig = {};
  try {
    fileConfig = JSON.parse(readFileSync(configPath, 'utf8'));
  } catch { /* absent or unreadable — use defaults */ }

  const cfg = { ...DEFAULTS, ...fileConfig };

  if (process.env.CLUED_MONGO_URL)    cfg.mongoUrl    = process.env.CLUED_MONGO_URL;
  if (process.env.CLUED_DB_NAME)      cfg.dbName      = process.env.CLUED_DB_NAME;
  if (process.env.CLUED_PORT)         cfg.port        = parseInt(process.env.CLUED_PORT, 10);
  if (process.env.CLUED_MCP_PORT)     cfg.mcpPort     = parseInt(process.env.CLUED_MCP_PORT, 10);
  if (process.env.CLUED_PROJECTS_DIR) cfg.projectsDir = process.env.CLUED_PROJECTS_DIR;

  cfg.projectsDir = expandHome(cfg.projectsDir);
  cfg.mongoUrl    = expandHome(cfg.mongoUrl);

  return cfg;
}
