<?php

namespace App\Repository;

use App\Contracts\Repository;
use App\Contracts\Countable;
use App\Models\Order;
use App\Events\OrderPlaced;

/**
 * Stores orders in memory.
 */
class OrderRepository extends BaseRepository implements Repository, Countable
{
    use LogsQueries, CachesResults {
        CachesResults::remember insteadof LogsQueries;
    }

    private Connection $db;

    public function __construct(Connection $db, ?Cache $cache = null)
    {
        parent::__construct($cache);
        $this->db = $db;
    }

    public function find(int $id): ?Order
    {
        $row = $this->db->select('orders', ['id' => $id]);
        if (!$row instanceof Row) {
            throw new NotFoundException("order $id");
        }
        return Order::fromRow($row);
    }

    public function save(Order|Draft $order): void
    {
        $this->db?->insert('orders', $order->toArray());
        event(new OrderPlaced($order));
        static::flush();
        self::log(sprintf('saved %d', $order->id));
        $fn = strtoupper(...);
        $total = \App\Support\sum($order->lines, Order::TAX_RATE);
        namespace\audit($order);
    }

    public function count(): int
    {
        try {
            return count($this->db->all('orders'));
        } catch (QueryException | \RuntimeException $e) {
            report($e);
            return 0;
        }
    }
}

interface Repository extends \Countable, Searchable
{
}
