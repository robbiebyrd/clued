// CommonJS in TypeScript (.cts): import-equals, require and module.exports.
import assert = require("node:assert");
const express = require("express");
const { Router, json: parseJson } = require("express");
require("dotenv/config");

const router = Router();

function handler(req: Request, res: Response): void {
  assert.ok(req.body);
  res.send(parseJson(req.body));
}

router.post("/items", handler);

module.exports = { router, app: express() };
