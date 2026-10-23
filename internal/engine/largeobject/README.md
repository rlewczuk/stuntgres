# internal/engine/largeobject

Immutable sparse large-object bytes and bounded recognition of generated
replay shapes. The package has no engine, session, mutex, allocator or
publication authority.

## Values

`Value` owns a signed 64-bit EOF and private 2048-byte pages (`PageSize`).
Missing pages and short page tails below EOF denote zero bytes.

- `Write` copies the page index and only the touched pages, preserves
  unmodified prefixes and suffixes, and shares immutable untouched pages.
  Empty writes validate the seek without allocating or extending EOF.
- `Read` clips at EOF, validates the materialized size before seek
  diagnostics, then constructs one owned string. Neither bounded reads nor
  writes allocate holes.
- The maximum size is 4398046509056 bytes (`MaximumSize`); the maximum
  materialized read is 1073741819 bytes (`MaximumRead`).
- `Chunks` borrows immutable stored slices; consumers must not modify them.

## Ownership outside the package

Catalog descriptors own values. The engine journals descriptor replacements,
captures query-entry READ versus command-visible mutation catalogs, enforces
owner/SELECT/UPDATE rights and publishes metadata and content together.
VOLATILE SQL body activation obtains a fresh immutable command-visible READ
capture through the lexical scratch, then restores its caller's capture on
success or error. Rollback and savepoints select old descriptors; there is no
second store.

Dump visits stored chunks, including terminal zero bytes, without copying
them. The protected `pg_largeobject` producer enumerates the same chunks,
preserving sparse and short page shape rather than synthesizing logical-read
zeros. Catalog SELECT on it is an independent relation ACL, not a large-object
read privilege.

## Replay shapes

`RestoresBytes` and `ACLRepair` admit only generated, qualified, fixed-OID
literal create/put shapes and one-object typed raw-ACL repair shapes.
Parameters, nested queries, arbitrary functions, labels, widened predicates
and multiple assignments do not qualify. Principal resolution, ACL codecs,
private replay authority and atomic installation stay in the engine's host and
`control` seams.

## Testing

Package units cover sparse reads and writes, page immutability, size bounds
and replay shapes; engine units cover descriptor authority and principal
remapping.

```sh
make check RUN='^TestLargeObject'
```

Public SQL and control scenarios are in
[reference/object_grants](../../../reference/object_grants/README.md#testing).
