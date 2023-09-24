<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker\Version;

use RoadRunner\VersionChecker\Exception\RoadrunnerNotInstalledException;

interface InstalledInterface
{
    /**
     * @return non-empty-string
     *
     * @throws RoadrunnerNotInstalledException
     */
    public function getInstalledVersion(): string;
}
