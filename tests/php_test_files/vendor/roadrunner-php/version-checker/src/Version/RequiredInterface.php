<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker\Version;

interface RequiredInterface
{
    /**
     * @return non-empty-string|null
     */
    public function getRequiredVersion(): ?string;
}
