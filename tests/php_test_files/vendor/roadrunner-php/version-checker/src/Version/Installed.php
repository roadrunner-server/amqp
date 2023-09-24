<?php

declare(strict_types=1);

namespace RoadRunner\VersionChecker\Version;

use RoadRunner\VersionChecker\Environment\EnvironmentInterface;
use RoadRunner\VersionChecker\Environment\Native;
use RoadRunner\VersionChecker\Exception\RoadrunnerNotInstalledException;
use RoadRunner\VersionChecker\Process\Process;
use RoadRunner\VersionChecker\Process\ProcessInterface;
use Symfony\Component\Process\Exception\ProcessFailedException;

final class Installed implements InstalledInterface
{
    private const ENV_VARIABLE = 'RR_VERSION';

    /**
     * @var non-empty-string|null
     */
    private static ?string $cachedVersion = null;

    private ProcessInterface $process;
    private EnvironmentInterface $environment;

    /**
     * @param non-empty-string $executablePath
     */
    public function __construct(
        ProcessInterface $process = null,
        EnvironmentInterface $environment = null,
        private string $executablePath = './rr'
    ) {
        $this->process = $process ?? new Process();
        $this->environment = $environment ?? new Native();
    }

    /**
     * @return non-empty-string
     *
     * @throws RoadrunnerNotInstalledException
     */
    public function getInstalledVersion(): string
    {
        if (!empty(self::$cachedVersion)) {
            return self::$cachedVersion;
        }

        if (!empty(self::$cachedVersion = $this->getVersionFromEnv())) {
            return self::$cachedVersion;
        }

        if (!empty(self::$cachedVersion = $this->getVersionFromConsoleCommand())) {
            return self::$cachedVersion;
        }

        throw new RoadrunnerNotInstalledException('Unable to determine RoadRunner version.');
    }

    /**
     * @return non-empty-string|null
     */
    private function getVersionFromEnv(): ?string
    {
        /** @var string|null $version */
        $version = $this->environment->get(self::ENV_VARIABLE);

        if (\is_string($version) && !empty($version)) {
            return $version;
        }

        return null;
    }

    /**
     * @return non-empty-string|null
     * @throws RoadrunnerNotInstalledException
     */
    private function getVersionFromConsoleCommand(): ?string
    {
        try {
            $output = $this->process->exec([$this->executablePath, '--version']);
        } catch (ProcessFailedException) {
            throw new RoadrunnerNotInstalledException(\sprintf(
                'Roadrunner is not installed. Make sure RoadRunner is installed and available here: `%s`.' .
                ' If RoadRunner is installed in a different path, pass the correct `executablePath` parameter to the' .
                ' `%s` class constructor.',
                $this->executablePath,
                self::class
            ));
        }

        \preg_match('/\bversion (\d+\.\d+\.\d+[\w.-]*)/', $output, $matches);

        if (isset($matches[1]) && !empty($matches[1])) {
            return $matches[1];
        }

        return null;
    }
}
