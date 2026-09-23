import { createRequire } from 'module'; const require = createRequire(import.meta.url);
const name = "bash-binaries";
const collection = "hook_events";
const enabled = true;
function matches(doc) {
  const ti = doc.tool_input;
  return doc.tool_name === "Bash" && typeof ti?.command === "string";
}
async function enrich(doc) {
  const ti = doc.tool_input;
  return { binaries: extractBinaries(ti.command) };
}
function extractBinaries(command) {
  const tokens = command.split(/\s*(?:&&|\|\||;|\||\n)\s*/).map((stage) => stage.trim()).filter(Boolean).map((stage) => {
    const binary = stage.split(/\s+/).find((t) => !/^\w+=/.test(t));
    return binary ? binary.replace(/^["']|["']$/g, "") : null;
  }).filter((b) => b !== null);
  const BUILTINS = /* @__PURE__ */ new Set(["if", "then", "else", "fi", "for", "do", "done", "while", "case", "esac", "echo", "cd", "export", "source", ".", "[", "[[", "]]", "]"]);
  return [...new Set(tokens.map((t) => t.split("/").pop()).filter((t) => t && !BUILTINS.has(t)))];
}
export {
  collection,
  enabled,
  enrich,
  matches,
  name
};
