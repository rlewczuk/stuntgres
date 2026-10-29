# internal/engine/publication

Database-local logical publication validation, command staging and effective
membership. This package consumes catalog, authorization, DDL, normal expression
and existing relation/hierarchy owners; it imports neither core nor the root
engine. It does not implement a replication transport.

`Stage` writes only the caller's private catalog stage. The host holds Engine.Mu;
normal name/definition/dependency admission and final journal publication belong
to core. There is no independent metadata store or transaction path.
Explicit table membership takes the actual relation's ShareUpdateExclusive lock,
including command-expanded inheritance members, before post-wait authority
recapture. A canceled waiter cannot publish its provisional candidate.

`VisitTables` enumerates current deduplicated presented relations without
materializing DML-effective filters or default column lists. Core projects direct
mapping metadata only after relation selection. `VisitRelation` resolves the
current definitions applicable to an admitted physical relation through its real
partition ancestry, without enumerating database tables or constructing absent
column lists. Ordinary inheritance is expanded by the defining command; partition,
schema and all-table coverage remain dynamic. `EffectiveTable.Source` supplies
stable source attributes for DML; callers translate to leaf layouts by current
names. Borrowed descriptors and attribute slices are immutable for their catalog read.

Filters retain the normal `stored.Policy` evaluator and frozen presentation.
`ValidateFilter` checks the normal bound expression and expanded virtual recipes;
`FilterSQL` renders current stable names with the same ordinary renderer.
Semantic dependency addresses are detached catalog metadata, separate from full
executable lifetime dependencies. Local SQL is never filtered by a publication.
Occurrence-owned anonymous RECORD constructors are normal expression structure,
not user-defined catalog types; their children retain all filter restrictions.

`CheckReplicaIdentity` consumes current membership and actual physical keys.
Filter coverage, explicit-list coverage, generated keys and missing identity are
checked in their defined order for each relevant publication independently.
FULL covers every attribute: virtual generated columns remain unpublished under
either generated-column option; stored generated columns require the stored option.
Command capability admission, including empty targets, MERGE and ON CONFLICT
UPDATE, belongs to core Execute; Prepare retains no authorization decision.
This admission precedes unsupported physical hierarchy execution boundaries,
without granting an executor capability or executing a matching row.

## Testing

Private domain identity units live in `enginetests`, with composed catalog,
retained predicate and capture ownership units beside core. Use
`make check RUN='^TestPublication'` for the family. Public command, diagnostic,
transaction and native-control scenarios live in `reference/catalogs` under the
shared [test policy](../../../docs/TESTING.md).
