# Statistics reference scenarios

This bounded feature package owns public SQL scenarios for the seven-column
cluster archiver, four-column cluster bgwriter, eleven-column cluster checkpointer,
nine-column `pg_stat_all_indexes`/`pg_stat_sys_indexes`, 30-column `pg_stat_all_tables` and twelve-column
`pg_stat_xact_all_tables`/`pg_stat_xact_sys_tables`/`pg_stat_xact_user_tables` views.
All Go tests are untagged external tests using public stuntgres and
`reference/fixture` APIs. The same scenario bodies serve native and
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

### SYS index statistics

The six SYS definitions own ordinary `information_schema` hash fixtures and
public negatives; no bootstrap indexes or physical TOAST storage are fabricated.
They check the exact `pg_catalog`/`information_schema`/case-sensitive `^pg_toast`
parent predicate and ALL/SYS/USER-predicate identity bags, fixed native row/array
relationships, nine-column metadata and dependency-aware view display. Current
own-view ACL denial is distinct from unredacted parent facts. DML errors name
the underlying ALL view; TRUNCATE/COPY retain SYS. Rename/move/drop/recreation
reexecute held reads against current identities and preserve provider history.

EXPLAIN and Prepare alone count nothing. A native direct hash hit contributes
1 search / 2 entries / 2 heap fetches; a second held hit and prepared miss bring
the total to3/4/4. PostgreSQL expectations classify the actual simple, bitmap or
sequential path rather than requiring an index choice. Every phase, including
the confirmed sequential negative, retires its reporter and awaits a separate
publication marker before asserting counters and the last-scan instant. SYS and
ALL facts are read under one existing snapshot contract, not a per-view cache.

```sh
make check RUN='^TestReferencePGStatSysIndexes(IdentityAndMetadata|Events|Lifetime|Privileges|ReadOnly|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePGStatSysIndexes(IdentityAndMetadata|Events|Lifetime|Privileges|ReadOnly|FeatureSmoke)$'
make check RUN='^TestReferenceInfoColumnsPopulation$'
make check REFERENCE=1 RUN='^TestReferenceInfoColumnsPopulation$'
make check RUN='^TestPGStatSysIndexes(Catalog|Membership|SourceSelection)$'
make check RACE=1 RUN='^TestPGStatSysIndexesSourceSelection$'
```

The standalone feature walkthrough creates positive/negative fixtures, checks
fresh0/NULL metadata, consumes real direct/held hit/miss accesses, awaits
publication, checks positive typed time and ALL equality, proves sequential zero
delta, rejects zero-row UPDATE with the full ALL diagnostic, then observes DROP:

```sh
make check RUN='^TestReferencePGStatSysIndexesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGStatSysIndexesFeatureSmoke$'
```

## Cumulative table statistics

ALL and SYS reuse `tableStatFamily` for independent 30-column catalog/driver
metadata, actual sequential and attached-index access, tuple settlement,
completed maintenance, Reader modes, lifetime and native-control scenarios.
Each family's publication probes demand its named projection through real
physical writer retirement. SYS owns ordinary information_schema fixtures;
the public lifetime matrix narrows its schema predicate with A's identity first,
matching the shared Keep contract's first-evaluated-conjunct authority.
Composed ownership coverage separately gives owned namespace descriptors unique
system-prefix spellings, without assuming information_schema has no builtin heaps,
and uses the real shared Provider/Reader to distinguish pure schema rejection:
cache retains A but not B, snapshot retains both, and clear refreshes table and
attached-index facts without publishing pending work. Cleanup removes only
owned objects; no writable pg_catalog fixture or DDL bypass is involved.

SYS membership is compared as an identity bag against ALL's exact
pg_catalog/information_schema/case-sensitive pg_toast prefix subset; USER is
the complement. Privilege scenarios distinguish the named view's current ACL
from underlying schema/table access and from ALL's ACL. Read-only scenarios
assert complete underlying grouped-ALL diagnostics, including zero-row DML,
while TRUNCATE/COPY retain the named projection.
Canonical and pretty SYS disclosure have independent full-definition expectations
under quoted/unquoted identifiers and actual ALL-name shadowing, through direct
and prepared execution. Their membership expression and all thirty columns stay
identical while redundant display parentheses differ.
The xact consumer independently exercises the current physical writer and
live tuple frames, rather than cumulative history.
Terminal-local witnesses begin on a reporter before any setup command can
establish its publication throttle; their DML uses qualified fixture names.
Per-backend lifetime destinations remain callback-local, so one backend's
schema round trip cannot alter the other's scenario.

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

