-- Every way a Lua file pulls in another.
require "busted.runner"
require("luarocks.loader")
require 'strict'

local json = require("cjson")
local socket = require "socket"
local http = require("socket.http")
local insert = require("table").insert
local lpeg, re = require("lpeg"), require("re")

local ok, lfs = pcall(require, "lfs")

dofile("config.lua")
local chunk = loadfile("plugins/init.lua")

local function fetch(url)
  local body, code = http.request(url)
  if code ~= 200 then
    error("fetch " .. url .. ": " .. tostring(code))
  end
  return json.decode(body)
end

local data = fetch("https://example.com/data.json")
insert(data, socket.gettime())
print(lpeg.match(re.compile("%a+"), "abc"), ok and lfs.currentdir(), chunk)
