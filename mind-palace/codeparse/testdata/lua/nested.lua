-- Awkward cases: nested tables, deep field paths, closures, long strings,
-- goto and a generated table of handlers.

local app = { handlers = {}, util = {} }

function app.util.escape(s)
  return (s:gsub("[%c\"\\]", function(c)
    return string.format("\\%03d", c:byte())
  end))
end

function app.handlers.get(req)
  local function inner(x)
    local function innermost() return x end
    return innermost()
  end
  return inner(req.path)
end

app.handlers["post"] = function(req) return req end

local template = [==[
function not_a_function() end
local x = require("not.an.import")
]==]

-- Generated: one handler per verb.
for _, verb in ipairs({ "put", "delete" }) do
  app.handlers[verb] = function(req)
    return app.handlers.get(req)
  end
end

local ops = {
  add = function(a, b) return a + b end,
  sub = function(a, b) return a - b end,
  nested = {
    mul = function(a, b) return a * b end,
  },
}

local function loop(n)
  local i = 1
  ::top::
  if i < n then
    i = i + 1
    goto top
  end
  return ops.nested.mul(i, ops.add(1, 2)), template
end

local obj = { value = 1 }
function obj:get() return self.value end
print(obj:get(), loop(3), app.util.escape("a\n"))

return app
