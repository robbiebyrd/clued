-- Classes the usual way: a table as metatable, __index for inheritance.

--- Animal is the base class.
local Animal = {}
Animal.__index = Animal

--- Creates an animal called name.
function Animal.new(name, sound)
  local self = setmetatable({}, Animal)
  self.name = name
  self.sound = sound
  self.on_speak = function() end
  return self
end

--- Speak prints the animal's sound.
function Animal:speak()
  self.on_speak()
  print(self.name .. " says " .. self:sound_of())
end

function Animal:sound_of()
  return self.sound
end

--- Dog extends Animal.
local Dog = setmetatable({}, { __index = Animal })
Dog.__index = Dog

function Dog.new(name)
  local self = Animal.new(name, "woof")
  return setmetatable(self, Dog)
end

function Dog:fetch(thing)
  self:speak()
  return thing
end

local Cat = {}
setmetatable(Cat, { __index = Animal })

function Cat:speak()
  Animal.speak(self)
end

local rex = Dog.new("Rex")
rex:fetch("ball")
local base = require("lib.base")
local Puppy = setmetatable({}, { __index = base.Dog })

return { Animal = Animal, Dog = Dog, Cat = Cat, Puppy = Puppy }
