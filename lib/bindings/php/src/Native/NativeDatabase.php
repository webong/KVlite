<?php

declare(strict_types=1);

namespace Webong\KVLite\Native;

use FFI;
use FFI\CData;
use Throwable;
use Webong\KVLite\Exception\InvalidArgumentException;
use Webong\KVLite\Exception\NativeLibraryException;
use Webong\KVLite\Exception\NotFoundException;
use Webong\KVLite\Exception\StorageException;
use Webong\KVLite\JsonCodec;
use Webong\KVLite\Store;

/**
 * Thin PHP FFI adapter over capi/kvlite.h. It owns one native DB handle and
 * deliberately keeps serialization in PHP so its values stay portable.
 */
final class NativeDatabase implements Store
{
    private const ABI_VERSION = 1;
    private const STATUS_OK = 0;
    private const STATUS_NOT_FOUND = 1;
    private const STATUS_INVALID_ARGUMENT = 2;

    private FFI $ffi;
    private int $handle;

    private function __construct(FFI $ffi, int $handle)
    {
        $this->ffi = $ffi;
        $this->handle = $handle;
    }

    public static function open(string $path, ?string $libraryPath = null, string $backend = 'rocksdb', ?string $driver = null): self
    {
        if ($path === '') {
            throw new InvalidArgumentException('KVLite database path is required.');
        }
        $explicitDriver = $driver !== null;
        $backend = self::selectDriver($backend, $driver);
        $explicitDriver = $explicitDriver || $backend !== 'rocksdb';
        self::assertFfiIsAvailable();

        try {
            $resolvedLibrary = LibraryFinder::find($libraryPath, $backend);
            // Do not declare additive symbols here: PHP FFI resolves declared
            // functions at cdef time on some builds, so the default ABI v1 path
            // must still load an older libkvlite that lacks backend selection.
            $ffi = FFI::cdef(self::definitions(), $resolvedLibrary);
        } catch (Throwable $exception) {
            if ($exception instanceof NativeLibraryException) {
                throw $exception;
            }
            throw new NativeLibraryException('Unable to load KVLite native library: '.$exception->getMessage(), 0, $exception);
        }

        if ((int) $ffi->kvlite_abi_version() !== self::ABI_VERSION) {
            throw new NativeLibraryException(sprintf(
                'KVLite native ABI mismatch: this PHP package needs ABI %d.',
                self::ABI_VERSION,
            ));
        }

        $handle = $ffi->new('uint64_t');
        $outError = $ffi->new('char *');
        if (!$explicitDriver) {
            // Keep the long-standing v1 default path compatible with an older
            // native library that predates the additive backend-selection symbol.
            $status = (int) $ffi->kvlite_open($path, FFI::addr($handle), FFI::addr($outError));
        } else {
            try {
                $backendFfi = FFI::cdef(self::backendDefinitions(), $resolvedLibrary);
                $handle = $backendFfi->new('uint64_t');
                $outError = $backendFfi->new('char *');
                $status = (int) $backendFfi->kvlite_open_with_backend($path, $backend, FFI::addr($handle), FFI::addr($outError));
            } catch (Throwable $exception) {
                throw new NativeLibraryException(
                    'This KVLite native library does not support selecting a storage backend. Upgrade libkvlite.',
                    0,
                    $exception,
                );
            }
            self::throwForStatus($backendFfi, $status, $outError);
            return new self($ffi, (int) $handle->cdata);
        }
        self::throwForStatus($ffi, $status, $outError);

        return new self($ffi, (int) $handle->cdata);
    }

    public function put(string $key, mixed $value, int $ttlSeconds = 0): void
    {
        $this->putBytes($key, JsonCodec::encode($value), $ttlSeconds);
    }

    public function get(string $key): mixed
    {
        return JsonCodec::decode($this->getBytes($key));
    }

    /** Store raw bytes when an application needs its own codec. */
    public function putBytes(string $key, string $value, int $ttlSeconds = 0): void
    {
        $this->assertOpen();
        self::assertKey($key);
        self::assertTTL($ttlSeconds);
        [$keyPointer, $keyBuffer] = $this->bytes($key);
        [$valuePointer, $valueBuffer] = $this->bytes($value);
        $outError = $this->ffi->new('char *');
        $status = (int) $this->ffi->kvlite_put(
            $this->handle,
            $keyPointer,
            strlen($key),
            $valuePointer,
            strlen($value),
            $ttlSeconds,
            FFI::addr($outError),
        );
        self::throwForStatus($this->ffi, $status, $outError);
    }

    /** Return raw bytes when an application needs its own codec. */
    public function getBytes(string $key): string
    {
        $this->assertOpen();
        self::assertKey($key);
        [$keyPointer, $keyBuffer] = $this->bytes($key);
        $outValue = $this->ffi->new('void *');
        $outLength = $this->ffi->new('size_t');
        $outError = $this->ffi->new('char *');
        $status = (int) $this->ffi->kvlite_get(
            $this->handle,
            $keyPointer,
            strlen($key),
            FFI::addr($outValue),
            FFI::addr($outLength),
            FFI::addr($outError),
        );
        self::throwForStatus($this->ffi, $status, $outError);

        try {
            $length = (int) $outLength->cdata;
            return $length === 0 ? '' : FFI::string($outValue, $length);
        } finally {
            if (!FFI::isNull($outValue)) {
                $this->ffi->kvlite_free($outValue);
            }
        }
    }

