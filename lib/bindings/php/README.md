# `webong/kvlite` for PHP

`webong/kvlite` gives PHP the same two choices as a SQLite-style library:

- `KVLite::open()` loads the local `libkvlite` shared library and opens the
  selected local backend in the current PHP process.
- `KVLite::connect()` speaks KVLite's public JSON/HTTP API to an owner process.

Use embedded mode only when this PHP process is the sole owner of the database
directory. For PHP-FPM workers, queues, or several applications, run
`kvlite serve` once and use `connect()` (or any Redis client against KVLite's
optional Redis endpoint).

## Install

Until the package is published on Packagist, use it from a clone as a Composer
path repository:

```json
{
  "repositories": [{"type": "path", "url": "../KVlite/lib/bindings/php", "options": {"symlink": true}}],
  "require": {"webong/kvlite": "dev-main"}
}
```

The package source is in `lib/bindings/php`; publishing will make the
`webong/kvlite` Composer package.

## Embedded use

Build or download the matching `libkvlite` first, then point PHP at it:

```bash
export KVLITE_LIBRARY_PATH=/opt/kvlite/lib/libkvlite.dylib # .so on Linux
php -d ffi.enable=1 app.php
```

```php
use Webong\KVLite\KVLite;

$db = KVLite::open(__DIR__.'/data', driver: 'leveldb');
$db->put('user:101', ['id' => 101, 'name' => 'Ada'], ttlSeconds: 3600);
$user = $db->get('user:101'); // associative array
$db->close();
```

`ext-ffi` must be enabled for the process. PHP commonly has `ffi.enable`
disabled for web SAPIs; do not enable FFI for untrusted code. In a production
web deployment, the remote or Redis mode is usually the better boundary.

`NativeDatabase` also exposes `putBytes()` and `getBytes()` for applications
that use MessagePack, protobuf, or another binary codec.

Without a selector, `KVLite::open()` uses the native bundle's default driver:
RocksDB for a RocksDB bundle or LevelDB for a LevelDB-only bundle. Set
`driver: 'leveldb'` when this process creates the local owner. `backend:`
remains a compatible embedded-call alias. Explicit selection requires a
current `libkvlite` that exports `kvlite_open_with_driver` (or
`kvlite_open_with_backend`).

## Remote use

```php
$db = KVLite::connect(
    'http://127.0.0.1:8089',
    token: getenv('KVLITE_TOKEN'),
    driver: 'leveldb',
);
$db->put('user:101', ['id' => 101, 'name' => 'Ada']);
```

Remote mode has no native dependency and stores JSON values through the stable
`/v1/entries/{base64url-key}` API. The server validates the selected driver
against its installed, server-owned driver/path mappings.

## Test

```bash
composer test
```

The test suite compiles a tiny C ABI mock and exercises the real PHP FFI
bridge; it does not need RocksDB installed.
