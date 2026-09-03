import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

export class KVLiteError extends Error {}
export class NotFoundError extends KVLiteError {}
export class InvalidArgumentError extends KVLiteError {}
export class StorageError extends KVLiteError {}
export class SerializationError extends KVLiteError {}
export class NativeLibraryError extends KVLiteError {}

function nativeError(error) {
  if (error instanceof KVLiteError) return error;
  const message = error instanceof Error ? error.message : String(error);
  switch (error?.code) {
    case 'KVLITE_NOT_FOUND': return new NotFoundError(message, { cause: error });
    case 'KVLITE_INVALID_ARGUMENT': return new InvalidArgumentError(message, { cause: error });
    case 'KVLITE_NATIVE_LIBRARY': return new NativeLibraryError(message, { cause: error });
    default: return new StorageError(message, { cause: error });
  }
}

function assertKey(key) {
  const value = Buffer.isBuffer(key) ? key : key instanceof Uint8Array ? Buffer.from(key) : typeof key === 'string' ? Buffer.from(key) : null;
  if (!value || value.length === 0) {
    throw new InvalidArgumentError('KVLite key must be a non-empty string, Buffer, or Uint8Array.');
  }
  return value;
}

function assertTTL(ttlSeconds) {
  if (!Number.isSafeInteger(ttlSeconds) || ttlSeconds < 0) {
    throw new InvalidArgumentError('KVLite TTL must be zero or a positive integer number of seconds.');
  }
}

function encode(value) {
  try {
    const json = JSON.stringify(value);
    if (json === undefined) {
      throw new TypeError('undefined is not JSON-serializable');
    }
    return Buffer.from(json);
  } catch (error) {
    throw new SerializationError(`KVLite value is not JSON-serializable: ${error.message}`, { cause: error });
  }
}

function decode(value) {
  try {
    return JSON.parse(value.toString('utf8'));
  } catch (error) {
    throw new SerializationError(`KVLite returned invalid JSON: ${error.message}`, { cause: error });
  }
}

function libraryName() {
  if (process.platform === 'win32') return 'kvlite.dll';
  if (process.platform === 'darwin') return 'libkvlite.dylib';
  return 'libkvlite.so';
}

function target() {
  const platform = process.platform === 'win32' ? 'windows' : process.platform;
  const architecture = process.arch === 'x64' ? 'amd64' : process.arch === 'arm64' ? 'arm64' : process.arch;
  return `${platform}-${architecture}`;
}

function soleBundledLibrary(home, library) {
  try {
    const candidates = fs.readdirSync(path.join(home, 'drivers'), { withFileTypes: true })
      .filter((entry) => entry.isDirectory())
      .map((entry) => path.join(home, 'drivers', entry.name, 'lib', library))
      .filter((candidate) => fs.existsSync(candidate) && fs.statSync(candidate).isFile());
    return candidates.length === 1 ? candidates[0] : undefined;
  } catch {
    return undefined;
  }
}

export function findLibrary(requestedPath, driver) {
  const candidates = [requestedPath, process.env.KVLITE_LIBRARY_PATH];
  if (process.env.KVLITE_HOME) {
    if (driver) candidates.push(path.join(process.env.KVLITE_HOME, 'drivers', driver, 'lib', libraryName()));
    const bundled = soleBundledLibrary(process.env.KVLITE_HOME, libraryName());
    if (bundled) candidates.push(bundled);
    candidates.push(path.join(process.env.KVLITE_HOME, 'lib', libraryName()));
  }
  candidates.push(path.join(packageRoot, 'native', target(), libraryName()));
  if (driver) candidates.push(path.resolve(packageRoot, '../../../dist/dev', target(), 'drivers', driver, 'lib', libraryName()));
  candidates.push(path.resolve(packageRoot, '../../../dist/dev', target(), 'lib', libraryName()));
  const found = candidates.find((candidate) => candidate && fs.existsSync(candidate) && fs.statSync(candidate).isFile());
  if (found) return path.resolve(found);
  throw new NativeLibraryError(
    `KVLite native library was not found. Set KVLITE_LIBRARY_PATH to libkvlite or install a matching KVLite native bundle. Looked in:\n${candidates.filter(Boolean).map((candidate) => `  - ${candidate}`).join('\n')}`,
  );
}

function loadAddon() {
  try {
    return require('../build/Release/kvlite_native.node');
  } catch (error) {
    throw new NativeLibraryError(
      `KVLite's N-API extension is unavailable. Run \`npm run build:native\` with node-gyp, or use connect() for remote mode. ${error.message}`,
      { cause: error },
    );
  }
}

export class NativeDatabase {
  #addon;
  #handle;

  constructor(addon, handle) {
    this.#addon = addon;
    this.#handle = handle;
  }

  static open(databasePath, options = {}) {
    if (typeof databasePath !== 'string' || databasePath.length === 0) {
      throw new InvalidArgumentError('KVLite database path is required.');
    }
    const { libraryPath } = options;
    const hasDriver = options.driver !== undefined;
    const hasBackend = options.backend !== undefined;
    let driver = hasDriver ? options.driver : hasBackend ? options.backend : 'rocksdb';
    if (typeof driver !== 'string' || !(driver = driver.trim().toLowerCase())) {
      throw new InvalidArgumentError('KVLite storage driver is required.');
    }
    if (hasDriver && hasBackend) {
      const legacyBackend = options.backend;
      if (typeof legacyBackend !== 'string' || !(legacyBackend.trim())) {
        throw new InvalidArgumentError('KVLite storage backend is required.');
      }
      if (legacyBackend.trim().toLowerCase() !== driver) {
        throw new InvalidArgumentError('KVLite driver and backend options select different storage drivers.');
      }
    }
    const explicitDriver = hasDriver || (hasBackend && driver !== 'rocksdb');
    const addon = loadAddon();
    try {
      const library = findLibrary(libraryPath, driver);
      const handle = explicitDriver
        ? addon.openWithBackend(databasePath, driver, library)
        : addon.open(databasePath, library);
      return new NativeDatabase(addon, handle);
    } catch (error) {
      throw nativeError(error);
    }
  }

