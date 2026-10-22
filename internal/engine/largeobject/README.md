# internal/engine/largeobject

Immutable sparse large-object bytes and bounded generated replay shape recognition.
The package has no engine, session, mutex, allocator or publication authority.

`Value` owns a signed64-bit EOF and private2048-byte pages. Missing pages and
short page tails below EOF denote zero bytes. Write copies the page index and
only touched pages, preserves unmodified prefixes/suffixes, and shares immutable
untouched pages. Empty writes validate seek without allocating or extending EOF.
Read clips at EOF, validates materialized size before seek diagnostics, then
constructs one owned string. Neither bounded reads nor writes allocate holes.
The maximum size is4398046509056; maximum materialized bytes1073741819.
`Chunks` borrows immutable stored slices; consumers must not modify them.

Catalog descriptors own values. The engine journals descriptor replacements,
captures query-entry READ versus command-visible mutation catalogs, enforces
owner/SELECT/UPDATE rights and publishes metadata/content together. VOLATILE SQL
body activation obtains a fresh immutable command-visible READ capture through
existing lexical scratch, then restores its caller's capture on success/error.
Rollback/savepoints select old descriptors; this package adds no second store.
Dump visits stored chunks, including terminal zero bytes, without copying them.
The protected `pg_largeobject` producer also enumerates these same chunks,
preserving sparse/short page shape rather than synthesizing logical-read zeros.
Catalog SELECT is an independent relation ACL, not an LO object read privilege.

`RestoresBytes` and `ACLRepair` admit only generated qualified fixed-OID literal
create/put and one-object typed raw-ACL repair shapes. Parameters, nested queries,
arbitrary functions, labels, widened predicates and multiple assignments do not
qualify. Principal resolution, ACL codecs, private replay authority and atomic
installation stay in existing host/control seams.

## Testing

Follow the [shared testing guide](../../../docs/TESTING.md). Package-function
units supplement, never replace, public dual-mode SQL/control scenarios:

```sh
make check RUN='^TestLargeObject(SparseReadWrite|ImmutablePages|SizeBounds|ReplayShapes)$'
make check RUN='^TestLargeObject(DescriptorAuthority|PrincipalRemap)$'
```

Use external900-second deadlines. Public selected proof, race proof and standalone
smoke instructions are in [reference/object_grants](../../../reference/object_grants/README.md#transactional-large-object-byte-storage).
