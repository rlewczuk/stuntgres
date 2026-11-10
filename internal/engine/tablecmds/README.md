# internal/engine/tablecmds

Table, sequence and maintenance statement orchestration. `table`, `index`,
`partition` and `sequence` own decisions/records; core owns runtime authority.

## Layering

Imports core and lower decision packages, never the root or another family.
Other permitted families reuse its hosts and shared continuations. Core keeps
relation admission, dependency controllers, TEMP, row/index publication and
transaction/effect lifecycle.

## Entry points

Locked table continuations enter with Engine.Mu held and the target admitted,
and release it on every return. Ordinary utility entries begin with nothing held.
Capture/metadata binding is dormant under short lock windows; waits, scans,
rewrites, provider initialization and physical index builds run unlocked on the
current statement child. Post-wait and final-publication fences retain original
identity, storage, namespace and role authority.

ALTER processes ordered metadata phases and then detached physical demand over
accepted candidates. Private descriptor/row/index changes share the real stage
and journal. Reusing produced keys, transporting type generations and genuine
rebuild intent are distinct; no later metadata edit silently reevaluates prior
private writes or waives predecessor checks.

## VACUUM

Maintenance uses the actual statement/ticket and scoped per-target contributions.
Capture immutable physical roots under canonical ownership, release locks for
computation, then revalidate target, storage and maintenance predecessors before
installation. Failed detached work publishes no partial model. Nontransactional
maintenance and transactional rewrite paths retain their separate core owners.

## ANALYZE

Collection consumes real native/foreign/inherited inputs through admitted current
capabilities. Metadata negotiation and actual sample acquisition are distinct.
Collected typed facts and nontransactional maintenance reports have separate
completion boundaries; core journals, visibility and publication define lifetime.
Partition roots own metadata, not invented physical heaps.

## Hosts

`TableHost`, `AlterHost` and `TypeHost` embed core's DDLHost over
`{E, Op, CatalogStage}` and carry compile-time assertions. Their lower-package
methods run under caller-owned Engine.Mu without waiting or reacquiring it.
Families build the host they own; pre-stage decisions do not borrow a stale stage.

## Testing

External `tablecmds_test` tests use composed engines and `enginetest` fixtures.
Test-only accessors expose genuine stage/owner boundaries, not alternative
executors. Public table, inheritance and maintenance SQL belongs to reference
packages under [docs/TESTING.md](../../../docs/TESTING.md).
