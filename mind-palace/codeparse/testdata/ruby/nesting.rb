# Compact and nested namespaces, reopened classes and definitions written
# as calls: the cases name-based resolution has to get right.

autoload :Ledger, "billing/ledger"

module Billing
  module Gateways; end
end

=begin
Stripe is reached through Billing::Gateways, written compactly.
=end
class Billing::Gateways::Stripe < Billing::Gateways::Base
  API_VERSION = "2024-06-20"

  def charge(amount, token)
    client.post("/charges", amount: amount, source: token)
  end
end

module Billing
  # Reopens the module: Invoice is nested lexically.
  class Invoice
    Line = Struct.new(:description, :cents)
    Totals = Data.define(:net, :tax)
    Formatting = Module.new

    %w[draft sent paid].each do |state|
      define_method(:"#{state}?") { @state == state }
    end

    define_method(:void!) do
      @state = "void"
    end

    alias_method :to_s, :number
    alias settle pay

    def self.[](number) = new(number)

    def pay
      Gateways::Stripe.new.charge(total, token)
    end

    def ==(other) = other.is_a?(Invoice) && number == other.number

    # A compact name inside a nesting: Monthly is qualified by its lexical
    # containers, Reports stays a reference.
    class Reports::Monthly; end
  end

  Gateways::TIMEOUT = 15
end

# A class built at run time.
Refund = Class.new(Billing::Invoice) do
  def credit?
    true
  end
end

def Billing.configure
  yield config
end

Billing::Invoice.prepend(Module.new { def pay = super })
