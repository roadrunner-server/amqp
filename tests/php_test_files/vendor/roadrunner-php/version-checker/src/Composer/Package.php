<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker\Composer;

use Composer\InstalledVersions;
use Composer\Semver\VersionParser;

final class Package implements PackageInterface
{
    /**
     * @param non-empty-string $packageName
     * @return non-empty-string[]
     */
    public function getRequiredVersions(string $packageName): array
    {
        $versions = [];
        foreach (InstalledVersions::getInstalledPackages() as $package) {
            $path = InstalledVersions::getInstallPath($package);
            if ($path !== null && \file_exists($path . '/composer.json')) {
                /** @var array{require?: array<non-empty-string, non-empty-string>} $composerJson */
                $composerJson = \json_decode(\file_get_contents($path . '/composer.json'), true);

                if (
                    isset($composerJson['require'][$packageName]) &&
                    $this->isSupportedVersion($composerJson['require'][$packageName])
                ) {
                    $versions[] = $this->getMinVersion($composerJson['require'][$packageName]);
                }
            }
        }

        return $versions;
    }

    /**
     * @param non-empty-string $version
     */
    private function isSupportedVersion(string $version): bool
    {
        $parser = new VersionParser();

        try {
            $parser->parseConstraints($version);
        } catch (\Throwable) {
            return false;
        }

        return !\str_starts_with($version, 'dev-');
    }

    /**
     * @param non-empty-string $version
     *
     * @return non-empty-string
     */
    private function getMinVersion(string $version): string
    {
        $parser = new VersionParser();

        $constraint = $parser->parseConstraints($version);

        /** @var non-empty-string $min */
        $min = $constraint->getLowerBound()->getVersion();

        return $min;
    }
}
