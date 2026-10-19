// Shapes: an ES module with every kind of definition JavaScript has.

import { EventEmitter } from 'node:events';
import * as geometry from './geometry.mjs';

/** The default precision of every area, in decimal places. */
export const PRECISION = 2;

/** How many shapes were made; not exported. */
let created = 0;

var registry = new Map();

/**
 * Shape is the base of every shape.
 * It counts the shapes it makes.
 */
export class Shape extends EventEmitter {
  /** The shape's name. */
  name = 'shape';
  static #count = 0;
  #id;

  constructor(name) {
    super();
    this.name = name;
    this.#id = Shape.#count++;
    created++;
  }

  /** Area of the shape; subclasses override it. */
  area() {
    throw new Error('not implemented');
  }

  get id() {
    return this.#id;
  }

  set label(value) {
    this.name = String(value);
  }

  static create(kind, ...args) {
    return new kinds[kind](...args);
  }

  #describe() {
    return `${this.name} #${this.#id}`;
  }

  toString() {
    return this.#describe();
  }

  // An arrow function in a field is a method bound to the instance.
  onResize = (event) => {
    this.emit('resize', event);
  };

  *[Symbol.iterator]() {
    yield this.name;
  }
}

/** A circle of radius r. */
export class Circle extends Shape {
  constructor(r) {
    super('circle');
    this.r = r;
  }

  area() {
    return round(Math.PI * this.r ** 2);
  }
}

class Square extends geometry.Polygon {
  area() {
    return round(geometry.squared(this.side));
  }
}

const kinds = { circle: Circle, square: Square };

/** Rounds x to PRECISION places. */
export function round(x) {
  const f = 10 ** PRECISION;
  return Math.round(x * f) / f;
}

export async function load(url) {
  const res = await fetch(url);
  return parse(await res.json());
}

function* ids(start = 0) {
  let i = start;
  while (true) yield i++;
}

/** Parses a list of shape descriptions. */
const parse = (items) => items.map((it) => Shape.create(it.kind, it.size));

export const total = function (shapes) {
  return shapes.reduce((sum, s) => sum + s.area(), 0);
};

export const Unit = class {
  area() {
    return 1;
  }
};

export let counter = 0, limit = 10;

export default class Canvas {
  draw(shape) {
    registry.set(shape.id, shape);
    console.log(shape.toString());
  }
}

export { created, ids as identifiers };
