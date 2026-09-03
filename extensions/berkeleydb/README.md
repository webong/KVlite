# Berkeley DB driver extension

This is a catalog placeholder for KVLite's future Berkeley DB driver. It does
not contain Go source, a native library, or a release artifact, so
`kvlite.Open(..., kvlite.WithDriver("berkeleydb"))` correctly returns
`ErrDriverNotInstalled` today.

Berkeley DB distributions have licensing choices that are independent from
KVLite core, RocksDB, and LevelDB. A maintained implementation must be released
as its own extension package and native module bundle after its exact Berkeley
DB distribution, license obligations, build matrix, and ABI compatibility have
been documented. Installing that future extension must remain an explicit user
choice.
