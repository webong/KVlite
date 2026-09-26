# KVLite

KVLite offers one embedded key-value interface with independently installable capabilities.

## Language

**Driver**:
An implementation inside an extension that provides an engine or transport capability. One extension may contain drivers for both.
_Avoid_: Extension as a synonym for the implementation

**Engine**:
The underlying storage technology that owns records and an on-disk format, such as RocksDB or LMDB.
_Avoid_: Transport

**Transport**:
A client-facing communication protocol for accessing a KVLite database owned by a process, such as HTTP or Redis RESP.
_Avoid_: Engine

**Extension**:
An independently installable KVLite product containing one or more drivers. It may provide an engine, a transport, or both.
_Avoid_: Driver as a synonym for every extension
