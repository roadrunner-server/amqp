<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker\Composer;

interface PackageInterface
{
    /**
     * @param non-empty-string $packageName
     * @return non-empty-string[]
     */
    public function getRequiredVersions(string $packageName): array;
}
