<?php

declare(strict_types=1);

namespace Webong\KVLite\Native;

use Webong\KVLite\Exception\NativeLibraryException;

final class LibraryFinder
{
    public static function find(?string $requestedPath = null, ?string $driver = null): string
    {
        $candidates = [];
        foreach ([$requestedPath, getenv('KVLITE_LIBRARY_PATH') ?: null] as $candidate) {
            if (is_string($candidate) && $candidate !== '') {
                $candidates[] = $candidate;
            }
        }

        $libraryName = self::libraryName();
        $home = getenv('KVLITE_HOME');
        if (is_string($home) && $home !== '') {
            $home = rtrim($home, DIRECTORY_SEPARATOR);
            if ($driver !== null) {
                $candidates[] = $home.DIRECTORY_SEPARATOR.'drivers'.DIRECTORY_SEPARATOR.$driver.DIRECTORY_SEPARATOR.'lib'.DIRECTORY_SEPARATOR.$libraryName;
            }
            $soleBundle = self::soleDriverBundle($home, $libraryName);
            if ($soleBundle !== null) {
                $candidates[] = $soleBundle;
            }
            $candidates[] = $home.DIRECTORY_SEPARATOR.'lib'.DIRECTORY_SEPARATOR.$libraryName;
        }

        $packageRoot = dirname(__DIR__, 2);
        $target = self::target();
        $candidates[] = $packageRoot.'/native/'.$target.'/'.$libraryName;

        // This last candidate makes a source checkout convenient after
        // `make release RELEASE_VERSION=dev`, without affecting installed
        // Composer packages.
        $repositoryRoot = dirname(__DIR__, 5);
        if ($driver !== null) {
            $candidates[] = $repositoryRoot.'/dist/dev/'.$target.'/drivers/'.$driver.'/lib/'.$libraryName;
        }
        $candidates[] = $repositoryRoot.'/dist/dev/'.$target.'/lib/'.$libraryName;

        foreach ($candidates as $candidate) {
            if (is_file($candidate) && is_readable($candidate)) {
                return $candidate;
            }
        }

        $displayed = array_map(static fn (string $path): string => "  - {$path}", array_unique($candidates));
        throw new NativeLibraryException(
            "KVLite native library was not found. Set KVLITE_LIBRARY_PATH to libkvlite or install a matching KVLite native bundle. Looked in:\n"
            .implode("\n", $displayed),
        );
    }

    private static function libraryName(): string
    {
        return match (PHP_OS_FAMILY) {
            'Windows' => 'kvlite.dll',
            'Darwin' => 'libkvlite.dylib',
            default => 'libkvlite.so',
        };
    }

    private static function soleDriverBundle(string $home, string $libraryName): ?string
    {
        $pattern = $home.DIRECTORY_SEPARATOR.'drivers'.DIRECTORY_SEPARATOR.'*'.DIRECTORY_SEPARATOR.'lib'.DIRECTORY_SEPARATOR.$libraryName;
        $bundles = array_values(array_filter(
            glob($pattern) ?: [],
            static fn (string $path): bool => is_file($path) && is_readable($path),
        ));

        return count($bundles) === 1 ? $bundles[0] : null;
    }

    private static function target(): string
    {
        $os = match (PHP_OS_FAMILY) {
            'Windows' => 'windows',
            'Darwin' => 'darwin',
            default => 'linux',
        };
        $arch = match (strtolower(php_uname('m'))) {
            'x86_64', 'amd64' => 'amd64',
            'aarch64', 'arm64' => 'arm64',
            default => strtolower(php_uname('m')),
        };

        return $os.'-'.$arch;
    }
}
