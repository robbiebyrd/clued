import { readFileSync } from 'fs';
import { homedir } from 'os';
import { join } from 'path';

export interface Config {
  mongoUrl:          string;
  dbName:            string;
  port:              number;
  mcpPort:           number;
  projectsDir:       string;
  disabledEnrichers: string[];
}

const DEFAULT_CONFIG_PATH = join(homedir(), '.claude', 'plugins', 'data', 'clued', 'config.json');

const DEFAULTS: Config = {
  mongoUrl:          'mongodb://localhost:27018',
  dbName:            'claude_sessions',
  port:              8085,
  mcpPort:           8086,
  projectsDir:       join(homedir(), '.claude', 'projects'),
  disabledEnrichers: [],
};

function expandHome(val: string): string {
  return val.startsWith('~/') ? join(homedir(), val.slice(2)) : val;
}

export function loadConfig(configPath = DEFAULT_CONFIG_PATH): Config {
  let fileConfig: Partial<Config> = {};
  try {
    fileConfig = JSON.parse(readFileSync(configPath, 'utf8')) as Partial<Config>;
  } catch { /* absent or unreadable — use defaults */ }

  const cfg: Config = { ...DEFAULTS, ...fileConfig };

  if (process.env.CLUED_MONGO_URL)    cfg.mongoUrl    = process.env.CLUED_MONGO_URL;
  if (process.env.CLUED_DB_NAME)      cfg.dbName      = process.env.CLUED_DB_NAME;
  if (process.env.CLUED_PORT)         cfg.port        = parseInt(process.env.CLUED_PORT, 10);
  if (process.env.CLUED_MCP_PORT)     cfg.mcpPort     = parseInt(process.env.CLUED_MCP_PORT, 10);
  if (process.env.CLUED_PROJECTS_DIR) cfg.projectsDir = process.env.CLUED_PROJECTS_DIR;

  cfg.projectsDir = expandHome(cfg.projectsDir);
  cfg.mongoUrl    = expandHome(cfg.mongoUrl);

  return cfg;
}
