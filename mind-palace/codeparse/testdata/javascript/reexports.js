// Every way a module re-exports or loads another one.

export * from './shapes.mjs';
export * as geometry from './geometry.mjs';
export { round, load as loadShapes } from './shapes.mjs';
export { default as Canvas } from './shapes.mjs';
export { default } from './App.jsx';

import defaultExport, * as everything from 'lib';
import { a, b as c, default as d } from "quoted-lib";
import json from './data.json' with { type: 'json' };

const lazy = () => import('./lazy.js');
const { Store } = require('./store.cjs');

export function plugin() {
  return import(`./plugins/${name}.js`);
}
