import { test } from 'node:test';
import assert from 'node:assert/strict';
import { dispatch, TOOLS, SERVER_INFO } from '../src/mcp-protocol';

const noTool = async () => { throw new Error('should not be called'); };

test('initialize echoes a supported protocol version and advertises tools', async () => {
  const res = await dispatch({ id: 1, method: 'initialize', params: { protocolVersion: '2025-03-26' } }, noTool) as any;
  assert.equal(res.id, 1);
  assert.equal(res.result.protocolVersion, '2025-03-26');
  assert.deepEqual(res.result.capabilities, { tools: {} });
  assert.deepEqual(res.result.serverInfo, SERVER_INFO);
});

test('initialize falls back to the default version for unknown requests', async () => {
  const res = await dispatch({ id: 1, method: 'initialize', params: { protocolVersion: '1999-01-01' } }, noTool) as any;
  assert.equal(res.result.protocolVersion, '2025-06-18');
});

test('notifications get no reply', async () => {
  assert.equal(await dispatch({ method: 'notifications/initialized' }, noTool), undefined);
});

test('tools/list returns every tool with an input schema', async () => {
  const res = await dispatch({ id: 2, method: 'tools/list' }, noTool) as any;
  assert.deepEqual(res.result.tools.map((t: any) => t.name),
    ['find_sessions', 'get_session_context', 'search_commands', 'read_transcript', 'restore_session']);
  for (const t of TOOLS) assert.equal(t.inputSchema.type, 'object');
});

test('tools/call wraps the result as JSON text content', async () => {
  const res = await dispatch(
    { id: 3, method: 'tools/call', params: { name: 'find_sessions', arguments: { query: 'x' }, _meta: { progressToken: 7 } } },
    async (name, args, meta) => ({ name, args, meta }),
  ) as any;
  assert.deepEqual(JSON.parse(res.result.content[0].text),
    { name: 'find_sessions', args: { query: 'x' }, meta: { progressToken: 7 } });
});

test('tools/call reports tool failures as a JSON-RPC error', async () => {
  const res = await dispatch(
    { id: 4, method: 'tools/call', params: { name: 'read_transcript', arguments: {} } },
    async () => { throw new Error('session not found'); },
  ) as any;
  assert.equal(res.error.code, -32000);
  assert.equal(res.error.message, 'session not found');
});

test('unknown methods return a method-not-found error', async () => {
  const res = await dispatch({ id: 5, method: 'resources/list' }, noTool) as any;
  assert.equal(res.error.code, -32601);
});
