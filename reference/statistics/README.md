# Statistics reference scenarios

This bounded feature package owns public SQL scenarios for the seven-column
cluster archiver, four-column cluster bgwriter, eleven-column cluster checkpointer,
nine-column `pg_stat_all_indexes`, 30-column `pg_stat_all_tables` and twelve-column
`pg_stat_xact_all_tables`/`pg_stat_xact_sys_tables` views. All Go tests are untagged external tests using public
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

Archiver scenarios independently assert exact seven-column/catalog/driver
metadata, singleton cardinality and read-only diagnostics; stable absent-event
zero estimates and NULL names/times; genuine bounded reset instants; current
view/column/backing EXECUTE admission; and none/cache/snapshot ownership.
Peer ACL changes are asserted after the reader transaction boundary, separately
from cumulative snapshot lifetime. Native private frontier tests distinguish
current authority from retained positive Reader backing.
Backing ACL controls distinguish implicit PUBLIC EXECUTE from explicit-empty,
explicit effective-default, reordered and duplicate entries, grant options and
alternate grantors. Direct and retained catalog/view consumers check real
GRANT/REVOKE and repeated native Dump/Restore into the source and an independent
destination; PostgreSQL executes the actual emitted ACL repairs. Default raw
absence requires no repair, while every explicit raw distinction survives replay.
Cluster reset history survives successive distinct late database publications,
including a shared reset issued by the first late target before the next is
published, and local native replacement. Unrelated shared, SLRU and strict-NULL
backend resets preserve archiver authority. Public controls retain actual
returned Rows, reject failed, I/O-interrupted and canceled replacement atomically,
and replay actual emitted user SQL into a distinct destination without importing
monitoring history.
Positive internally published events and cross-family private backing are
supplementary composed/producer tests, not a public physical archive facility.

Bgwriter scenarios additionally distinguish scalar getter demand from singleton
presence and constant-false evaluation. Current per-column SELECT and per-getter
EXECUTE remain separate from Reader ownership, including held statements.
`TestReferencePGStatBGWriterQueryAdmission` denies a later occurrence's reset getter
before an earlier permitted occurrence can establish a snapshot, with empty and
retained cache/snapshot cuts, ordinary invokers, grant/revoke and held reexecution.
Peer resets and positive retained rows distinguish preservation from clearing.
`TestReferencePGStatBGWriterAggregateDemand` requires typed singleton zero from
`count(buffers_clean) WHERE FALSE` without cumulative access, while retaining its
getter EXECUTE check. Presence-only `count(*)` and TRUE/count-one controls keep
cardinality, permission demand and actual Reader demand separate.
Peer getter-ACL changes commit before the consumer's BEGIN. Denied epochs seed a
retained family through a still-permitted sibling (`buffers_clean` for reset-getter
denial, `stats_reset` for clean-getter denial), then use savepoint recovery to inspect
the unchanged cut. Separate granted epochs prove allowed FALSE/count-zero and
TRUE/count-one behavior. The same held statement survives deny/grant/deny transaction
boundaries; these scenarios do not assume peer ACL invalidations refresh catalog
tuples behind locks already held within a consumer transaction.
Native counters are exact zero estimates without physical events; PostgreSQL
physical counters may drift while reset instants, metadata, privileges and cached
cuts stay exact. Four raw backing ACLs exercise the existing serializer/replay
authority without exposing callable getters.

Checkpointer scenarios extend the same scalar-singleton contract to eight
independent BIGINT counters and two independent FLOAT8 millisecond totals.
Public reads are unmasked, while demanded output/predicate/sort getters retain
current EXECUTE checks even for false input. Presence-only reads do not capture
statistics. Native technical estimates are zero without physical checkpoint,
restartpoint, buffer, SLRU or IO events; PostgreSQL's physical quantities may
drift independently. Exact cluster startup/reset metadata survives late database
publication and accepted native replacement. Selected, NULL and omitted shared
resets honor existing Reader lifetime and survive rollback. Eleven raw getter
ACLs round-trip implicit/explicit distinctions through real controls; no callable
getter or serialized monitoring history is introduced. Owning internal tests
seed all eight counters and both sub-millisecond times independently.

## Testing

The fixed-family view scenarios use `make check RUN='^TestReferencePGStat(Archiver|BGWriter|Checkpointer)'`
and the same selector with `REFERENCE=1`; the fixture pin remains PostgreSQL18.6.

Selected bgwriter admission/aggregate proofs (both modes are required):

```sh
make check RUN='^TestReferencePGStatBGWriter(QueryAdmission|AggregateDemand)$'
make check REFERENCE=1 RUN='^TestReferencePGStatBGWriter(QueryAdmission|AggregateDemand)$'
make check RUN='^TestPGStatBGWriter(QueryAdmission|ReaderAuthority|ConstantRejection)$'
make check RACE=1 RUN='^TestPGStatBGWriter(QueryAdmission|ReaderAuthority|ConstantRejection)$'
```

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
use actual writer retirement. The xact consumer independently exercises the
current physical writer and live tuple frames, rather than cumulative history.

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

## Transaction-local table statistics

The xact family uses shared statistics setup, physical reporting connections,
full diagnostic recording and independently specified direct/prepared outcomes.
It distinguishes live owner/suffix counts and aborted-child attempts from
cumulative publication and committed data. Reader modes and snapshot clear
cannot freeze or publish local work; track_counts suppresses only new events.
The same physical connection is inspected after synchronized transaction end:
fresh native publication and PostgreSQL-only fixture force-next-flush make the
zero-at-end witness independent of the ordinary cross-transaction throttle.
No production flush builtin or unconditional terminal clearing is introduced.
Failed-statement counters follow actual accepted row work, not validated
proposals. Whole-batch validation and incremental RETURNING admission have
separate independently specified witnesses; both preserve physical atomicity.

Identity/name membership is current r/t/m/p without parent ACL filtering, while
the view's own SELECT ACL remains current at execution. Index absence is typed
NULL, distinct from untouched attached-index zero. Exact scans count reached
sources and fetched occurrences before filters; bitmap candidates and index-only
heap fetches are independently observed through actual EXPLAIN. Native HOT and
new-page counts are deterministic zero physical estimates, not tuple-event
approximations. Multirow assertions use bags or ORDER BY, never traversal order.
Positive hash and bitmap pending facts are captured in the same implicit
simple-query message as their actual accesses and EXPLAIN witnesses. Every
result set is checked before the message's terminal publication; no wall-clock
throttle assumption or new explicit-transaction index capability is required.
Native replacement tests use public controls and preserve attached TEMP
membership without importing old pending work or serializing history.

SYS consumes the same owner-local pending authority, filtered before inquiries by
the exact case-sensitive pg_catalog/information_schema/prefix pg_toast namespace
names. Its own current SELECT ACL is independent of ALL and underlying objects,
including held statements and empty reads. Simple SYS mutations rewrite to the
grouped ALL diagnostic target. Positive SYS tuple/access witnesses use owned
ordinary information_schema tables, with setup borrowers held while acquiring a
fresh reporter and bounded cleanup registered before creation. They never reset
information_schema or fabricate physical catalog scans.

The reusable family selector is `make check RUN='^TestReferencePGStatXact(All|Sys)Tables'`
and the same command with `REFERENCE=1`; both use the existing PostgreSQL18.6
fixture policy. Internal provider/core supplements use
`make check RUN='^TestPGStatXact(All|Sys)Tables'`, with `RACE=1` for synchronized
ownership coverage.
