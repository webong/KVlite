# KVLite standalone-extension release handoff

**Prepared:** 2026-09-04  
**Repository baseline inspected:** `main` at `c842cbe` (`origin/main`), clean
before this handoff document was added

> **Status 2026-09-05: closed.** Every phase below landed on `main`
> (commits `20a4cee` and `6941ce9`, pushed). What changed versus the plan:
> Phase 1–2 shipped as specified, plus a latent layering bug the work exposed
> (engine operations routed through logical C ABI calls; fixed with additive
> raw `kvlite_raw_*` symbols). Phase 4 shipped as an HTTP-owner/attached-Redis
> topology with CLI orchestration instead of a new IPC wire protocol.
> Phase 5 shipped as the frozen `kvlite_module_init_v1` ABI
> (`capi/kvlite_module.h`) with a pure-C reference module. Self-contained
> installs shipped as `--bundle-runtime` plus license-notice gating.
> One behavior the plan assumed did not survive contact with reality:
> loaded Go shared libraries are never unloaded (a Go runtime cannot be torn
> down safely; `dlclose` hung the process), so module libraries stay mapped
> for the process lifetime. Residual, all tracked outside this document:
> CI runs the new suites (wired, awaiting runner proof), the first real
> RocksDB runtime bundle is unproven until native CI runs it, Windows runtime
> bundling is explicitly unsupported, and release signing (beyond checksums)
> is still open.

## Purpose

Finish KVLite's extension-first distribution model without changing the
embedded-first product contract:

- core opens no storage engine and starts no listener by default;
- RocksDB, LevelDB, and Berkeley DB are storage-driver extensions;
- HTTP and Redis are separately installed protocol extensions;
- a host discovers only explicit, checksummed module directories at runtime;
- users install only the driver and protocol artifacts they need.

The immediate deliverable is release packaging and installation for standalone
HTTP and Redis modules. A true in-process native-extension ABI and a
multi-protocol shared-owner IPC design are deliberately separate milestones.

## Architectural decisions already made

| Area | Agreed contract |
| --- | --- |
| Core | `github.com/webong/kvlite` remains embeddable. It has no default linked driver or network listener. |
| Driver choice | `WithDriver("rocksdb"|"leveldb"|"berkeleydb")` selects the engine when a local DB is first opened. `KVLITE-MANIFEST.json` persists that choice and rejects a different engine on reopen. Engine directories are never interchangeable. |
| Driver distribution | Every driver is an extension under `extensions/`. A packaged driver currently exposes a checksummed `c-shared` KVLite ABI bundle, loaded through the module driver loader. |
| Protocol distribution | HTTP and Redis are extensions, never a mandatory core dependency. A standalone module is an executable discovered from an explicit module directory and launched with `kvlite module run`. |
| Remote driver selection | A remote client may request a driver by name. The server resolves only its installed/server-owned driver mappings; it must return a clear unavailable/not-exposed error, and must never accept a client filesystem path. |
| Discovery and integrity | `KVLITE_MODULE_PATH`, `KVLITE_HOME/modules`, and `KVLITE_HOME/drivers` are the only default search locations. `kvlite-module.json` is schema/module-ABI validated; artifacts need SHA-256 verification before execution/loading. |
| Berkeley DB | It remains a separately licensed, explicitly enabled CGo extension. Do not publish its native runtime as part of a normal KVLite release. |

The distinction below must remain clear in code and documentation:

```text
C embedding ABI (capi/kvlite.h, v1)    -> functions an embedding process calls
Module ABI (kvlite-module.json, v1)    -> discovery/package compatibility
```

## What is implemented now

### Module catalog and driver loading

- `module.go` discovers manifests without executing them, validates paths and
  ABI metadata, resolves platform artifacts, and verifies SHA-256 hashes.
- `driver_module_loader.go` opens a discovered driver `c-shared` artifact with
  `dlopen`/`LoadLibrary`, requires the KVLite C ABI symbols, and opens the
  driver through that artifact. This is a real runtime driver load path, not a
  Go `plugin`.
- `backend.go` falls back to that loader when the requested driver is not
  linked into the calling process.
- `cmd/kvlite` can list, verify, and execute installed executable modules:
  `kvlite module list`, `kvlite module verify [name]`, and
  `kvlite module run <name> -- <args>`.

### Source extensions and CLI behavior