The reusable cumulative family selector is
`make check RUN='^TestReferencePGStat(All|Sys)Tables'` and the same command
with `REFERENCE=1`. Internal ownership supplements use
`make check RUN='^TestPGStat(All|Sys)Tables'`, adding `RACE=1` for synchronized
ownership coverage. Native controls retain the same general TEMP assertions
while SYS also holds persistent system-namespace rows through replacement.

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

USER uses the same twelve typed columns and pending authority, with fixed native
relation12170, rowtype12172 and array12171. Its namespace predicate is exactly
`schemaname NOT IN ('pg_catalog','information_schema') AND schemaname !~ '^pg_toast'`,
the case-sensitive complement of SYS; TEMP tables qualify. Both namespace and
identity/schema/name selection reject before any table/index runtime inquiry.
Current rename and schema movement, including savepoint rollback, preserve OIDs
while changing membership. Neither base-table ownership nor inaccessible schema
USAGE redacts identities/counters. The USER view's own current SELECT/column ACL
still applies to direct, prepared, held and FALSE reads independently of ALL.
No-index index counters are typed INT8 NULLs; all other absent counters are zero.
Native OID/INT8 values use int64 and NAME uses string; pq's OID/NAME use []byte.
Retained rows and metadata survive closure and later DDL/replacement unchanged.
Dependency-aware default/pretty view definitions preserve quoting and resolve
the real ALL dependency under search-path shadows.

USER tests independently exercise two-row INSERT with a NULL payload, writer2/
peer0 isolation, synchronized rollback/commit publication, cumulative aborted
attempts versus physical data, continuous none/cache/snapshot state, tracking
gates, demanded sequential/index accesses and retained/native-control lifetime.
Positive hash cases retain all four hit/miss accesses and their pending captures
in one implicit message, with genuine index plans and exact cumulative vectors.
ACL scenarios keep the held role reader and same-role writer physically distinct,
assert both reader/admin peers remain zero, then retire the reporting writer.
The zero-after-end witness requires actual publication, not unconditional
transaction reset: throttled unreported pending batches can cross transaction
ends. Repeated USER reads leave maintenance history unchanged; only completed
ANALYZE/VACUUM change the existing cumulative counts/timestamps. HOT/newpage alone
retain deterministic native zero physical-optimization estimates. DML rejects
with 55000 naming grouped ALL, exact GROUP BY detail and operation-specific hint;
TRUNCATE/COPY retain USER's identity and 42809 diagnostics.

Run each small USER selection in native mode and again with `REFERENCE=1`
against the pinned PostgreSQL 18.6 fixture, without substituting parity for the
independent assertions:

```sh
make check RUN='^(TestReferencePGStatXactUserTablesIdentityAndMetadata|TestReferencePGStatXactUserTablesMembership)$'
make check RUN='^(TestReferencePGStatXactUserTablesTransactions|TestReferencePGStatXactUserTablesCurrentState)$'
make check RUN='^TestReferencePGStatXactUserTablesAccess$'
make check RUN='^(TestReferencePGStatXactUserTablesPrivileges|TestReferencePGStatXactUserTablesReadOnly)$'
make check RUN='^(TestReferencePGStatXactUserTablesLifetime|TestReferencePGStatXactUserTablesCancellation)$'
make check RUN='^TestReferencePGStatXactUserTablesMaintenanceIsolation$'
make check RUN='^TestReferencePGStatXactUserTablesFeatureSmoke$'
make check RUN='^(TestPGStatXactUserTablesCatalogIdentity|TestPGStatXactUserTablesSourceSelection|TestPGStatXactAllTablesPendingProvider)$'
make check RACE=1 RUN='^(TestPGStatXactUserTablesSourceSelection|TestPGStatXactAllTablesPendingProvider)$'
```

