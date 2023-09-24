<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker\Process;

use Symfony\Component\Process\Exception\ProcessFailedException;

final class Process implements ProcessInterface
{
    public function exec(array $command): string
    {
        $process = new \Symfony\Component\Process\Process($command);
        $process->run();

        if (!$process->isSuccessful()) {
            throw new ProcessFailedException($process);
        }

        return $process->getOutput();
    }
}