- All optional source modules are in `extensions/`: `rocksdb`, `leveldb`,
  `berkeleydb`, `http`, and `redis`.
- HTTP and Redis have standalone entrypoints at
  `extensions/http/cmd/kvlite-http` and
  `extensions/redis/cmd/kvlite-redis`.
- A `kvlite` binary built with `kvlite_no_linked_extensions` can use
  `serve --extension-mode=auto` to find and start a standalone HTTP or Redis
  executable. `linked` and `standalone` modes are also explicit.
- The default CLI build still links HTTP and Redis via
  `cmd/kvlite/linked_extensions_enabled.go`. This is convenient for source
  development, but it is **not** the final extension-only release profile.

### Existing release builder

`scripts/build-release.sh` currently builds one driver-specific bundle at:

```text
dist/<version>/<os>-<arch>/drivers/<driver>/
  bin/kvlite                 # optional component
  lib/libkvlite.<suffix>     # optional component
  include/kvlite.h           # with c-shared
  kvlite-module.json
  SHA256SUMS
```

It supports RocksDB and LevelDB by default, and Berkeley DB only with explicit
`--allow-berkeleydb` / `ALLOW_BERKELEYDB_BUNDLE=1`. The manifest is generated
with artifact checksums.

## Gaps and source/documentation mismatches

1. **No release artifact exists for HTTP or Redis yet.** Their source
   manifests intentionally have no `artifacts` entries, so they can be linked
   in Go but cannot currently pass installed-module verification.
2. **The release CLI links HTTP and Redis by default.**
   `scripts/build-release.sh` does not add `kvlite_no_linked_extensions`, so a
   normal driver CLI bundle is not yet protocol-extension-free.
3. **The HTTP/Redis extension READMEs incorrectly promise private local IPC.**
   Their standalone commands currently call `kvlite.Open(path, ...)` and own
   that directory themselves. There is no IPC owner/client protocol today.
   `MODULES.md` describes the current direct-owner behavior correctly.
4. **Two separate standalone transports cannot safely own the same directory.**
   Run one standalone HTTP *or* one standalone Redis process per database.
   The CLI rejects HTTP+Redis in standalone mode for this reason. A shared
   database owner plus IPC is a later design, not an implied implementation.
5. **A `native-module` manifest kind exists only as a reserved contract.**
   There is no `kvlite_module_init_v1` implementation. Do not replace the
   working executable/C-shared approach with Go runtime plugins.
6. **Native release bundles are not self-contained installers.** RocksDB,
   Berkeley DB, and compression runtime libraries still require bundling,
   loader-path relocation, notices, and signed release provenance before
   clean-machine distribution is claimed.

## Recommended delivery plan

### Phase 1 — package protocol executables (next task)

Make HTTP and Redis installable executable modules using the existing manifest
and `module run` contracts.

1. Extend `scripts/build-release.sh` with an explicit component selector for
   protocol modules, preferably `--extension http|redis`. Keep it mutually
   exclusive with `--driver` so the release intent is unambiguous.
2. Build the selected nested-module command from its own Go module:

   ```text
   extensions/http/cmd/kvlite-http
   extensions/redis/cmd/kvlite-redis
   ```

   These executables should contain only core plus their selected protocol
   implementation. They should not statically link RocksDB, LevelDB, Berkeley
   DB, or the other protocol extension.
3. Emit a generated installed manifest with the source module's identity and
   capability metadata plus a single executable artifact and SHA-256:

   ```text
   dist/<version>/<target>/modules/http/
     bin/kvlite-http[.exe]
     kvlite-module.json
     SHA256SUMS

   dist/<version>/<target>/modules/redis/
     bin/kvlite-redis[.exe]
     kvlite-module.json
     SHA256SUMS
   ```

   `KVLITE_HOME=<install-root>` then discovers protocols in
   `<install-root>/modules/*` and drivers in `<install-root>/drivers/*` with
   the current `DefaultModulePaths` implementation.
4. Build driver CLIs with `kvlite_no_linked_extensions` in the release flow.
   The release CLI should be a small host/runner and dynamically launch a
   verified HTTP or Redis executable in `auto` or `standalone` mode.
   Keep the linked convenience build available only as an explicit development
   or opt-in release choice.
5. Preserve the current runtime relationship: a protocol executable finds the
   requested driver's installed C-shared module and opens it through
   `driver_module_loader.go`. This requires CGO-enabled native builds because
   the loader uses the system dynamic-library API.

