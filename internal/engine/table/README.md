# internal/engine/table

Table records and decisions below core: definition/column/constraint staging,
ALTER preparation, dependencies, inheritance, maintenance and native SQL
presentation. It sits above index and below partition/view/DML/control.

## What it owns

CREATE/ALTER metadata decisions and ordered provider phases; immutable rename/
layout candidates; constraint/default/generated recipes; dependency/drop
preparation; inheritance relationships; maintenance validation and dump records.
These consume actual captured descriptors, stable attributes and typed recipes,
not an alternate mutable catalog or statement executor.

## Hosts

`Host` supplies shared DDL/catalog/namespace/type capabilities; `AlterHost` adds
the current ALTER input, captures and stage. Read refreshed host inputs when a
capture changes them. Methods run with caller-owned Engine.Mu, never take/release
it or wait. Detached/waiting dependency controllers require nothing held; core
and tablecmds own those orchestration windows.

## Inheritance attach and detach fences

Relationship candidates retain complete lexical endpoints, original descriptors
and exact current-stage topology. Names and numeric constraint equality do not
establish ancestry; column/constraint origins retain immediate-parent identity.
Inherited recipe remap preserves its genuine defining provider/dependencies and
original source occurrence instead of recapturing them as fresh authority.

## Invariants

- Preserve ALTER's ordered metadata/provider phases and per-node reentry so
  first error is deterministic. Prepare complete checked metadata before physical
  callbacks, and retain original tuple layouts and publication fences.
- Constraint definition/build/scan order is explicit. Typed row conversion and
  row validation keep their specified order; no publication occurs until all
  required work validates. Failure preserves identities, rows and roots.
- Stable physical attributes are not dense projection slots. Layout evolution
  retains surviving IDs and genuine storage/index generation provenance.
- Prepared DROP captures immutable admitted closure and replays ordinary semantic
  retirement/effects; it does not grant authority from names or bypass dependency
  revalidation.
- Canonical dump order never depends on maps. Restore shape parsers stay beside
  emitters and replay ordinary typed binding/ownership/validation, not blanket
  privileged SQL or promoted constraint validity.
- No package-owned engine state or mutex; stage and publication lifetime belong
  to orchestration. Metadata/presentation never evaluates a stored recipe.

## What stays in the engine

`tablecmds` owns statement sequencing, lock windows and family hosts. Core owns
private catalog/rows/index journals, waits, row runtime, trigger/constraint effects,
transaction completion and atomic publication.

## Testing

Pure decisions use fake hosts or small genuine catalog fixtures. Engine tests
live in tablecmds/core and their owner fixtures. Public SQL belongs to reference
table/inheritance/maintenance packages under
[docs/TESTING.md](../../../docs/TESTING.md).
