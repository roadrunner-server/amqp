<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker\Version;

use Composer\Semver\Comparator as SemverComparator;
use Composer\Semver\VersionParser;

final class Comparator implements ComparatorInterface
{
    private VersionParser $parser;

    public function __construct(VersionParser $parser = null)
    {
        $this->parser = $parser ?? new VersionParser();
    }

    /**
     * @param non-empty-string $requested
     * @param non-empty-string $installed
     */
    public function greaterThan(string $requested, string $installed): bool
    {
        return SemverComparator::greaterThanOrEqualTo(
            $this->parser->normalize($installed),
            $this->parser->normalize($requested)
        );
    }

    /**
     * @param non-empty-string $requested
     * @param non-empty-string $installed
     */
    public function lessThan(string $requested, string $installed): bool
    {
        return SemverComparator::lessThanOrEqualTo(
            $this->parser->normalize($installed),
            $this->parser->normalize($requested)
        );
    }

    /**
     * @param non-empty-string $requested
     * @param non-empty-string $installed
     */
    public function equal(string $requested, string $installed): bool
    {
        return SemverComparator::equalTo(
            $this->parser->normalize($installed),
            $this->parser->normalize($requested)
        );
    }
}
