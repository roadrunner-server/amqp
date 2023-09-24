<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker\Version;

interface ComparatorInterface
{
    /**
     * @param non-empty-string $requested
     * @param non-empty-string $installed
     */
    public function greaterThan(string $requested, string $installed): bool;

    /**
     * @param non-empty-string $requested
     * @param non-empty-string $installed
     */
    public function lessThan(string $requested, string $installed): bool;

    /**
     * @param non-empty-string $requested
     * @param non-empty-string $installed
     */
    public function equal(string $requested, string $installed): bool;
}
