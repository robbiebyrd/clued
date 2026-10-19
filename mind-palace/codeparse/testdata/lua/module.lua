-- Copyright 2026 Example. A file header, not a doc comment.

--- A small string utility module.
-- @module strutil
local strutil = {
  --- The module's version.
  VERSION = "1.2.0",
  sep = ",",
  --- Trims white space from both ends of s.
  trim = function(s)
    return (s:gsub("^%s+", ""):gsub("%s+$", ""))
  end,
}

--- The default options.
strutil.defaults = { strict = false }
strutil.MAX = 64

local cache = {}

--- Splits s at sep (strutil.sep by default).
---@param s string
---@param sep? string
---@return string[]
function strutil.split(s, sep)
  sep = sep or strutil.sep
  local out = {}
  for piece in string.gmatch(s, "([^" .. sep .. "]+)") do
    out[#out + 1] = strutil.trim(piece)
  end
  return out
end

--[[
  Joins the parts with sep.
]]
function strutil.join(parts, sep)
  return table.concat(parts, sep or strutil.sep)
end

-- memo caches f's results by its first argument.
local function memo(f)
  return function(x)
    if cache[x] == nil then
      cache[x] = f(x)
    end
    return cache[x]
  end
end

strutil.upper = memo(string.upper)

--- Reports whether s starts with prefix.
strutil.startswith = function(s, prefix)
  return s:sub(1, #prefix) == prefix
end

local count = function(s, pattern)
  return select(2, s:gsub(pattern, ""))
end

--- Counts the words in s.
function strutil.words(s)
  return count(s, "%S+")
end

-- A global helper, visible to every chunk.
function strutil_debug(...)
  print("strutil", ...)
end

return strutil
