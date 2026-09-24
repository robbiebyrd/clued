// Transport-agnostic MCP JSON-RPC handling, shared by the SSE and stdio transports.

export const SUPPORTED_PROTOCOL_VERSIONS = ['2025-11-25', '2025-06-18', '2025-03-26', '2024-11-05'];
const DEFAULT_PROTOCOL_VERSION = '2025-06-18';

export const SERVER_INFO = { name: 'clued', version: '1.0.0' };

export const TOOLS = [
  {
    name: 'find_sessions',
    description: 'Find previous Claude Code sessions, newest first. Filters match (case-insensitive regex) against project path, cwd, and git origin.',
    inputSchema: {
      type: 'object',
      properties: {
        project_path: { type: 'string', description: 'Regex matched against the session project path' },
        git_origin:   { type: 'string', description: 'Regex matched against the git remote origin URL' },
        query:        { type: 'string', description: 'Regex matched against project path or cwd' },
        limit:        { type: 'number', description: 'Max sessions to return (default 10, max 500)' },
      },
    },
  },
  {
    name: 'get_session_context',
    description: 'Summarise one session: metadata, its most recent distinct Bash commands, and the first/last transcript lines.',
    inputSchema: {
      type: 'object',
      properties: { session_id: { type: 'string' } },
      required: ['session_id'],
    },
  },
  {
    name: 'search_commands',
    description: 'Search Bash commands run in previous sessions, newest first.',
    inputSchema: {
      type: 'object',
      properties: {
        pattern:    { type: 'string', description: 'Case-insensitive regex matched against the command' },
        session_id: { type: 'string', description: 'Restrict to one session' },
        git_origin: { type: 'string', description: 'Restrict to sessions whose git origin matches this regex' },
        limit:      { type: 'number', description: 'Max commands to return (default 20, max 500)' },
      },
      required: ['pattern'],
    },
  },
  {
    name: 'read_transcript',
    description: 'Read transcript lines of a session in order, paginated by offset/limit.',
    inputSchema: {
      type: 'object',
      properties: {
        session_id: { type: 'string' },
        offset:     { type: 'number', description: 'Line offset (default 0)' },
        limit:      { type: 'number', description: 'Lines to return (default 200, max 500)' },
      },
      required: ['session_id'],
    },
  },
];

export interface JsonRpcRequest {
  jsonrpc?: string;
  id?:      unknown;
  method:   string;
  params?:  Record<string, unknown>;
}

export type CallTool = (
  name: string,
  args: Record<string, unknown>,
  meta: Record<string, unknown>,
) => Promise<unknown>;

// Returns the JSON-RPC response to send, or undefined for notifications (which get no reply).
export async function dispatch(rpc: JsonRpcRequest, callTool: CallTool): Promise<unknown> {
  const { id, method, params = {} } = rpc;
  if (id === undefined || id === null) return undefined;

  const ok = (result: unknown) => ({ jsonrpc: '2.0', id, result });

  switch (method) {
    case 'initialize': {
      const requested = params.protocolVersion as string | undefined;
      const protocolVersion = requested && SUPPORTED_PROTOCOL_VERSIONS.includes(requested)
        ? requested
        : DEFAULT_PROTOCOL_VERSION;
      return ok({ protocolVersion, capabilities: { tools: {} }, serverInfo: SERVER_INFO });
    }
    case 'ping':
      return ok({});
    case 'tools/list':
      return ok({ tools: TOOLS });
    case 'tools/call':
      try {
        const result = await callTool(
          params.name as string,
          (params.arguments as Record<string, unknown>) || {},
          (params._meta as Record<string, unknown>) || {},
        );
        return ok({ content: [{ type: 'text', text: JSON.stringify(result) }] });
      } catch (e) {
        return { jsonrpc: '2.0', id, error: { code: -32000, message: (e as Error).message } };
      }
    default:
      return { jsonrpc: '2.0', id, error: { code: -32601, message: `method not found: ${method}` } };
  }
}
