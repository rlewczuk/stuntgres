# internal/engine/subscription

Subscription option/connection validation and immutable metadata staging below
core. `Stage` consumes the positioned parser node and a `ddl.Host` extension;
`schemacmds` owns operation capture, lock admission and publication. This package
never publishes roots, takes locks, changes settings or uses external I/O.

The host enters with the cluster mutex held. Each operation builds a complete
candidate before replacing a descriptor. Publications are ordered remote names,
not local publication or relation dependencies. Connection parsing implements
release-18 libpq lexical validation, including URI decoding and the complete
keyword inventory, without defaults, environment/files, DNS or sockets. The
original connection text is retained, not reconstructed from parsed values.

Option and decoded connection-key names use exact membership. DefElem conversion
keeps numeric and string arguments distinct: scanner int32 values are normalized
for Boolean/string conversion, while Float and quoted-string spellings remain
textual. URI decoding scans left to right so the first lexical error wins;
literal edge spaces are ignored but percent-decoded spaces remain data.

ENABLE/DISABLE store desired metadata only; two-phase activation remains pending
until a real external synchronization mechanism exists. Effective connected
CREATE and publication refresh require publisher connections; remote failover,
two_phase=false and non-NULL-slot DROP require slot mechanisms. Valid demands
for those mechanisms fail explicitly with 0A000 before any publication. SKIP is
a declared LSN, never a claim of applied or skipped replication work. No workers,
slots, synchronization mappings or progress are synthesized.

Disconnected NULL-slot DROP uses the ordinary provider retirement path, so a
successful DROP removes both metadata and the database-scoped name before a
subsequent CREATE. Duplicate-option diagnostics reuse the AST's already
one-based Unicode character position without a second cursor adjustment.

## Testing

Public direct/prepared scenarios live in `reference/catalogs`; pure option,
conninfo and slot validation units live here, stager authority units beside core,
and parser syntax units beside the parser. Use `make check RUN='^Test(ReferenceSubscription|Subscription)'`, adding
`REFERENCE=1` for pinned PostgreSQL and `RACE=1` for selected concurrency proof.
