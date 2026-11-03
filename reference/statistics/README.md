# Statistics reference scenarios

This bounded feature package owns public SQL scenarios for the nine-column
`pg_stat_all_indexes` and 30-column `pg_stat_all_tables` views. All Go tests are untagged external tests using public
stuntgres and `reference/fixture` APIs. The same scenario bodies serve native and
the fixture's pinned PostgreSQL 18.6 oracle; native controls remain explicit
native-only phases, not a simulated PostgreSQL Restore or Reset API.

## Contract

- Actual parent/index/schema identities, independent index OIDs, nullable typed
  metadata, direct/prepared/empty/retained results and raw adapter values. Fresh
  activity is zero counters and SQL NULL `last_idx_scan`, not an invented date.
- Actual access ownership authenticated by EXPLAIN: native hash hit/miss and
  lossy bulk bloom, PostgreSQL simple/bitmap/index-only behavior and actual
  `Heap Fetches`. Candidate entries are not inferred from output cardinality.
  Native B-tree presence creates no new indexed access method.
- Negative access witnesses explicitly enable sequential scans and disable
  index paths before checking the actual Full/Seq Scan plan. Disabled
  collection and prepare-only/NULL cases do not fabricate access events.
- Complete read-only diagnostics, including diagnostic fields and
  prepare-versus-execute boundaries, unchanged data/identity/history, COPY
  failure without affected rows or a command tag, and subsequent usable SELECT.
- Real parent-schema denial does not redact view identities/counters. The view
  retains its own current SELECT ACL, including already-held WHERE FALSE
  statements. Absence of activity and privacy redaction are not conflated.
- Shared none/cache/snapshot cuts, clear without publication/reset, actual
  writer retirement and independent bounded publication witnesses. Drop,
  rollback, rename/move and same-name recreation preserve the correct identity
  and generation boundaries.
- Transaction-stop timestamps, rollback-surviving read work and preparer-owned
  PREPARE publication. Native explicit/private queries retain their existing
  full-scan fallback; exact positive implicit stop/abort clocks and interrupted
  prefixes are covered by composed core ownership tests.
- Actual native Dump/Restore/Reset, failed/canceled Restore atomicity and
  destination-attached TEMP index history. PostgreSQL replays the actual emitted
  definitions/data and independently checks fresh activity. Runtime statistics
  are never serialized. Retained rows compare every identity/name/counter,
  NULL validity and exact timestamp instant, not Go time-zone pointer identity.

Common lifecycle bodies live in `fixture/statistics_helpers.go`; index-specific
row and expectation helpers remain local to this package. Public physical
retirement is a genuine terminal publication boundary, not a new SQL flush API.

## Testing

Run all nine definitions in native and real pinned PostgreSQL modes:

```sh
make check RUN='^TestReferencePGStatAllIndexes(IdentityAndMetadata|Events|AccessOwnership|ReadOnly|Privileges|SnapshotsAndLifetime|TransactionStop|NativeControls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePGStatAllIndexes(IdentityAndMetadata|Events|AccessOwnership|ReadOnly|Privileges|SnapshotsAndLifetime|TransactionStop|NativeControls|FeatureSmoke)$'
```

The feature smoke uses a real hash hit and held-statement miss, exact identities
and typed metadata, an authenticated sequential negative, a B-tree control,
read-only diagnostics and committed drop absence. Native hit plus miss produces
2 searches / 2 entries / 2 simple fetches with a non-NULL last scan; PostgreSQL
expectations follow its independently authenticated access method.

```sh
make check RUN='^TestReferencePGStatAllIndexesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGStatAllIndexesFeatureSmoke$'
```

Shared-helper changes also require the originating session-state consumers:

```sh
make check RUN='^TestReference(SessionActivity(Values|PreparedAndBatch)|StatsSnapshot(Lifecycle|Modes|Invalidation|TransactionReporting|PreparedReporting|TruncateReporting|AbortReporting))$'
make check REFERENCE=1 RUN='^TestReference(SessionActivity(Values|PreparedAndBatch)|StatsSnapshot(Lifecycle|Modes|Invalidation|TransactionReporting|PreparedReporting|TruncateReporting|AbortReporting))$'
```

Composed interrupted-prefix proof, including race mode, uses
`make check RUN='^TestPGStatAllIndexesInterruptedAccess$'` and the same command
with `RACE=1`. Whole-project Docker-free verification remains `make quickcheck`.

## ALL table statistics

The ten `TestReferencePGStatAllTables` definitions independently specify identity
and all 30 typed fields; r/t/m/p membership and unredacted inaccessible parents;
cumulative abort/commit attempts (including NULL payload rows); actual
sequential demand and transaction-stop timestamps; attached-index SUM/MAX and
no-index NULL versus untouched-index zero; source-specific completed maintenance;
none/cache/snapshot and predicate-before-reader ordering; qualified object
lifetimes; genuine native controls; complete read-only diagnostics; and a
standalone feature walkthrough. Public publication probes name ALL directly and
use actual writer retirement, never a SQL force-flush hook. Internal current
transaction Ledger/core tests cover unresolved ownership, not a new XACT view.