### Phase 2 — make the release flow testable

Add a small, native LevelDB integration test or test script. It must:

1. build a LevelDB driver `c-shared` bundle plus a protocol executable bundle;
2. assemble them beneath a temporary `KVLITE_HOME` using the layout above;
3. prove `kvlite module list` discovers metadata without executing artifacts;
4. prove `kvlite module verify leveldb`, `http`, and `redis` verifies all
   generated artifact checksums;
5. start HTTP and Redis separately against fresh temporary LevelDB paths,
   then check an HTTP put/get and Redis `PING`/set/get; and
6. prove a missing requested driver returns the expected actionable error.

Use separate database directories for HTTP and Redis in this test. Socket
tests may need to run outside restrictive local sandboxes, but they belong in
native CI.

Keep existing unit tests that use fake executable artifacts: they protect the
security properties of discovery, checksumming, and argument forwarding but
do not prove a release artifact can open a real driver.

### Phase 3 — reconcile public docs and metadata

Update these documents in the same change as the release builder:

- `MODULES.md`: move HTTP/Redis out of the “future release transition” text
  and show the installed layout and commands.
- `extensions/http/README.md` and `extensions/redis/README.md`: remove the
  false IPC claim. State plainly that standalone mode owns one local database
  process and HTTP+Redis need separate paths today.
- `README.md`, `extensions/README.md`, `lib/README.md`, and
  `lib/manifest.json`: describe extension bundles as available only after the
  packaging implementation lands; do not promise standalone artifacts early.
- `Makefile`: add focused release targets such as `release-http` and
  `release-redis`, and a LevelDB-only release integration target. Preserve the
  explicit Berkeley DB gate.

### Phase 4 — optional later design: shared-owner IPC

Only start this phase if HTTP and Redis must serve *the same* local database
simultaneously while remaining separate executables. It requires a real local
IPC contract, authentication/ownership lifecycle, crash/restart semantics,
and tests. Do not claim it by documentation alone.

The design should make one host process own the driver directory; protocol
modules connect to that host. It must not let a remote client select arbitrary
filesystem paths or dynamically load unverified modules.

### Phase 5 — optional later design: native in-process extension ABI

If direct in-process loading of HTTP/Redis/other module code is needed, define
and freeze a small C entrypoint (for example `kvlite_module_init_v1`) with
explicit lifecycle, capability negotiation, and ABI compatibility tests.
Continue to use manifests and checksum verification. Go's `plugin` package is
not an acceptable public extension mechanism because it is toolchain and build
identity coupled.

## Suggested first commands for the next agent

```bash
git status --short
git log --oneline -n 5
go test . ./cmd/kvlite ./extensions/leveldb/... ./extensions/http/... ./extensions/redis/...
sed -n '1,320p' scripts/build-release.sh
sed -n '1,260p' MODULES.md
```

Then implement Phase 1 and Phase 2 together, keeping the generated release
manifest format compatible with `ModuleManifest.Validate`, `Module.Verify`,
and `ResolveModuleExecutable`.

## Acceptance checklist for the next change

- [ ] `make release-http` and `make release-redis` (or equivalent explicit
      commands) produce checksummed installed executable-module layouts.
- [ ] Driver release CLI binaries carry `kvlite_no_linked_extensions` unless a
      linked-protocol profile was explicitly requested.
- [ ] A protocol executable dynamically opens an installed LevelDB driver
      bundle without importing the LevelDB Go module itself.
- [ ] `kvlite module verify` catches a modified HTTP, Redis, or driver
      executable/library before it is run or loaded.
- [ ] HTTP and Redis each work as the sole owner of a fresh database path.
- [ ] Documentation says “direct owner” until actual IPC exists.
- [ ] Default core and default release driver surfaces remain embedded-first
      and do not start network listeners.
- [ ] Berkeley DB remains excluded from normal public/native release bundles.

## Files most likely to change

```text
scripts/build-release.sh
Makefile
cmd/kvlite/linked_extensions_enabled.go
cmd/kvlite/main_test.go
module_test.go
MODULES.md
README.md
extensions/README.md
extensions/http/README.md
extensions/redis/README.md
lib/README.md
lib/manifest.json
scripts/test-standalone-modules.sh            # new, if shell integration is preferred
```

Do not edit `capi/kvlite.h` or increment either ABI version for the packaging
work alone. That would create a compatibility change without a corresponding
runtime-contract need.
