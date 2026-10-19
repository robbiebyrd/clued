# frozen_string_literal: true

require "bigdecimal"
require "active_support/core_ext/object/blank"
require_relative "line_item"
require_relative "../concerns/auditable"

# Shop holds the storefront's domain model.
module Shop
  # The currency every price is kept in.
  CURRENCY = "EUR"
  MAX_ITEMS = 100
  STATUSES = %i[draft placed shipped].freeze
  DEFAULT_TIMEOUT ||= 30

  class Error < StandardError; end

  # Order is a customer's basket once it is placed.
  class Order < ApplicationRecord
    include Comparable
    include Shop::Auditable
    extend Forwardable
    prepend Instrumented

    # The order's identifier and its line items.
    attr_reader :id, :items
    attr_accessor :note
    attr_writer "customer"

    def_delegators :items, :size, :each

    # Builds an order for a customer.
    def initialize(id, customer:, items: [], **options)
      @id = id
      @customer = customer
      @items = items.map { |attrs| LineItem.new(**attrs) }
      @placed_at = nil
      super(**options)
    end

    # The total of every line, in CURRENCY.
    def total
      items.sum(BigDecimal("0")) { |item| item.price * item.quantity }
    end

    def place!
      raise Error, "empty order" if items.empty?
      raise Error, "too many items" if items.size > MAX_ITEMS

      @placed_at = Time.now.utc
      Shop::Mailer.order_placed(self).deliver_later
      notify(:placed)
    end

    def placed? = !@placed_at.nil?

    def <=>(other)
      total <=> other.total
    end

    # Finds an order by its identifier.
    def self.find(id)
      Repository.instance.fetch(id) || raise(Shop::NotFound, id)
    end

    class << self
      # Every order placed today.
      def today
        where(placed_at: Date.today.all_day)
      end
    end

    protected

    def notify(event)
      Shop::Events.publish(event, order: self)
    end
  end

  # Line items price themselves.
  class LineItem < Struct.new(:sku, :price, :quantity, keyword_init: true)
    def subtotal
      price * quantity
    end
  end
end

def format_money(amount)
  format("%.2f %s", amount, Shop::CURRENCY)
end
