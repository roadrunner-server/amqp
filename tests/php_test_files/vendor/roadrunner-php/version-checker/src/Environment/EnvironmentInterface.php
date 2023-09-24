<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker\Environment;

interface EnvironmentInterface
{
    /**
     * @param non-empty-string $name
     */
    public function get(string $name, mixed $default = null): mixed;
}
