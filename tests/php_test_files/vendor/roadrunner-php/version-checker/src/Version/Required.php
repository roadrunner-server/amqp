<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker\Version;

use Composer\Semver\Comparator as SemverComparator;
use RoadRunner\VersionChecker\Composer\Package;
use RoadRunner\VersionChecker\Composer\PackageInterface;

final class Required implements RequiredInterface
{
    private const ROADRUNNER_PACKAGE = 'spiral/roadrunner';

    /**
     * @var non-empty-string|null
     */
    private static ?string $cachedVersion = null;

    private PackageInterface $package;

    public function __construct(PackageInterface $package = null)
    {
        $this->package = $package ?? new Package();
    }

    /**
     * @return non-empty-string|null
     */
    public function getRequiredVersion(): ?string
    {
        if (self::$cachedVersion !== null) {
            return self::$cachedVersion;
        }

        foreach ($this->package->getRequiredVersions(self::ROADRUNNER_PACKAGE) as $packageVersion) {
            self::$cachedVersion = $this->getMaximumVersion($packageVersion, self::$cachedVersion);
        }

        return self::$cachedVersion;
    }

    /**
     * @param non-empty-string $version
     * @param non-empty-string|null $previous
     * @return non-empty-string
     */
    private function getMaximumVersion(string $version, ?string $previous = null): string
    {
        if ($previous === null) {
            return $version;
        }

        return SemverComparator::greaterThan($version, $previous) ? $version : $previous;
    }
}
