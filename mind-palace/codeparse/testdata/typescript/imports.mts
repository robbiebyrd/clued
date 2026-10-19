// Every import form an ES module (.mts) can write.
import fs, { readFile, writeFile as write } from "node:fs";
import * as path from "node:path";
import type { Entry, Options } from "./definitions.js";
import { type Store, default as createStore } from "./store.js";
import Default from "../lib/default.js";
import "./polyfills.js";

export * from "./types.js";
export * as util from "./util.js";
export { format, parse as parseEntry } from "./definitions.js";
export type { Kind } from "./definitions.js";

const lazy = await import("./lazy.js");

export async function load(file: string): Promise<Entry[]> {
  const raw = await readFile(path.join(Default.dir, file), "utf8");
  const plugin = await import("./plugins/" + file);
  write(file + ".bak", raw);
  return plugin.decode(raw, fs.constants.R_OK, lazy);
}
