# KVLite packaging contract

This directory holds the starting points for system package managers
(Homebrew, Debian, and friends). KVLite is built for this model: a small
extension-free host binary plus versioned, checksummed artifact directories
that are discovered at runtime. A package only moves files into place and
points discovery at them; no binary patching is ever required.

## The fast path: online installer

Most users should never touch what follows. Published releases carry
per-target tarballs plus a versioned installer script:

```sh
curl -fsSL https://github.com/webong/KVlite/releases/latest/download/kvlite-installer.sh | bash
```

Useful flags: `--version`, `--driver leveldb|rocksdb`, `--prefix`,
`--no-http`, `--no-redis`, `--shell-rc`, `--yes`. The installer detects the
platform, verifies the tarball checksum before unpacking, installs through
the same `scripts/install.sh` path below, verifies every module, and prints
the one export it needs (`KVLITE_SYSTEM_MODULE_PATH`). A tampered download
is rejected before anything executes. `make test-install-online` covers the
whole flow against a local asset server, including tamper rejection.

## Package split

One installable package per bundle kind, so users install only what they run:

| Package (example name) | Contents | Depends on |
| --- | --- | --- |
| `kvlite-leveldb` | `bin/kvlite`, driver bundle, `include/kvlite.h` | nothing (pure Go) |
| `kvlite-rocksdb` | same shape for RocksDB | distro RocksDB/compression libs, or the `--bundle-runtime` output |
| `kvlite-http` | `bin/kvlite-http` + bundle | any one `kvlite-*` driver package |
| `kvlite-redis` | `bin/kvlite-redis` + bundle | any one `kvlite-*` driver package |

Berkeley DB is never a normal package: it stays a separately licensed,
explicitly enabled build (see `extensions/berkeleydb`).

## Layout

Install release trees with `scripts/install.sh`, which enforces this layout
and keeps every bundle-internal path relative (safe for `DESTDIR` staging
and relocated prefixes):

```text
<prefix>/bin/kvlite{,-http,-redis}   # symlinks into the catalog
<prefix>/lib/kvlite/drivers/*/       # full driver bundles, intact
<prefix>/lib/kvlite/modules/*/       # full protocol bundles, intact
<prefix>/include/kvlite.h            # reviewed C ABI header
<prefix>/share/doc/kvlite/<bundle>/  # third-party notices per bundle
```

Only one CLI can own `bin/kvlite`. When several driver packages are
installed, pick the primary with `install.sh --link-cli`; the others remain
reachable through `kvlite module run` (or the manager's alternatives
system, e.g. Debian `update-alternatives`).

Build, then install from source with:

```sh
make release RELEASE_VERSION=v0.1.0 DRIVER=leveldb
make release-http release-redis RELEASE_VERSION=v0.1.0
make install INSTALL_PREFIX=/usr/local INSTALL_VERSION=v0.1.0
```

Packagers staging a package pass `DESTDIR=/tmp/stage make install`; the
script verifies the assembled tree through the same discovery a user gets
whenever a driver CLI is present.

## Discovery

Point the catalog root at the install with the system tier:

```sh
export KVLITE_SYSTEM_MODULE_PATH="<prefix>/lib/kvlite"
```

Search order is `KVLITE_MODULE_PATH`, `KVLITE_HOME/{modules,drivers}`,
then `KVLITE_SYSTEM_MODULE_PATH`, so a user install always shadows the
system one. Daemons and services without a login shell should get the
variable from the service definition (systemd `Environment=`, launchd
`EnvironmentVariables`) rather than shell init. The grouping directories
`drivers/` and `modules/` are part of the discovery contract: a catalog
root is usable as a single search path (see `MODULES.md`).

## Build rules for recipes

- Build natively per OS/arch; never cross-compile. The release scripts
  refuse cross builds because RocksDB is a native dependency.
- `CGO_ENABLED=1` is required (the driver loader uses the system dynamic
  loader).
- Stamp the release version through (`make release RELEASE_VERSION=...`);
  manifests and `SHA256SUMS` carry it.
- Run `kvlite module verify <name>` against the staged tree after
  installing; `install.sh` already does this unless a driver package lands
  separately, in which case the last package installed must verify the
  assembled tree.

## Obligations that stay with the publisher

- **Notices.** `--bundle-runtime` refuses to build without `--notice-file`
  entries. Collect them with `scripts/collect-runtime-notices.sh` and review
  the set before publishing; distro compression-library texts in particular
  need a human pass per release.
- **Provenance.** Manifest checksums prove integrity (nothing runs or loads
  before `verify` passes), not provenance. The manager's own signatures
  cover transport; repository-level release signing is still open.
- **RocksDB range.** Stay within the tested `v10.8.3`–`v10.10.1` range or
  re-run `make test-rocksdb-compat` for the pinned version first.
