import assert from 'node:assert/strict';
import { HttpDatabase, KVLite, NotFoundError } from '../src/index.js';

const database = KVLite.open('/tmp/kvlite-node-mock', { libraryPath: process.env.KVLITE_TEST_LIBRARY, driver: 'leveldb' });
database.put('user:101', { id: 101, name: 'Ada' }, { ttlSeconds: 60 });
assert.deepEqual(database.get('user:101'), { id: 101, name: 'Ada' });
database.putBytes(Buffer.from('binary\0key'), Buffer.from('value\0bytes'));
assert.deepEqual(database.getBytes(Buffer.from('binary\0key')), Buffer.from('value\0bytes'));
database.delete(Buffer.from('binary\0key'));
assert.throws(() => database.getBytes(Buffer.from('binary\0key')), NotFoundError);
database.close();

// The original embedded option remains valid for existing callers.
KVLite.open('/tmp/kvlite-node-legacy-backend', {
  libraryPath: process.env.KVLITE_TEST_LIBRARY,
  backend: 'leveldb',
}).close();

const requests = [];
const remote = new HttpDatabase('http://127.0.0.1:8089', {
  token: 'secret',
  driver: 'leveldb',
  fetch: async (url, options) => {
    requests.push([url, options]);
    if (options.method === 'GET') return new Response('{"enabled":true}', { status: 200 });
    return new Response(null, { status: 204 });
  },
});
await remote.put('flags:101', { enabled: true }, { ttlSeconds: 30 });
assert.deepEqual(await remote.get('flags:101'), { enabled: true });
await remote.delete('flags:101');
assert.equal(requests.length, 3);
assert.match(requests[0][0], /ttl_seconds=30/);
assert.equal(requests[0][1].headers.Authorization, 'Bearer secret');
assert.equal(requests[0][1].headers['X-KVLite-Driver'], 'leveldb');

console.log('Node binding tests passed');
