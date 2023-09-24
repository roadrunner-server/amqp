# RoadRunner VersionChecker

[![PHP Version Require](https://poser.pugx.org/roadrunner-php/version-checker/require/php)](https://packagist.org/packages/roadrunner-php/version-checker)
[![Latest Stable Version](https://poser.pugx.org/roadrunner-php/version-checker/v/stable)](https://packagist.org/packages/roadrunner-php/version-checker)
[![phpunit](https://github.com/roadrunner-php/version-checker/actions/workflows/phpunit.yml/badge.svg)](https://github.com/roadrunner-php/version-checker/actions)
[![psalm](https://github.com/roadrunner-php/version-checker/actions/workflows/psalm.yml/badge.svg)](https://github.com/roadrunner-php/version-checker/actions)
[![Codecov](https://codecov.io/gh/roadrunner-php/version-checker/branch/master/graph/badge.svg)](https://codecov.io/gh/roadrunner-php/version-checker)
[![Total Downloads](https://poser.pugx.org/roadrunner-php/version-checker/downloads)](https://packagist.org/roadrunner-php/version-checker/phpunit)
<a href="https://discord.gg/8bZsjYhVVk"><img src="https://img.shields.io/badge/discord-chat-magenta.svg"></a>

## Requirements

Make sure that your server is configured with following PHP version and extensions:

- PHP 8.0+

## Installation

You can install the package via composer:

```bash
composer require roadrunner-php/version-checker
```

## Usage

Use the `RoadRunner\VersionChecker\VersionChecker` methods to check the compatibility of the installed RoadRunner
version. The VersionChecker class has three public methods:

- **greaterThan** - Checks if the installed version of RoadRunner is **greater than or equal** to the specified version.
  If no version is specified, the minimum required version will be determined based on the minimum required version of
  the `spiral/roadrunner` package.
- **lessThan** - Checks if the installed version of RoadRunner is **less than or equal** to the specified version.
- **equal** - Checks if the installed version of RoadRunner is **equal** to the specified version.

All three methods throw an `RoadRunner\VersionChecker\Exception\UnsupportedVersionException` if the installed version
of RoadRunner does not meet the specified requirements. If RoadRunner is not installed, a
`RoadRunner\VersionChecker\Exception\RoadrunnerNotInstalledException` is thrown.

```php
use RoadRunner\VersionChecker\VersionChecker;
use RoadRunner\VersionChecker\Exception\UnsupportedVersionException;

$checker = new VersionChecker();

try {
    $checker->greaterThan('2023.1');
} catch (UnsupportedVersionException $exception) {
    var_dump($exception->getMessage()); // Installed RoadRunner version `2.12.3` not supported. Requires version `2023.1` or higher.
    var_dump($exception->getInstalledVersion()); // 2.12.3
    var_dump($exception->getRequestedVersion()); // 2023.1
}

try {
    $checker->lessThan('2.11');
} catch (UnsupportedVersionException $exception) {
    var_dump($exception->getMessage()); // Installed RoadRunner version `2.12.3` not supported. Requires version `2.11` or lower.
    var_dump($exception->getInstalledVersion()); // 2.12.3
    var_dump($exception->getRequestedVersion()); // 2.11
}

try {
    $checker->equal('2.11');
} catch (UnsupportedVersionException $exception) {
    var_dump($exception->getMessage()); // Installed RoadRunner version `2.12.3` not supported. Requires version `2.11`.
    var_dump($exception->getInstalledVersion()); // 2.12.3
    var_dump($exception->getRequestedVersion()); // 2.11
}
```

### Path to the RoadRunner binary

To configure the `VersionChecker` to search for the RoadRunner binary in a location other than the default
(application root with a rr filename), you can bind the `RoadRunner\VersionChecker\Version\InstalledInterface`
within application container using the `RoadRunner\VersionChecker\Version\Installed` class and passing the desired
file path as the **$executablePath** parameter. After that, you can retrieve the VersionChecker class from
application container.

Example with Spiral Framework container:

```php
use RoadRunner\VersionChecker\Version\InstalledInterface;
use RoadRunner\VersionChecker\Version\Installed;

$container->bindSingleton(InstalledInterface::class, new Installed(executablePath: 'some/path'));
$checker = $container->get(VersionChecker::class);
```

## Testing

```bash
composer test
```

```bash
composer psalm
```

```bash
composer cs
```

## License

The MIT License (MIT). Please see [License File](LICENSE) for more information.
