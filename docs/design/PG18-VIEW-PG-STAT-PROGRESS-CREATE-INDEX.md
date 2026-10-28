# pg_stat_progress_create_index

## Contract and authority

This read-only view reports real, currently executing, admitted index commands.
It does not simulate work or retain a completed-command history. It covers the
existing CREATE INDEX dispatcher (including supported expressions, predicates,
uniqueness, access methods and partition recursion), supported CREATE INDEX
CONCURRENTLY, and existing ordinary REINDEX paths. It does not add SQL features;
REINDEX CONCURRENTLY and REINDEX on partitioned targets remain outside the
existing supported command surface.
Implicit indexes created by other SQL commands, DML index maintenance, Restore,
ALTER and unrelated root rebuilds do not become CREATE INDEX progress commands.

Authority is the [PostgreSQL 18 progress manual](https://www.postgresql.org/docs/18/progress-reporting.html#CREATE-INDEX-PROGRESS-REPORTING),
[monitoring visibility rules](https://www.postgresql.org/docs/18/monitoring-stats.html#MONITORING-STATS-VIEWS),
and released [REL_18_0 system_views.sql](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/catalog/system_views.sql),
[indexcmds.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/commands/indexcmds.c),
[pgstatfuncs.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/adt/pgstatfuncs.c)
and access-method build implementations. The catalog manual target is 18.6.
The actual existing reference fixture uses PostgreSQL **18.6**, image
`postgres:18.6-bookworm@sha256:afc7e2d441324c0388fa80c3d24f733b4194a4eb7f47dd8ee2b08eb1a24a647c`,
with UTF8/C; coordinator verification recorded server_version_num=180006.
The selected catalog's older 18.0 runtime description is stale and does not
authorize downgrading this fixture. The linked 18.0 release sources grounded
initial research; the actual pinned 18.6 runtime is the executable oracle.
Local development PostgreSQL sources are research, not an oracle. Expectations
below are contracts; passing evidence belongs to coordinator verification.

## Shape and authorization

The sixteen nullable attributes, in order, are:

| Columns | SQL types |
|---|---|
| pid | int4 |
| datid | oid |
| datname | name |
| relid, index_relid | oid |
| command, phase | text |
| lockers_total, lockers_done, current_locker_pid | int8 |
| blocks_total, blocks_done | int8 |
| tuples_total, tuples_done | int8 |
| partitions_total, partitions_done | int8 |

`pid` is the actual worker session identifier, and `datid`/`datname` identify its
database, not the observer's. `relid` is the target heap; recursive CREATE retains
the top partitioned relation identity. Ordinary CREATE has `index_relid=0`.
Concurrent CREATE exposes the actual published index OID; REINDEX exposes its
current existing target index OID. A REINDEX TABLE target change resets its
per-index state; target iteration has no SQL ordering guarantee.

Every fresh capture, including held prepared execution, evaluates the observer's
**current** role and inherited membership against the worker's **authenticated**
principal. Superusers, inherited worker members, and inherited
`pg_read_all_stats` members can read details; `pg_monitor` works through its
inherited membership. Worker SET ROLE does not transfer progress ownership to
that effective role. Table ownership or SELECT alone grants no detail access.
Observer SET ROLE/reset, SET SESSION AUTHORIZATION and membership changes use
fresh authority, not an approval cached at Prepare.

Restricted observers retain only pid/datid/datname. **All thirteen other cells
are typed SQL NULL**, including command, phase, index identity and every counter.
Authorized unknown or inapplicable counters are **zero, not NULL**; authorized
ordinary index identity is OID zero. Missing database lookup follows the released
left join and yields NULL datname. Returned facts/rows are detached snapshots;
future counter changes or cleanup cannot mutate a retained result. Multiple
workers are an unordered bag, never an implicit PID or command order.

## Production ownership and lifetime

`core/index_progress.go` owns a cluster-wide active-entry map keyed by existing
session identity, with authenticated principal captured at admission. One entry
mutex protects phase, identity and counters coherently. Capture takes the engine
capture lock before the entry lock; callback updates do not acquire the engine
lock while holding the entry lock. `pgcatalog.CreateIndexProgressFact` and
`VisitCreateIndexProgress` bridge this narrow runtime capability to the ordinary
typed catalog producer and shared `NewProgressViewer` filtering.

Registration follows actual relation admission and a valid non-skipped command.
An ordinary CREATE blocked on its initial ShareLock is not yet progress.
Permission/name errors and IF NOT EXISTS skips do not leave an entry. Existing
statement-scope completion owns exact-entry cleanup, including error,
cancellation, panic and early/repeated Finish. Cleanup of an old entry cannot
delete a replacement for that session. Registration must enclose the actual
build/publication, including the explicit/private overlay route; it is not
removed before delayed builders execute. No error/cancellation sets fabricated
final totals. Retried builds reuse command identity but reset actual build work.

The explicit index stage binds only selected targets, including its selected
partition descendants or REINDEX rebuild set. `index.BuildCapture.Progress`
transports an optional `index.BuildProgress` observer through existing build
paths. Its `ScanStart`, successful-prefix `ScanDone`, and method-aware
`ScanFinish(n, method)` callbacks instrument real processing. ScanFinish enters
the real B-tree sort, without claiming hash or other methods perform that sort.
Observer-only capture does not require captured output arrays;
a nil observer adds no scratch allocation or alternate executor. No live
observer is persisted in a committed descriptor/root or transaction journal.

Success, failure and cancellation remove the progress row before returning the
terminal result. This does **not** mean all durable artifacts are removed:
PostgreSQL-compatible admitted concurrent cancellation can retain a live,
not-ready, invalid index. Initial-admission cancellation and failed ordinary
CREATE do not publish an index. Existing transaction/index rules remain the
publication authority.

## Actual phases and counter meanings

Initialization uses `initializing` with unknown/inapplicable values zero. B-tree
scan callbacks publish `building index: scanning table`; the real live-entry
sort publishes `building index: sorting live tuples`. Native immutable-vector
root wrapping is O(1), not physical tree loading. It neither adds a fake
`loading tuples in tree` loop nor increments tuples_done for comparisons or
scanned heap slots. No dead-tuple sorting phase exists without actual dead-tuple
sorting work. PostgreSQL may genuinely expose additional short AM phases;
public tests do not require both engines to implement the same physical work.

Access methods without released AM phase names use `building index`, not a
B-tree suffix. Hash reports the actual indexed-entry total after its scan
(excluding NULL keys); native direct construction has no spool-loading tuple
progress. BRIN tuple counters remain zero: summary or row counts are not the
released tuple-progress parameters. Neither method resets completed scan blocks
merely to resemble B-tree sorting.
GiST and SP-GiST likewise use generic `building index` with tuple counters zero;
their native shared-vector implementation does not justify B-tree progress
labels or invented tuple-loading counters.

### The only approximation: logical scan blocks

For captured storage-version count N and successfully processed prefix v:

- `blocks_total = N/128 + (N%128 != 0)` using integer quotient/remainder;
- partial-scan `blocks_done = v/128`;
- at v=N, `blocks_done = blocks_total`, including the final partial block;
- empty input is 0/0.

One logical block is **up to 128 captured storage row versions**, not a physical
PostgreSQL page, byte count, tuple-width estimate or I/O claim. A version counts
only after its actual processing succeeds, including successful dead-version,
predicate-excluded or method-specific NULL skips. There is no pre-scan byte
encoding/pass to estimate physical work. Callback publication may batch at
completed logical blocks without inventing completed work.

During B-tree scan, tuples_total/tuples_done are unknown 0/0. At scan completion,
entering actual sort atomically resets block counters to 0/0 and sets
`tuples_total` to the exact admitted entry count, `tuples_done=0`. Duplicates and
B-tree NULL entries count; dead/predicate-excluded entries do not. Completed
counters are monotonic **within a phase/build attempt**, bounded by known
positive totals. Zero can mean unknown, so it is not a universal upper bound.
No global monotonicity is promised across phase, retry or REINDEX target resets.
Native logical blocks are never compared numerically with PostgreSQL pages.

### Concurrent work

Existing frozen writer cohorts drive `waiting for writers before build` and
`waiting for writers before validation`: lockers_total is the actual cohort,
current_locker_pid the actual current backend (zero for a prepared owner with
no backend), and lockers_done advances only after completed real waits. New
writers do not inflate an already frozen cohort. Normal writer-wait exit clears
the three locker fields, not retained prior build tuple/block metrics.

Existing TEMP behavior is a specific compatibility divergence: PostgreSQL
normalizes CREATE INDEX CONCURRENTLY on a temporary table to ordinary CREATE,
whereas the current native dispatcher actually takes its concurrent route.
Progress truthfully follows that existing native route, including concurrent
command identity and actual work. This part does not normalize TEMP execution
or claim shared TEMP parity; public dual-mode witnesses use ordinary permanent
tables.

The native first build and second fresh-snapshot rebuild execute real heap
extraction and AM sort/construction, so they report actual `building index`
work. They do not pretend to execute PostgreSQL's index-TID validation scan,
TID sort or physical heap-merge algorithm. WAIT2 retains prior build metrics.
Actual old-snapshot waits use `waiting for old snapshots` with their real frozen
transaction/snapshot cohort. Rechecks can invalidate remaining members; done
follows considered cohort ordinals, not elapsed time or a forced final total.
The released old-snapshot helper can retain the final locker state until command
end, unlike normal writer-wait cleanup.
On every reached iteration after the first, the whole remaining original cohort
is rechecked. Entries invalidated by a previous recheck are skipped without
another done update. A reached iteration still publishes its original ordinal,
even if it invalidates itself. Thus original [A,B,C], with B/C disappearing
while A blocks, settles total3/done2 and retains A's last PID, not done3.
Deterministic internal actual-wait coverage captures this final state before
Finish; public tests do not race command cleanup to sample a terminal counter.

The public old-snapshot witness observes from a dedicated **different database**.
Released `WaitForOlderSnapshots` calls `GetCurrentVirtualXIDs` with allDbs=false;
[REL_18_6 procarray.c](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/storage/ipc/procarray.c)
explicitly excludes other databases in that mode. Monitoring queries therefore
cannot accidentally join the frozen worker-database cohort. The view still
must expose the worker's database OID/name, not the monitor's. Setup, index OID
lookup and data checks use a separate worker-database control session.
The second sample requires both done1 and the actual remaining holder PID:
done can legitimately advance while the previous PID is still retained before
the next real wait begins.

Partition totals count selected direct and indirect descendants, excluding the
root; ONLY and ordinary nonpartitioned CREATE report zero. Compatible existing
subtree attachment increments done once: its indirect existing descendants are
not individually counted as completed. For example, a reused intermediate with
two leaves plus one newly built sibling has total4 and final done2, not4.
A newly staged physical leaf is not completed until its actual build succeeds.
New intermediate completion follows recursive work. Consequently done need not
equal total even at completion; cleanup never forces equality. REINDEX
partition counters remain zero.

## Read-only SQL diagnostics

Qualified/unqualified direct and prepared operations reuse ordinary builtin-view
write admission. INSERT VALUES/SELECT, UPDATE and DELETE (including WHERE false)
raise SQLSTATE `55000` with exact primary respectively:

- `cannot insert into view "pg_stat_progress_create_index"`
- `cannot update view "pg_stat_progress_create_index"`
- `cannot delete from view "pg_stat_progress_create_index"`

Detail is `Views that do not select from a single table or view are not automatically updatable.`
Hint is `To enable inserting into the view, provide an INSTEAD OF INSERT trigger or an unconditional ON INSERT DO INSTEAD rule.`
For UPDATE substitute `updating`/UPDATE; DELETE `deleting from`/DELETE.
TRUNCATE raises `42809`, `"pg_stat_progress_create_index" is not a table`, with
no Detail/Hint. Complete semantic diagnostics, including absent optional fields,
are asserted rather than only SQLSTATE/substrings. Prepared DML rejects at
Prepare; TRUNCATE rejects at Execute. These failed mutations must not change
base data or end the genuinely active builder.

## Independent acceptance and smoke

External tests live in `reference/catalog_privileges/index_progress*_test.go`
and reuse public fixture sessions, authenticated pools and concurrent worker
launch/join. They import no internal packages and execute the same definition
in native and real reference modes. Expectations are independent, not parity
alone. Internal tests provide deterministic barriers in actual builders for
transient scan/sort/method/partition/REINDEX phases and coherent snapshots.

| Selector | Public obligation |
|---|---|
| TestReferenceCreateIndexProgressShape | Empty qualified/unqualified direct/prepared reads; exact 16 names/types/nullable attributes, view and row-type ownership links |
| TestReferenceCreateIndexProgressReadOnly | Full diagnostic/stage matrix while a real concurrent builder remains active; sentinel unchanged |
| TestReferenceCreateIndexProgressAdmission | Ordinary initial ShareLock has no row; cancel no index, release and successful ordinary build/cleanup |
| TestReferenceCreateIndexProgressLongBuild | Real concurrent identity and index OID; two held writer transactions, release the PID actually reported, observe exact total2/done0→1/current other PID, cancel and clean invalid artifact; fresh held successful build |
| TestReferenceCreateIndexProgressVisibility | Authenticated worker, inherited worker, stats reader/monitor, superuser; unrelated SELECT user and effective table owner masked; worker SET ROLE; fresh direct/prepared captures after role/membership changes |
| TestReferenceCreateIndexProgressConcurrentPhases | A late writer creates a genuine second wait; prior B-tree exact tuple total retained; real success and cleanup |
| TestReferenceCreateIndexProgressOldSnapshots | Cross-database monitor excluded from the frozen cohort; two real REPEATABLE READ holders snapshot an unrelated table before launch; worker database identity retained, old-snapshot phase total2/done0/current real member; release the reported PID, await done1 **and** remaining PID without ordering assumptions; separate active cancellation and successful release, progress removal, ready-invalid versus valid index outcomes and unchanged data |
| TestReferenceCreateIndexProgressSupportedBuilds | Existing expression/partial/multikey/unique/hash/BRIN/partition attachment/REINDEX/skipped/failing ordinary SQL terminal cleanup and data/catalog controls |
| TestReferenceCreateIndexProgressFeatureSmoke | Focused real two-holder completed wait progress, restricted thirteen-NULL snapshot, cancellation+absence+invalid-index cleanup, then successful held build+absence+valid index+unchanged multiplicities |

No enormous ordinary workload is needed to manufacture a timing window. An
admitted concurrent wait is real supported command work, not an initial
ordinary-lock false row. Tests control real holders, release the reported PID
without assuming cohort order, and cancel/join every launched worker on cleanup.
The small ordinary success and deterministic internal real-scan/sort witnesses
remain separate requirements. A missed required active sample fails, not skips.

Coordinator-selected proof, cwd the assigned worktree, external deadline 900s:

```sh
make check RUN='^(TestReferenceCreateIndexProgressShape|TestReferenceCreateIndexProgressReadOnly|TestReferenceCreateIndexProgressAdmission)$'
make check RUN='^(TestReferenceCreateIndexProgressShape|TestReferenceCreateIndexProgressReadOnly|TestReferenceCreateIndexProgressAdmission)$' REFERENCE=1
make check RUN='^(TestReferenceCreateIndexProgressLongBuild|TestReferenceCreateIndexProgressVisibility|TestReferenceCreateIndexProgressConcurrentPhases|TestReferenceCreateIndexProgressSupportedBuilds)$'
make check RUN='^(TestReferenceCreateIndexProgressLongBuild|TestReferenceCreateIndexProgressVisibility|TestReferenceCreateIndexProgressConcurrentPhases|TestReferenceCreateIndexProgressSupportedBuilds)$' REFERENCE=1
make check RUN='^TestReferenceCreateIndexProgressOldSnapshots$'
make check RUN='^TestReferenceCreateIndexProgressOldSnapshots$' REFERENCE=1
make check RUN='^TestReferenceCreateIndexProgressFeatureSmoke$'
make check RUN='^TestReferenceCreateIndexProgressFeatureSmoke$' REFERENCE=1
```

Smoke succeeds only after the exact real active/cancel/success observations in
the table, not an idle query or readiness claim. Root owns verification after
all edits, whole-project quickcheck, race proof, review and integration. No
verification was run by the reference/document implementer.
