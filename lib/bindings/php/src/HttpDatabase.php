<?php

declare(strict_types=1);

namespace Webong\KVLite;

use Webong\KVLite\Exception\InvalidArgumentException;
use Webong\KVLite\Exception\NotFoundException;
use Webong\KVLite\Exception\StorageException;

/** Pure-PHP client for the public JSON/HTTP KVLite protocol. */
final class HttpDatabase implements Store
{
    /** @var (callable(string, string, ?string, array<int, string>): array{0: int, 1: string})|null */
    private $requester;

    private readonly ?string $driver;

    public function __construct(
        private readonly string $baseUrl,
        private readonly ?string $token = null,
        private readonly int $timeoutSeconds = 30,
        ?callable $requester = null,
        ?string $driver = null,
    ) {
        $parts = parse_url($baseUrl);
        if (!is_array($parts) || !isset($parts['scheme'], $parts['host']) || !in_array($parts['scheme'], ['http', 'https'], true)) {
            throw new InvalidArgumentException('KVLite remote URL must be a valid http(s) URL.');
        }
        if ($timeoutSeconds <= 0) {
            throw new InvalidArgumentException('KVLite HTTP timeout must be positive.');
        }
        if ($driver !== null) {
            $driver = strtolower(trim($driver));
            if ($driver === '') {
                throw new InvalidArgumentException('KVLite remote driver must be a non-empty string when provided.');
            }
        }
        $this->requester = $requester;
        $this->driver = $driver;
    }

    public function put(string $key, mixed $value, int $ttlSeconds = 0): void
    {
        self::assertKey($key);
        if ($ttlSeconds < 0) {
            throw new InvalidArgumentException('KVLite TTL must be zero or a positive number of seconds.');
        }
        $path = '/v1/entries/'.self::encodedKey($key);
        if ($ttlSeconds > 0) {
            $path .= '?ttl_seconds='.$ttlSeconds;
        }
        [$status, $body] = $this->request('PUT', $path, JsonCodec::encode($value));
        if ($status !== 204) {
            throw self::responseError($status, $body);
        }
    }

    public function get(string $key): mixed
    {
        self::assertKey($key);
        [$status, $body] = $this->request('GET', '/v1/entries/'.self::encodedKey($key));
        if ($status === 404) {
            throw new NotFoundException('KVLite key was not found.');
        }
        if ($status !== 200) {
            throw self::responseError($status, $body);
        }

        return JsonCodec::decode($body);
    }

    public function delete(string $key): void
    {
        self::assertKey($key);
        [$status, $body] = $this->request('DELETE', '/v1/entries/'.self::encodedKey($key));
        if ($status !== 204) {
            throw self::responseError($status, $body);
        }
    }

    public function close(): void
    {
        // PHP stream transports have no persistent resource to close.
    }

    /** @return array{0: int, 1: string} */
    private function request(string $method, string $path, ?string $body = null): array
    {
        $headers = ['Accept: application/json'];
        if ($body !== null) {
            $headers[] = 'Content-Type: application/json';
        }
        if ($this->token !== null && $this->token !== '') {
            $headers[] = 'Authorization: Bearer '.$this->token;
        }
        if ($this->driver !== null) {
            $headers[] = 'X-KVLite-Driver: '.$this->driver;
        }
        $url = rtrim($this->baseUrl, '/').$path;

        if ($this->requester !== null) {
            $response = ($this->requester)($method, $url, $body, $headers);
            if (!is_array($response) || !isset($response[0], $response[1])) {
                throw new StorageException('KVLite test transport returned an invalid response.');
            }

            return [(int) $response[0], (string) $response[1]];
        }

        $context = stream_context_create([
            'http' => [
                'method' => $method,
                'header' => implode("\r\n", $headers),
                'content' => $body ?? '',
                'ignore_errors' => true,
                'timeout' => $this->timeoutSeconds,
            ],
        ]);
        $response = @file_get_contents($url, false, $context);
        $status = self::statusCode($http_response_header ?? []);
        if ($response === false && $status === 0) {
            throw new StorageException('KVLite HTTP request failed.');
        }

        return [$status, $response === false ? '' : $response];
    }

    /** @param array<int, string> $headers */
    private static function statusCode(array $headers): int
    {
        foreach ($headers as $header) {
            if (preg_match('/^HTTP\/\\S+\\s+(\\d{3})/', $header, $matches) === 1) {
                return (int) $matches[1];
            }
        }

        return 0;
    }

    private static function encodedKey(string $key): string
    {
        return rtrim(strtr(base64_encode($key), '+/', '-_'), '=');
    }

    private static function assertKey(string $key): void
    {
        if ($key === '') {
            throw new InvalidArgumentException('KVLite key is required.');
        }
    }

    private static function responseError(int $status, string $body): StorageException
    {
        $message = trim($body);
        if ($message === '') {
            $message = 'KVLite HTTP request failed with status '.$status.'.';
        }

        return new StorageException($message);
    }
}
