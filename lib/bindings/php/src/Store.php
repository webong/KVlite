<?php

declare(strict_types=1);

namespace Webong\KVLite;

/** A JSON-value KVLite connection, whether embedded or remote. */
interface Store
{
    /**
     * Store a JSON-serializable value. A zero TTL means no expiry.
     *
     * @throws Exception\InvalidArgumentException
     * @throws Exception\StorageException
     */
    public function put(string $key, mixed $value, int $ttlSeconds = 0): void;

    /**
     * Read a JSON value. Stored JSON objects are returned as associative arrays.
     *
     * @throws Exception\NotFoundException
     * @throws Exception\StorageException
     */
    public function get(string $key): mixed;

    /** Delete a key. Deleting an absent key succeeds. */
    public function delete(string $key): void;

    /** Release native or transport resources. It is safe to call more than once. */
    public function close(): void;
}
