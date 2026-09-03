<?php

declare(strict_types=1);

namespace Webong\KVLite;

use Webong\KVLite\Native\NativeDatabase;

/** Entry points that mirror the familiar SQLite open/connect split. */
final class KVLite
{
    public static function open(string $path, ?string $libraryPath = null, string $backend = 'rocksdb', ?string $driver = null): NativeDatabase
    {
        return NativeDatabase::open($path, $libraryPath, $backend, $driver);
    }

    public static function connect(string $baseUrl, ?string $token = null, int $timeoutSeconds = 30, ?string $driver = null): HttpDatabase
    {
        return new HttpDatabase($baseUrl, $token, $timeoutSeconds, driver: $driver);
    }
}
