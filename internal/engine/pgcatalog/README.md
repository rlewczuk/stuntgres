# internal/engine/pgcatalog

Pure row producers and descriptor specifications for `pg_catalog` and
producer-backed `information_schema` relations. Core owns source admission,
capture and runtime state; this package projects explicit captured values.

## Producer contract

`Producer(Input)` returns owned typed rows or an error. Input supplies context,
paired catalog/role roots, exact owner/source/ref, a Runtime capability, captured
presentation/path and optional detached runtime facts/Keep restriction.

Validate actual source identity, owner/domain/epoch, paired roles and source-owned
type/attribute generations before publishing rows, including empty results.
Current source and backing-routine authorization is independent of captured row
values; Prepare/Describe and retained definitions cache no permission.

Build cells from the captured declarations and real descriptor metadata, preserving
NULL versus empty/zero and immutable generation ownership. Returned rows survive
later catalog replacement and changes. Runtime facts arrive as detached values,
never provider/session/writer handles. Absence of a physical facility is not
permission to synthesize worker, connection, storage or WAL evidence.

`Keep` restricts only its designated producer-owned key. Apply it before expensive
row construction when safe, without evaluating arbitrary predicates or retaining
a previous execution's parameter/authority decision.

## Registry

One ordered producer registry and `Specs` own source identity and schema
registration. Lookup uses that registry, not a second core dispatcher. Definition
and bootstrap metadata follow their canonical owners; same-spelled SQL objects
do not impersonate registered sources.

## Runtime capability

`Runtime` supplies focused captured facts and nonexecuting definition presentation.
Core's `catalogrt.Runtime` implements it through engine-owned Host adapters.
Projection does not open sources, run SQL, initialize physical state, create a
statistics snapshot or acquire engine/session authority itself. Catalog capture
and unlocked materialization remain distinct phases.

## Exported helpers

Shared declaration/formal, relation-kind, constraint, dependency and name
projections serve catalog producers and inquiry/display owners. They consume
actual captured descriptors and preserve stable attributes, declared order,
nullable metadata and exact endpoint identities rather than reparsing SQL.

## Testing

Pure registry/projection helpers are tested here without an engine. Producer
fixtures requiring genuine seeded catalog/type/role authority live in
`enginetests` and core. Assert owned rows and rejection of stale/foreign sources
separately from public SQL, which belongs to reference packages under
[docs/TESTING.md](../../../docs/TESTING.md).
