<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker\Environment;

final class Native implements EnvironmentInterface
{
    public function __construct(
        private array $values = []
    ) {
        $this->values = $values + $_ENV + $_SERVER;
    }

    /**
     * @param non-empty-string $name
     */
    public function get(string $name, mixed $default = null): mixed
    {
        return $this->values[$name] ?? $default;
    }
}