Native HOT/newpage estimates are zero because there are no corresponding
physical optimizations. Live/dead use logical version estimates. Elapsed totals
are finite nonnegative milliseconds, not cross-backend wall-clock goldens.
Automatic fields stay zero/NULL without workers. Exact scans, identities,
privileges, tuple events and completed counts are not approximations.
Actual SQL index builds and FULL rewrites use the same sequential capability.
Tests isolate and publish DDL baselines before later access, count fetched source
versions before partial/rewrite filtering, and distinguish FULL from ordinary
vacuum history. Reconstruction and ordinary ANALYZE/lazy VACUUM add no generic
sequential event.
Actual index DDL shares the physical writer's ordinary publication throttle.
Unrelated sequence membership fixtures therefore isolate setup index builds on
a genuinely retired setup connection, preserving their sequence writer and TEMP
ownership rather than forcing publication or suppressing collection.
The composed parallel proof uses real query and aggregate coordinators: reached
lanes keep private start/tuple scalars, and only the joined leader merges them.
It checks exact two-lane input totals, filtered results, disabled collection,
unstarted work and gate-defined failed/canceled prefixes, including an error
after a full scan when projected output is discarded.
Pre-admission maintenance cancellation uses `sql.DB.ExecContext` with an already
canceled context, so pool acquisition rejects before invoking either driver,
and verifies unchanged ALL facts. It is not an in-flight cancellation proof:
the composed deterministic execution/publication-fence tests own that evidence.
An already-held pq connection may complete a quick command despite that context.
The internal mixed-hierarchy demand witness uses actual engine/catalog/local
heaps and the shared provider, including a reached empty parent and unopened
local children. An existing registered unit wrapper is only an **unopened**
foreign-arm negative control: the proof consumes no fabricated foreign data and
makes no claim about foreign transport. Existing foreign hierarchy lineage,
rewind and identity regressions remain separate coverage.

```sh
make check RUN='^TestPGStatAllTables(CatalogIdentity|Provider|ScanDemand|ParallelAccess|InterruptedAccess|CurrentTransaction|IndexBuild|FullRewrite)$'
make check RACE=1 RUN='^TestPGStatAllTables(Provider|ParallelAccess|InterruptedAccess|CurrentTransaction|IndexBuild|FullRewrite)$'
make check RUN='^Test(ProducerRegistryLookup|CatalogProducerLookup)$'
make check RUN='^(TestPartitionPruningEmptySegments|TestPlannerSettingsPruningSelection|TestQueryBranchPruning(EmptyDemand)?)$'
make check RUN='^(TestForeignHierarchy(Lineage|RewindOwnership|RecheckIdentity)|TestPgBuffercacheMixedHierarchyReadFrontier)$'
make check RACE=1 RUN='^(TestForeignHierarchy(Lineage|RewindOwnership|RecheckIdentity)|TestPgBuffercacheMixedHierarchyReadFrontier)$'
make check RUN='^TestReferencePGStatAllTables(IdentityAndMetadata|MembershipAndPrivileges|EventsAndRollback|SequentialAccess|IndexAggregation|Maintenance|SnapshotsAndLifetime|NativeControls|ReadOnly|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePGStatAllTables(IdentityAndMetadata|MembershipAndPrivileges|EventsAndRollback|SequentialAccess|IndexAggregation|Maintenance|SnapshotsAndLifetime|NativeControls|ReadOnly|FeatureSmoke)$'
make check RUN='^TestReferencePGStatio(All|Sys)SequencesMembership$'
make check REFERENCE=1 RUN='^TestReferencePGStatio(All|Sys)SequencesMembership$'
make check RUN='^TestReferencePGStatAllTablesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGStatAllTablesFeatureSmoke$'
```

The feature smoke creates a fresh three-row heap, observes scan1/read3 including
a NULL result, creates attached hash and untouched B-tree controls (two actual
builds add2 scans/read6), publishes that DDL baseline, then executes held prepared
hit+miss (index scans2/fetch1, no further sequential work). It rolls back two inserts while cumulative
inserts rise to5 and physical rows stay3, completes ANALYZE/VACUUM, checks
unchanged maintenance on repeated ALL reads, rejects UPDATE with GROUP BY detail,
and proves committed drop absence. These are expected assertions, not a recorded
verification claim. The fixture remains pinned PostgreSQL 18.6; both selected
modes and the existing index regression selectors above are required after
changes. Whole-project Docker-free verification remains `make quickcheck`.
