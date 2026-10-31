# internal/engine/core/stats

The statistics runtime: the `pg_restore_relation_stats`,
`pg_restore_attribute_stats`, `pg_clear_relation_stats` and
`pg_clear_attribute_stats` imports, the `pg_stats` and `pg_statistic_ext_data`
visitors, statistics expression recipes for CREATE STATISTICS, and the codec
that renders and rebuilds captured statistics values for Dump, Restore and
catalog replacement. It is a leaf below core (ENGINE-SPLIT §3, L14.5); the
statistics facts, collectors and snapshot records are
[`engine/storage`](../../storage/README.md), and the pending facts are resolved
through [`engine/session`](../../session/README.md).

## Layering

- Imports only packages below core: `access`, `analyze`, `authorization`,
  `catalog`, `exec`, `expr`, `ids`, `lock`, `plan`, `regobj`, `session`,
  `storage`, `stored`, `table`, `typedef`, `view`, `parser`, `sqlstate`,
  `sqltype`. It never imports core, another leaf, a family or the root.
- Core reaches it through one-line adapters (`statistics_hosts.go`):
  `statementHost`'s `exec.Host` methods `AttributeStatistics` and
  `RelationStatistics`, the `catalogrt.Host` visitors of core's `catalogRuntime`
  and `DDLHost`'s `ddl.Host` recipe methods. `StatisticsSnapshotCodec`
  (`statistics_snapshot_binding.go`) builds a codec over core's bootstrap
  authority; the root's Dump and Restore, ANALYZE re-owning and catalog
  replacement call it.
- The imported-statistics reset that storage replacement and TRUNCATE call
  under `Engine.Mu` stays in core (`imported_statistics_reset.go`).

## Contract

- `ImportRelation` and `ImportAttribute` are the bodies of the restore and
  clear functions: argument decoding with WARNING notices in released
  validation order, relation resolution and maintenance authorization, the
  relation lock, then a fenced record into the statement's private catalog
  journal. They return whether every argument was applied.
- `VisitAttributes` and `VisitExtended` project the resolved statistics facts
  of every visible relation into `pg_stats` and `pg_statistic_ext_data` rows.
- `BindExpression` binds one statistics expression with the index expression
  binder that core builds over the parent, preflights its coercions and
  returns the retained `catalog.StatisticsExpression`; `SameExpression`
  compares two definitions.
- `SnapshotCodec` (`NewSnapshotCodec`) implements
  `storage.StatisticsSnapshotCodec`: deparsed expressions, encoded typed
  replay nodes and their checked canonical decoding.

## Host and authority

- `Host[A]` is the statement an import runs in; core implements it on
  `statementHost` (the shared leaf method set). It gives the operation
  (notices), the execution context, the input environment, the Engine.Mu
  pair, the pending catalog journal, the engine store, the routine
  authorization, relation resolution, the VACUUM maintenance check, the
  relation lock, the published relation and the statement's private catalog
  journal.
- `Authority` is that authorization, held by value (`Access`). The imports
  are generic over it, as `tsearch` is, so capturing it never allocates;
  `CatalogAuthority` adds the pending journal for the visitors. Core's
  `AuthorizationView` implements both.
- `RecipeAuthority` (routine admission and the view) and `SnapshotAuthority`
  (also the binder authority) are implemented by `*AuthorizationView`.
- `SnapshotHost` is a codec's captured bootstrap authority: a fresh copy of
  the authorization per decode, and the snapshot binding host for constructor
  scalars.

## Locks

- Imports are entered with no engine lock held. `Host.ReacquireEngine` takes
  `Engine.Mu` and `Host.ReleaseEngineLocked` releases it; the host methods
  named …Locked run inside those windows. Argument decoding, value parsing,
  notices and the relation lock (`AcquireObjectLock`) run with nothing held,
  and every window re-fences the catalog generation and authorization.
- The visitors run under the catalog runtime's capture lock; the codec and
  `BindExpression` run under the caller's lock and hold none of their own.

## Behaviour

Statistics expressions share the single-relation binder setup with indexes but
use a distinct `StatisticsConsumer`: no parameters, subqueries, aggregates,
grouping, windows or SRFs, and no index-specific IMMUTABLE restriction.
`BindExpression` preflights coercions and retains normal conditional execution
recipes, stable input identities and external dependencies. CREATE does not
evaluate volatile runtime values. Definition-object locks include these real
dependencies; publication rejects retired inputs/providers.
The authenticated catalog runtime projects ndistinct, dependencies, joint MCV
and expression summaries from one immutable extended fact. Missing payloads use
the current descriptor's typed NULL. Catalog SELECT admission precedes capture;
the runtime never collects populations or rebuilds retained statistics on read.
`SnapshotCodec` bridges semantic captured statistics to the existing
control-literal renderer and checked typed replay traversal. Destination value
construction uses the ordinary binder/executor under captured catalog authority.
Only this adapter supplies the retained structural-tree capability; ordinary
row replay and SQL input remain unchanged. Joint MCV native images are rebuilt
from typed dictionaries/counts, never copied with source OIDs.
It is not an expression-statistics evaluator or a re-ANALYZE path. Stable
deparsing matches retained recipes independently of source object addresses.

## Testing

- The runtime's engine tests stay in core until their census owners move
  them (`RUN='^Test(Statistics|RelationStatistics|AttributeStatistics)'` over
  `./internal/engine/core`); census-owned statistics tests are in
  `core/enginetests/{tables,schema,types,writes,pgcatalog}`. Public scenarios
  are `RUN='^TestReference(Statistics|AttributeStatistics|AlterStatistics|Analyze)'`.
- Under T7 this package has no `export_test.go`; engine tests that come here
  later go to `core/stats/enginetests`.
