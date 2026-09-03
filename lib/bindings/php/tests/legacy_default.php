<?php

declare(strict_types=1);

use Webong\KVLite\KVLite;

$package = dirname(__DIR__);
spl_autoload_register(static function (string $class) use ($package): void {
    $prefix = 'Webong\\KVLite\\';
    if (!str_starts_with($class, $prefix)) {
        return;
    }
    $path = $package.'/src/'.str_replace('\\', '/', substr($class, strlen($prefix))).'.php';
    if (is_file($path)) {
        require $path;
    }
});

$library = $argv[1] ?? '';
if ($library === '') {
    throw new RuntimeException('legacy mock library path is required');
}

// This mock intentionally does not export kvlite_open_with_backend. Default
// RocksDB opening must remain valid for an older ABI v1 native library.
$database = KVLite::open('/tmp/kvlite-php-legacy-mock', $library);
$database->put('legacy', ['ok' => true]);
if ($database->get('legacy') !== ['ok' => true]) {
    throw new RuntimeException('legacy ABI default open round trip failed');
}
$database->close();

fwrite(STDOUT, "PHP legacy ABI default-open test passed\n");
