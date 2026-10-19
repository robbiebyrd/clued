<?php
/**
 * A template with code inside braces namespaces, conditional and nested
 * declarations, closures and an anonymous class.
 */

namespace Legacy\Helpers {

    if (!function_exists('Legacy\Helpers\render')) {
        /**
         * Renders a template with its variables.
         */
        function render(string $view, array $vars = []): string
        {
            function escape(string $s): string
            {
                return htmlspecialchars($s, ENT_QUOTES);
            }

            $wrap = function (string $html) use ($view): string {
                return "<div class=\"$view\">$html</div>";
            };
            $upper = fn(string $s): string => strtoupper($s);

            return $wrap($upper(escape(implode(',', $vars))));
        }
    }

    $handler = new class(10) extends Handler implements \JsonSerializable {
        public function __construct(private int $limit)
        {
        }

        public function jsonSerialize(): mixed
        {
            return ['limit' => $this->limit];
        }
    };
}

namespace {

    #[Attribute(Attribute::TARGET_CLASS)]
    final class Route
    {
        public function __construct(public string $path)
        {
        }
    }

    function global_helper(): void
    {
        \Legacy\Helpers\render('home');
    }
}
