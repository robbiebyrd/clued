'use strict';

// A key-value store in CommonJS: require, module.exports and prototypes.

const fs = require('fs');
const path = require('node:path');
const { promisify, inspect: show } = require('util');
const debug = require('debug')('store');
require('./polyfills');

const readFile = promisify(fs.readFile);

/**
 * Store keeps values by key, in memory and on disk.
 * @param {string} dir where the store writes
 */
function Store(dir) {
  if (!(this instanceof Store)) return new Store(dir);
  this.dir = path.resolve(dir);
  this.cache = Object.create(null);
}

/** Get returns the value of key, reading it from disk once. */
Store.prototype.get = async function get(key) {
  if (key in this.cache) return this.cache[key];
  const raw = await readFile(this.file(key), 'utf8');
  return (this.cache[key] = JSON.parse(raw));
};

Store.prototype.file = function (key) {
  return path.join(this.dir, encodeURIComponent(key));
};

Store.prototype.toString = () => show(this);

function memoize(fn) {
  const seen = new Map();
  // A closure defined inside a function nests under it.
  function lookup(arg) {
    if (!seen.has(arg)) seen.set(arg, fn(arg));
    return seen.get(arg);
  }
  return lookup;
}

/** Opens a store under the user's home. */
exports.open = function open(name) {
  debug('open %s', name);
  return new Store(path.join(process.env.HOME, name));
};

module.exports.close = (store) => {
  store.cache = null;
};

exports.VERSION = '1.4.0';

module.exports.Store = Store;
module.exports.memoize = memoize;

module.exports = {
  Store,
  memoize,
  /** Removes every cached value. */
  clear(store) {
    store.cache = Object.create(null);
  },
  reset: function (store) {
    module.exports.clear(store);
  },
};
