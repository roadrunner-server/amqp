<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker\Exception;

final class UnsupportedVersionException extends VersionCheckerException
{
    /**
     * @param non-empty-string $message
     * @param non-empty-string $installed
     * @param non-empty-string $requested
     */
    public function __construct(
        string $message,
        private string $installed,
        private string $requested
    ) {
        parent::__construct($message);
    }

    /**
     * @return non-empty-string
     */
    public function getInstalledVersion(): string
    {
        return $this->installed;
    }

    /**
     * @return non-empty-string
     */
    public function getRequestedVersion(): string
    {
        return $this->requested;
    }
}
