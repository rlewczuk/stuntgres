# internal/engine/ddl

Shared locked catalog-staging contract: authority, stage, allocator and host
capabilities used by feature decisions. The package sits below statement families
and never owns an Engine, statement scope, mutex or publication.

## What it owns

- `Authority` is an opaque per-call value combining `access.Environment` with a
  marker. Host operations return changed authority by value; handlers retain no
  engine-side authorization implementation.
- `Stage` accepts private Create/Replace/Remove changes. Complete validation and
  atomic publication belong to its engine owner.
- `Allocator` supplies object identity and revision in the accepted operation's
  defined order. `TypeStaging` handles associated types and creation bookkeeping.
- `Host` supplies captured views, namespace/name/type/routine resolution, stage
  access and diagnostics. Feature hosts embed it and declare only additional
  family capabilities, not another authority protocol.
- Shared helpers pair checks with the authority's own environment, retain actual
  object references, and attach command annotations without granting publication
  authority. Schema-level pure decisions and stagers use the same private stage.

## Lock rule

Every Host method runs with caller-owned `Engine.Mu` held. None acquires/releases
it or waits on the lock manager. Callbacks, expression work and external I/O
must return to an unlocked orchestration boundary.

## Who embeds it

Routine, type, extension, index, table and schema feature hosts reuse this seam.
Core supplies `DDLHost`; families build their adapters over `{E, Op, CatalogStage}`.
Adding a feature host extends only the capability it genuinely needs.

## What stays in the engine

Core owns authority capture, stage begin/discard, lock windows and publication;
`schemacmds` and other statement families own utility dispatch and orchestration.
Stagers never start a second transaction or publish roots themselves.

## Testing

Pure host/helper units use small paired catalog/role fixtures without an engine.
Composed family tests cover actual staging/publication. Public SQL belongs to
reference packages under [docs/TESTING.md](../../../docs/TESTING.md).
