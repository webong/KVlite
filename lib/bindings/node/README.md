# `@webong/kvlite` for Node.js

`@webong/kvlite` offers an embedded N-API extension and a pure-JavaScript HTTP
client:

- `open()` loads `libkvlite` into the current Node.js process, analogous to a
  native SQLite binding.
- `connect()` uses KVLite's public JSON/HTTP API and needs no native library.

One process must own an embedded RocksDB directory. For clustered Node workers
or several applications, start `kvlite serve` once and use `connect()` (or a
standard Redis package against KVLite's optional Redis endpoint).

## Install

Until the package is published to npm, clone the public repository and build
the package in place:

```bash
git clone https://github.com/webong/KVlite.git
npm --prefix KVlite/lib/bindings/node run build:native
```

The npm package itself is rooted at `lib/bindings/node`; publishing will make
the normal package name `@webong/kvlite` available.

## Embedded use

The first install/source build compiles the small N-API loader with `node-gyp`.
It dynamically loads KVLite's separate shared library, so point it at the
matching release artifact:

```bash
export KVLITE_LIBRARY_PATH=/opt/kvlite/lib/libkvlite.dylib # .so on Linux
npm run build:native
```

```js
import { open } from '@webong/kvlite';

const db = open('./data');
db.put('user:101', { id: 101, name: 'Ada' }, { ttlSeconds: 3600 });
console.log(db.get('user:101'));
db.close();
```

`NativeDatabase` also provides `putBytes()` and `getBytes()` for applications
that own their own binary codec.

## Remote use

```js
import { connect } from '@webong/kvlite';

const db = connect('http://127.0.0.1:8089', { token: process.env.KVLITE_TOKEN });
await db.put('user:101', { id: 101, name: 'Ada' });
```

Remote mode uses Node 18+ `fetch` and KVLite's stable JSON protocol; it needs
no N-API extension or RocksDB installation.

## Test

```bash
npm test
```

The suite compiles a tiny ABI-compatible C mock, builds the N-API extension
against the running Node headers, and exercises the actual loader without a
RocksDB installation.
