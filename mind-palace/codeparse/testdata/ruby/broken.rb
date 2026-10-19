require "json"

module Inventory
  # A warehouse with stock.
  class Warehouse
    def initialize(name)
      @name = name
    end

    def stock(sku
      @levels.fetch(sku, 0)
    end

    def restock(sku, count)
      @levels[sku] += count
    end
  end

  LOW_WATER = 5
end