    public function delete(string $key): void
    {
        $this->assertOpen();
        self::assertKey($key);
        [$keyPointer, $keyBuffer] = $this->bytes($key);
        $outError = $this->ffi->new('char *');
        $status = (int) $this->ffi->kvlite_delete(
            $this->handle,
            $keyPointer,
            strlen($key),
            FFI::addr($outError),
        );
        self::throwForStatus($this->ffi, $status, $outError);
    }

    public function close(): void
    {
        if ($this->handle === 0) {
            return;
        }
        $outError = $this->ffi->new('char *');
        $status = (int) $this->ffi->kvlite_close($this->handle, FFI::addr($outError));
        self::throwForStatus($this->ffi, $status, $outError);
        $this->handle = 0;
    }

    public function __destruct()
    {
        try {
            $this->close();
        } catch (Throwable) {
            // Destructors must never turn shutdown into a fatal error.
        }
    }

    /** @return array{0: CData|null, 1: CData|null} */
    private function bytes(string $value): array
    {
        if ($value === '') {
            return [null, null];
        }
        $buffer = $this->ffi->new('unsigned char['.strlen($value).']');
        FFI::memcpy($buffer, $value, strlen($value));

        return [$buffer, $buffer];
    }

    private static function throwForStatus(FFI $ffi, int $status, CData $outError): void
    {
        if ($status === self::STATUS_OK) {
            return;
        }
        $message = 'KVLite native operation failed.';
        if (!FFI::isNull($outError)) {
            try {
                $message = FFI::string($outError);
            } finally {
                $ffi->kvlite_free($outError);
            }
        }

        throw match ($status) {
            self::STATUS_NOT_FOUND => new NotFoundException($message),
            self::STATUS_INVALID_ARGUMENT => new InvalidArgumentException($message),
            default => new StorageException($message),
        };
    }

    private static function assertFfiIsAvailable(): void
    {
        if (!extension_loaded('FFI')) {
            throw new NativeLibraryException('PHP FFI is not installed. Use KVLite::connect() or enable ext-ffi for embedded mode.');
        }
        if (ini_get('ffi.enable') === '0') {
            throw new NativeLibraryException('PHP FFI is disabled. Enable it for this process (for example, php -d ffi.enable=1).');
        }
    }

    private static function assertKey(string $key): void
    {
        if ($key === '') {
            throw new InvalidArgumentException('KVLite key is required.');
        }
    }

    private static function assertTTL(int $ttlSeconds): void
    {
        if ($ttlSeconds < 0) {
            throw new InvalidArgumentException('KVLite TTL must be zero or a positive number of seconds.');
        }
    }

    private static function normalizeBackend(string $backend): string
    {
        $backend = strtolower(trim($backend));
        if ($backend === '') {
            throw new InvalidArgumentException('KVLite storage backend is required.');
        }

        return $backend;
    }

    private static function selectDriver(string $backend, ?string $driver): string
    {
        $legacyBackend = self::normalizeBackend($backend);
        if ($driver === null) {
            return $legacyBackend;
        }
        $selectedDriver = self::normalizeBackend($driver);
        if ($legacyBackend !== 'rocksdb' && $legacyBackend !== $selectedDriver) {
            throw new InvalidArgumentException('KVLite driver and backend options select different storage drivers.');
        }

        return $selectedDriver;
    }

    private function assertOpen(): void
    {
        if ($this->handle === 0) {
            throw new StorageException('KVLite database is closed.');
        }
    }

    private static function definitions(): string
    {
        // Windows LLP64 uses a 64-bit unsigned long long for size_t; Unix
        // release targets use unsigned long. KVLite currently ships 64-bit
        // targets only, matching the C release contract.
        $sizeT = PHP_OS_FAMILY === 'Windows'
            ? 'typedef unsigned long long size_t;'
            : 'typedef unsigned long size_t;';

        return <<<CDEF
typedef unsigned long long uint64_t;
typedef long long int64_t;
{$sizeT}
unsigned int kvlite_abi_version(void);
int kvlite_open(const char *path, uint64_t *out_handle, char **out_error);
int kvlite_close(uint64_t handle, char **out_error);
int kvlite_put(uint64_t handle, const void *key, size_t key_length,
               const void *value, size_t value_length, int64_t ttl_seconds,
               char **out_error);
int kvlite_get(uint64_t handle, const void *key, size_t key_length,
               void **out_value, size_t *out_length, char **out_error);
int kvlite_delete(uint64_t handle, const void *key, size_t key_length,
                  char **out_error);
void kvlite_free(void *pointer);
CDEF;
    }

    private static function backendDefinitions(): string
    {
        $sizeT = PHP_OS_FAMILY === 'Windows'
            ? 'typedef unsigned long long size_t;'
            : 'typedef unsigned long size_t;';

        return <<<CDEF
typedef unsigned long long uint64_t;
{$sizeT}
int kvlite_open_with_backend(const char *path, const char *backend,
                             uint64_t *out_handle, char **out_error);
void kvlite_free(void *pointer);
CDEF;
    }
}
