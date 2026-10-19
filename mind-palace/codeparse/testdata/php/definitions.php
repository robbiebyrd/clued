<?php

declare(strict_types=1);

namespace App\Billing;

/**
 * The largest invoice the service accepts, in cents.
 */
const MAX_INVOICE = 1_000_000;

define('BILLING_VERSION', '2.4');

/**
 * Describes anything that can be charged.
 */
interface Chargeable
{
    /** The charge's currency, as an ISO 4217 code. */
    public const CURRENCY = 'EUR';

    public function amount(): int;

    public static function fromArray(array $data): static;
}

/**
 * Timestamps a model on creation and update.
 */
trait Timestamps
{
    protected ?\DateTimeImmutable $createdAt = null;

    // Marks the model as touched now.
    public function touch(): void
    {
        $this->createdAt ??= new \DateTimeImmutable();
    }
}

/**
 * The states an invoice goes through.
 */
enum Status: string
{
    case Draft = 'draft';
    case Paid = 'paid';

    const DEFAULT = self::Draft;

    public function label(): string
    {
        return ucfirst($this->value);
    }
}

/**
 * An invoice of one customer.
 */
abstract class Invoice implements Chargeable
{
    use Timestamps;

    public const PREFIX = 'INV-';
    private const SECRET = 'x', OTHER = 'y';

    public static int $count = 0;

    private array $lines = [];

    public function __construct(
        private readonly string $number,
        protected int $total = 0,
    ) {
        self::$count++;
    }

    /**
     * The invoice's total, in cents.
     */
    public function amount(): int
    {
        return $this->total;
    }

    abstract protected function tax(int $amount): int;

    final public static function fromArray(array $data): static
    {
        return new static($data['number'], $data['total'] ?? 0);
    }

    private function secret(): string
    {
        return self::SECRET;
    }
}

final readonly class Money
{
    public function __construct(public int $cents, public string $currency = 'EUR')
    {
    }
}

/**
 * Formats an amount in cents for display.
 */
function format_amount(int $cents, string $currency = 'EUR'): string
{
    return sprintf('%s %.2f', $currency, $cents / 100);
}

function &reference_returning(array &$items): array
{
    return $items;
}

class Legacy
{
    var $name;

    static function make()
    {
        return new self();
    }
}
