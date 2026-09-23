export const name       = 'bash-binaries';
export const collection = 'hook_events';
export const enabled    = true;

export function matches(doc: Record<string, unknown>): boolean {
  const ti = doc.tool_input as Record<string, unknown> | undefined;
  return doc.tool_name === 'Bash' && typeof ti?.command === 'string';
}

export async function enrich(doc: Record<string, unknown>): Promise<{ binaries: string[] }> {
  const ti = doc.tool_input as Record<string, unknown>;
  return { binaries: extractBinaries(ti.command as string) };
}

// Extracts the leading token of each pipeline stage — the binary name.
// Handles &&, ||, ;, |, and newline separators. Strips quoting and env-var prefixes.
function extractBinaries(command: string): string[] {
  const tokens = command
    .split(/\s*(?:&&|\|\||;|\||\n)\s*/)
    .map(stage => stage.trim())
    .filter(Boolean)
    .map(stage => {
      const binary = stage.split(/\s+/).find(t => !/^\w+=/.test(t));
      return binary ? binary.replace(/^["']|["']$/g, '') : null;
    })
    .filter((b): b is string => b !== null);

  const BUILTINS = new Set(['if','then','else','fi','for','do','done','while','case','esac','echo','cd','export','source','.','[','[[',']]',']']);
  return [...new Set(tokens.map(t => t.split('/').pop()!).filter(t => t && !BUILTINS.has(t)))];
}
