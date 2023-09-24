<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker;

use RoadRunner\VersionChecker\Exception\RequiredVersionException;
use RoadRunner\VersionChecker\Exception\RoadrunnerNotInstalledException;
use RoadRunner\VersionChecker\Exception\UnsupportedVersionException;
use RoadRunner\VersionChecker\Version\Comparator;
use RoadRunner\VersionChecker\Version\ComparatorInterface;
use RoadRunner\VersionChecker\Version\Installed;
use RoadRunner\VersionChecker\Version\InstalledInterface;
use RoadRunner\VersionChecker\Version\Required;
use RoadRunner\VersionChecker\Version\RequiredInterface;

final class VersionChecker
{
    private InstalledInterface $installedVersion;
    private RequiredInterface $requiredVersion;
    private ComparatorInterface $comparator;

    public function __construct(
        InstalledInterface $installedVersion = null,
        RequiredInterface $requiredVersion = null,
        ComparatorInterface $comparator = null
    ) {
        $this->installedVersion = $installedVersion ?? new Installed();
        $this->requiredVersion = $requiredVersion ?? new Required();
        $this->comparator = $comparator ?? new Comparator();
    }

    /**
     * @param non-empty-string|null $version
     *
     * @throws UnsupportedVersionException
     * @throws RoadrunnerNotInstalledException
     * @throws RequiredVersionException
     */
    public function greaterThan(?string $version = null): void
    {
        if (empty($version)) {
            $version = $this->requiredVersion->getRequiredVersion();
        }

        if (empty($version)) {
            throw new RequiredVersionException(
                'Unable to determine required RoadRunner version.' .
                ' Please specify the required version in the `$version` parameter.'
            );
        }

        $installedVersion = $this->installedVersion->getInstalledVersion();

        if (!$this->comparator->greaterThan($version, $installedVersion)) {
            throw new UnsupportedVersionException($this->getFormattedMessage(
                'Installed RoadRunner version `%s` not supported. Requires version `%s` or higher.',
                $installedVersion,
                $version
            ), $installedVersion, $version);
        }
    }

    /**
     * @param non-empty-string $version
     *
     * @throws UnsupportedVersionException
     * @throws RoadrunnerNotInstalledException
     */
    public function lessThan(string $version): void
    {
        $installedVersion = $this->installedVersion->getInstalledVersion();

        if (!$this->comparator->lessThan($version, $installedVersion)) {
            throw new UnsupportedVersionException($this->getFormattedMessage(
                'Installed RoadRunner version `%s` not supported. Requires version `%s` or lower.',
                $installedVersion,
                $version
            ), $installedVersion, $version);
        }
    }

    /**
     * @param non-empty-string $version
     *
     * @throws UnsupportedVersionException
     * @throws RoadrunnerNotInstalledException
     */
    public function equal(string $version): void
    {
        $installedVersion = $this->installedVersion->getInstalledVersion();

        if (!$this->comparator->equal($version, $installedVersion)) {
            throw new UnsupportedVersionException($this->getFormattedMessage(
                'Installed RoadRunner version `%s` not supported. Requires version `%s`.',
                $installedVersion,
                $version
            ), $installedVersion, $version);
        }
    }

    /**
     * @param non-empty-string $message
     * @param non-empty-string $installedVersion
     * @param non-empty-string $version
     *
     * @return non-empty-string
     */
    private function getFormattedMessage(string $message, string $installedVersion, string $version): string
    {
        \preg_match('/\bv?(\d+)\.(\d+)\.(\d+)\b/', $version, $matches);

        if (!empty($matches[0])) {
            $version = $matches[1] . '.' . $matches[2] . '.' . $matches[3];
        }

        /** @var non-empty-string $msg */
        $msg = \sprintf($message, $installedVersion, $version);

        return $msg;
    }
}
