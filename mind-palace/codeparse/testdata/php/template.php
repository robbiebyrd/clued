<?php
/** @var array<int, \App\Models\Item> $items */
require __DIR__ . '/layout.php';

function item_class(\App\Models\Item $item): string
{
    return $item->active ? 'active' : 'idle';
}
?>
<!doctype html>
<html>
<body>
<ul>
<?php foreach ($items as $item): ?>
    <li class="<?= item_class($item) ?>"><?= htmlspecialchars($item->name) ?></li>
<?php endforeach; ?>
</ul>
<?php if (count($items) === 0) { ?>
    <p>No items.</p>
<?php } ?>
</body>
</html>
