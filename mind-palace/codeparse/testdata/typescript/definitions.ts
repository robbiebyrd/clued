// Definitions of every kind TypeScript has: functions, classes and their
// members, interfaces, type aliases, enums, namespaces, modules and
// module-level variables.

/** The default page size. */
export const PAGE_SIZE = 50;

export let current: Store | undefined;

var legacyCounter = 0;

const internalCache = new Map<string, Entry>();

/**
 * Formats an entry for display.
 * @param entry the entry to format
 */
export function format(entry: Entry, width = 80): string {
  return pad(entry.title, width);
}

function pad(s: string, width: number): string {
  return s.padEnd(width);
}

export function* ids(start: number): Generator<number> {
  let next = start;
  while (true) yield next++;
}

// Overloads: two signatures and the implementation.
export function parse(input: string): Entry;
export function parse(input: Buffer): Entry;
export function parse(input: string | Buffer): Entry {
  return JSON.parse(input.toString());
}

export const toKey = (entry: Entry): string => `${entry.id}`;

const compare = function byTitle(a: Entry, b: Entry): number {
  return a.title.localeCompare(b.title);
};

/** An entry of the store. */
export interface Entry {
  id: number;
  title: string;
  tags?: string[];
  describe(verbose: boolean): string;
}

export type EntryID = Entry["id"];

export type Options = {
  limit: number;
  filter(entry: Entry): boolean;
};

type Callback<T> = (err: Error | null, value?: T) => void;

/** How an entry is stored. */
export enum Kind {
  Note,
  Task = "task",
  Event = 3,
}

const enum Flag {
  None = 0,
  Pinned = 1 << 0,
}

/** A store of entries, kept in memory. */
export class Store implements Iterable<Entry> {
  static instances = 0;
  readonly name: string;
  protected entries: Entry[] = [];
  private dirty = false;
  #secret = "s3cr3t";

  constructor(name: string) {
    this.name = name;
    Store.instances++;
  }

  /** Adds an entry and marks the store dirty. */
  add(entry: Entry): void {
    this.entries.push(entry);
    this.touch();
  }

  get size(): number {
    return this.entries.length;
  }

  private touch(): void {
    this.dirty = true;
  }

  #rotate(): void {
    this.#secret = this.#secret.split("").reverse().join("");
  }

  async *[Symbol.asyncIterator](): AsyncGenerator<Entry> {
    yield* this.entries;
  }

  [Symbol.iterator](): Iterator<Entry> {
    return this.entries[Symbol.iterator]();
  }
}

export abstract class Exporter<T> {
  abstract write(item: T): Promise<void>;
  protected abstract flush(): void;

  async writeAll(items: T[]): Promise<void> {
    for (const item of items) {
      await this.write(item);
    }
    this.flush();
  }
}

export namespace Storage.Disk {
  export const ROOT = "/var/lib/store";
  export function path(id: number): string {
    return `${ROOT}/${id}`;
  }
  let opened = 0;
  function open(): void {
    opened++;
  }
}

namespace Internal {
  export class Lock {}
}