Only the public `TestReference...` selections take `REFERENCE=1`. The genuine
USER feature smoke has its own body: twelve-column metadata/no-index NULLs;
ALL/USER1, SYS0 membership; INSERT2 including NULL with peer0; demanded ordered
scan1/read2; synchronized ROLLBACK then pending0, cumulative inserts2 and data0;
grouped-ALL UPDATE FALSE rejection; and maintenance unchanged by repeated reads.
These are expected observations, not a verification claim.

The reusable family selector is `make check RUN='^TestReferencePGStatXact(All|Sys|User)Tables'`
and the same command with `REFERENCE=1`; both use the existing PostgreSQL18.6
fixture policy. Internal provider/core supplements use
`make check RUN='^TestPGStatXact(All|Sys|User)Tables'`, with `RACE=1` for synchronized
ownership coverage.

## Transaction-local user function statistics

`TestReferencePGStatXactUserFunctions` covers the six-column view through public
database/sql consumers: typed identities and driver representations, tracking and
installed environments, trigger bodies, nested calls, present-zero/absent activity,
continuous current-state reads, savepoint and terminal lifetime, nonowner
distribution, retained VOLATILE backing EXECUTE, read-only full diagnostics,
catalog lifetime, cancellation and native controls. Independent native timing
expectations are one logical millisecond of own cost per completed tracked
invocation; PostgreSQL timings are genuine elapsed milliseconds, checked for
nonnegative/inclusive relationships rather than parity. Provider elapsed proofs
are unchanged. Only the named timing estimates differ, never IDs/names/counts.

Fresh physical reporting borrowers BEGIN before enabling tracking. Terminal
absence uses a recorded PostgreSQL-only force-next-flush and native first eligible
ordinary publication on the same backend; throttled cross-transaction retention
has a separate internal proof. No public native flush API is added. Included
admitted-zero entries are distinct from absent uninvoked/peer/untracked functions.
Ordinary nonowners see collected values without routine EXECUTE/schema USAGE;
the view and all retained volatile backing expressions retain current authority.
Literal FALSE and LIMIT0 have separate independently specified admission outcomes.
The direct bound-Boolean parameter case independently matches the pinned custom
plan: FALSE/typed Boolean NULL return empty, TRUE requires all retained getters,
and a held FALSE→NULL→TRUE→NULL→FALSE sequence rechecks current bindings within
the first five custom-plan executions. Own-view SELECT remains mandatory even
for eliminated FALSE/NULL input.
Cancellation's pre-canceled and already-expired Query/Prepare probes are explicitly
native-only inside the same dual-mode scenario. They require ordinary Canceled/
DeadlineExceeded context errors without SQLSTATE, unchanged facts, no cumulative
cut, and a subsequent healthy query after each pair of probes.
As [the cancellation policy](../../docs/TESTING.md#the-dual-mode-fixture) explains,
fast pre-canceled pq requests are not portable server admission witnesses and can
complete or invalidate their network transport. PostgreSQL instead executes the
scenario's normal state reads and unchanged-pending/no-cumulative-cut assertions;
it never sends a canceled probe through the reporting borrower. Actual canceled
body accounting remains strict in the independent native virtual-time proof.
The pretty definition checks the pinned release's unparenthesized JOIN/oid cast,
independently of the default renderer's parenthesized form.

Run `make check RUN='^TestReferencePGStatXactUserFunctions'` and the same command
with `REFERENCE=1`, including NativeControls. The permanent behavior smoke is
`make check RUN='^TestReferencePGStatXactUserFunctionsFeatureSmoke$'`, also with
`REFERENCE=1`. Internal supplements are selected with
`make check RUN='^TestPGStatXactUserFunctions'` and `RACE=1`. These are proof
selectors, not claims of exercised verification; whole Docker-free proof remains
`make quickcheck`.
The backing descriptors also extend the complete 2274-row routine inventory;
run `make check RUN='^TestReferencePGProcBuiltinInventory$'` and its
`REFERENCE=1` counterpart whenever these declarations or their facts change.
