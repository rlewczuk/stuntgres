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
- `Truncate` retains lower sparse pages and replaces only the endpoint payload,
  zero-filling extension without allocating gaps or copying discarded pages.
  Every endpoint is stored, including zero-length pages at aligned EOF; empty
  interior pages survive later writes and extensions. An equal aligned EOF
  still creates its endpoint if absent.
- `Read` clips at EOF, validates the materialized size before seek
  diagnostics, then constructs one owned string. Neither bounded reads nor
  writes allocate holes.
- The maximum size is 4398046509056 bytes (`MaximumSize`); the maximum
  materialized read is 1073741819 bytes (`MaximumRead`).
- `Chunks` borrows immutable stored slices; consumers must not modify them.
- `Validate` accepts empty stored pages through page 2147483647, requires every
  extent to fit EOF and requires the greatest stored extent to equal EOF.
  The zero value remains valid without a page; truncating to zero stores page 0.

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
literal create/put shapes, aligned `lo_truncate64(lo_open(...))` endpoints,
and one-object typed raw-ACL repairs. Endpoint replay requires literal WRITE
mode and exact qualified OID/INT4/INT8 casts with no call decorations.
Parameters, arbitrary nesting, labels, widened SELECT envelopes/predicates
and multiple assignments do not qualify. Principal resolution, ACL codecs,
statement-local replay authority and atomic installation stay in the engine's
host and `control` seams.

## Testing

Package units cover sparse reads, writes and truncation, endpoint/page
immutability, size bounds, validation and bounded replay shapes; engine units
cover descriptor authority, cursor/journal ownership and principal remapping.

```sh
make check RUN='^TestLargeObject'
```

Public byte/descriptor SQL scenarios are in
[reference/object_grants](../../../reference/object_grants/README.md#testing);
truncate and its native replay scenarios are owned by
[reference/large_objects](../../../reference/large_objects/README.md#testing).
