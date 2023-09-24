<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker\Process;

interface ProcessInterface
{
    public function exec(array $command): string;
}
