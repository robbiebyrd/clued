export const name       = 'bash-binaries';
export const collection = 'hook_events';
export const enabled    = true;

export function matches(doc) {
  return doc.tool_name === 'Bash' && typeof doc.tool_input?.command === 'string';
}

export async function enrich(doc) {
  const binaries = extractBinaries(doc.tool_input.command);
  return { binaries };
}

// Extracts the leading token of each pipeline stage — the binary name.
// Handles &&, ||, ;, |, and newline separators. Strips quoting and env-var prefixes.
function extractBinaries(command) {
  const tokens = command
    .split(/\s*(?:&&|\|\||;|\||\n)\s*/)
    .map(stage => stage.trim())
    .filter(Boolean)
    .map(stage => {
      // Skip env-var assignments (FOO=bar) at the start of a stage
      const tokens = stage.split(/\s+/);
      const binary = tokens.find(t => !/^\w+=/.test(t));
      return binary ? binary.replace(/^["']|["']$/g, '') : null;
    })
    .filter(Boolean);

  // Deduplicate, strip path prefixes, skip shell builtins
  const BUILTINS = new Set(['if','then','else','fi','for','do','done','while','case','esac','echo','cd','export','source','.','[','[[',']]',']']);
  return [...new Set(tokens.map(t => t.split('/').pop()).filter(t => t && !BUILTINS.has(t)))];
}
