<?php

namespace App\Broken;

/**
 * Parsed despite the error below.
 */
function good(): int
{
    return 1;
}

class Broken
{
    public function bad(: void
    {
        return
    }

    public function after(): string
    {
        return helper();
    }
}

function later(): void
{
}
