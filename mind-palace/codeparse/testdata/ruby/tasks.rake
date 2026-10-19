# frozen_string_literal: true

require "shop/importer"

namespace :shop do
  desc "Import the product catalogue from a CSV file"
  task :import, [:path] => :environment do |_t, args|
    Shop::Importer.new(args[:path]).run
  end

  desc "Rebuild the search index"
  task reindex: %i[environment import] do
    Shop::Search.rebuild!
  end

  namespace :orders do
    # Purges abandoned baskets.
    task "purge" do
      Shop::Order.where(status: :draft).delete_all
    end
  end

  def batch_size
    ENV.fetch("BATCH_SIZE", 500).to_i
  end
end

task default: "shop:reindex"