  put(key, value, { ttlSeconds = 0 } = {}) {
    this.#assertOpen();
    assertTTL(ttlSeconds);
    try {
      this.#addon.put(this.#handle, assertKey(key), encode(value), ttlSeconds);
    } catch (error) {
      throw nativeError(error);
    }
  }

  get(key) {
    this.#assertOpen();
    try {
      return decode(this.#addon.get(this.#handle, assertKey(key)));
    } catch (error) {
      if (error instanceof SerializationError) throw error;
      throw nativeError(error);
    }
  }

  putBytes(key, value, { ttlSeconds = 0 } = {}) {
    this.#assertOpen();
    assertTTL(ttlSeconds);
    if (!(Buffer.isBuffer(value) || value instanceof Uint8Array)) {
      throw new InvalidArgumentError('KVLite binary value must be a Buffer or Uint8Array.');
    }
    try {
      this.#addon.put(this.#handle, assertKey(key), Buffer.from(value), ttlSeconds);
    } catch (error) {
      throw nativeError(error);
    }
  }

  getBytes(key) {
    this.#assertOpen();
    try {
      return this.#addon.get(this.#handle, assertKey(key));
    } catch (error) {
      throw nativeError(error);
    }
  }

  delete(key) {
    this.#assertOpen();
    try {
      this.#addon.delete(this.#handle, assertKey(key));
    } catch (error) {
      throw nativeError(error);
    }
  }

  close() {
    if (this.#handle === null) return;
    try {
      this.#addon.close(this.#handle);
      this.#handle = null;
    } catch (error) {
      throw nativeError(error);
    }
  }

  #assertOpen() {
    if (this.#handle === null) throw new StorageError('KVLite database is closed.');
  }
}

export class HttpDatabase {
  constructor(baseUrl, { token, timeoutMs = 30_000, driver, fetch: fetchImpl = globalThis.fetch } = {}) {
    let parsed;
    try {
      parsed = new URL(baseUrl);
    } catch {
      throw new InvalidArgumentError('KVLite remote URL must be a valid http(s) URL.');
    }
    if (!['http:', 'https:'].includes(parsed.protocol)) {
      throw new InvalidArgumentError('KVLite remote URL must be a valid http(s) URL.');
    }
    if (!Number.isSafeInteger(timeoutMs) || timeoutMs <= 0) {
      throw new InvalidArgumentError('KVLite HTTP timeout must be positive.');
    }
    if (driver !== undefined && (typeof driver !== 'string' || !(driver = driver.trim().toLowerCase()))) {
      throw new InvalidArgumentError('KVLite remote driver must be a non-empty string when provided.');
    }
    if (typeof fetchImpl !== 'function') {
      throw new StorageError('No fetch implementation is available for KVLite remote mode.');
    }
    this.baseUrl = baseUrl.replace(/\/$/, '');
    this.token = token;
    this.driver = driver;
    this.timeoutMs = timeoutMs;
    this.fetch = fetchImpl;
  }

  async put(key, value, { ttlSeconds = 0 } = {}) {
    const rawKey = assertKey(key);
    assertTTL(ttlSeconds);
    let endpoint = `/v1/entries/${rawKey.toString('base64url')}`;
    if (ttlSeconds) endpoint += `?ttl_seconds=${ttlSeconds}`;
    const response = await this.#request('PUT', endpoint, encode(value));
    if (response.status !== 204) throw await this.#responseError(response);
  }

  async get(key) {
    const rawKey = assertKey(key);
    const response = await this.#request('GET', `/v1/entries/${rawKey.toString('base64url')}`);
    if (response.status === 404) throw new NotFoundError('KVLite key was not found.');
    if (response.status !== 200) throw await this.#responseError(response);
    return decode(Buffer.from(await response.arrayBuffer()));
  }

  async delete(key) {
    const rawKey = assertKey(key);
    const response = await this.#request('DELETE', `/v1/entries/${rawKey.toString('base64url')}`);
    if (response.status !== 204) throw await this.#responseError(response);
  }

  close() {}

  async #request(method, endpoint, body) {
    const headers = { Accept: 'application/json' };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (this.token) headers.Authorization = `Bearer ${this.token}`;
    if (this.driver) headers['X-KVLite-Driver'] = this.driver;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);
    try {
      return await this.fetch(this.baseUrl + endpoint, { method, headers, body, signal: controller.signal });
    } catch (error) {
      throw new StorageError(`KVLite HTTP request failed: ${error.message}`, { cause: error });
    } finally {
      clearTimeout(timer);
    }
  }

  async #responseError(response) {
    const body = (await response.text()).trim();
    return new StorageError(body || `KVLite HTTP request failed with status ${response.status}.`);
  }
}

export const KVLite = {
  open: NativeDatabase.open,
  connect(baseUrl, options) { return new HttpDatabase(baseUrl, options); },
};

export const open = NativeDatabase.open;
export function connect(baseUrl, options) { return new HttpDatabase(baseUrl, options); }
