<?php

declare(strict_types=1);

namespace Webong\KVLite;

use JsonException;
use Webong\KVLite\Exception\SerializationException;

final class JsonCodec
{
    public static function encode(mixed $value): string
    {
        try {
            return json_encode(
                $value,
                JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE,
            );
        } catch (JsonException $exception) {
            throw new SerializationException('KVLite value is not JSON-serializable: '.$exception->getMessage(), 0, $exception);
        }
    }

    public static function decode(string $payload): mixed
    {
        try {
            return json_decode($payload, true, 512, JSON_THROW_ON_ERROR);
        } catch (JsonException $exception) {
            throw new SerializationException('KVLite returned invalid JSON: '.$exception->getMessage(), 0, $exception);
        }
    }
}
