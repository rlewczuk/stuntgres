# internal/engine

The root engine is the analysis host and owns transactions, settings and
lifecycle publication: `Engine.mu`, the catalog store, statement scopes and
admission, catalog/storage/visibility installation, and the orchestration of
native Dump/Restore and the wire protocol. Subsystem logic lives in layered
subpackages ([`docs/ENGINE-SPLIT.md`](../../docs/ENGINE-SPLIT.md) §3) that never
import the root and reach it through the hosts below.

## Procedural exception boundaries

`plpgsql_checkpoint.go` binds unnamed owner marks to the exact admitted statement,
capturing pending stage rows/deletions and transactional effect roots as well as
the owner savepoint. Row/value payloads remain immutable and shared; mutable maps,
event progress and append prefixes are owned by the checkpoint. Initializers run
before the boundary. Success releases it; failure restores effects before matching
and runs the handler outside the failed mark. Provider callbacks run unlocked,
cleanup ignores cancellation, and failed cleanup prevents further handler execution
while preserving the originating diagnostic and secondary failure.
PL and SQL context decoration uses `sqlstate.WithDiagnostic`: the decorated
primary diagnostic is visible through `errors.As`, while the original cause,
fatal cleanup classification and secondary failure remain traversable across calls.
Simple CASE query/position mapping uses the same classification-preserving seam
and phase-aware expression-context policy as ordinary PL expressions. Execution
failures do not acquire a planning-only expression prefix.

Accepted write events retain their actual predecessor/successor and command token.
`sql_command_effects.go` completes only the active nested SQL command's pending
constraint obligations and causal referential actions; probes still see the full
private view. Root completion drains its remaining events without replaying actions.
Only relations with incoming/outgoing FK or exclusion obligations retain accepted
transitions. Row-at-a-time writers reuse relevance captured by each existing
physical write binding, including routed insertion groups; alternating destinations
do not repeat catalog discovery. Reuse requires the exact reference, parent and
immutable catalog root. Other lanes retain an allocation-free command value.
Unrelated DML does not allocate the effect queue. NOT VALID FKs still enforce new
writes, and exclusion-only transitions retain bounded command/version ownership.
Neither command completion nor protected rollback publishes rows, rotates snapshots,
rewinds identities or owns `Finish`. DO uses that same root publication path.
Nested utility completion flags are lexical scratch, saved and restored separately
from the root's publication outcome. A successful SET helper cannot suppress the
surviving writes of its SELECT or DO caller.
Queues with no referential work advance bookkeeping without adding a cancellation
phase ahead of the root's final publication fence. Procedural diagnostic provenance
uses the actual planner-to-executor handoff, not whether eager execution returned
an error before exposing rows.
PL lexical expressions use the existing closed-immutable folding policy before
that handoff; PL-local, parameter, row, domain and volatile demand barriers remain.


## Cluster and logical database ownership

Each exact Driver DSN owns one `databaseCluster`: a pointer-stable metadata mutex,
shared `roleStore`/principal domain/ACL authority and database registry with a
stable default target and monotonic identities. Local Engine runtimes borrow
that mutex and role authority from birth, but own independent catalog domains,
storage, visibility, locks, replacement gates and session resources. Equal numeric
local references never authorize cross-target use. Local database descriptors
project the shared registry; registration/owner/ACL publication prepares every
affected root before atomic installation. Retained captures remain immutable,
and transaction-private authority overlays retain predecessor/savepoint fences.
Database identity reservation starts at or beyond every runtime's local allocation
frontier (including unpublished DDL reservations) and skips imported definition OIDs.
Local object allocation also reserves projected database identities; registering
a target cannot invalidate stored definitions in an existing or newly copied root.

Creation clones/remints a lazily retained immutable real UTF8/C builtin seed,
not the application's changing default database. Mutable rows, indexes and
sequence generators are independent. Copied system owners remain bootstrap
`postgres`; `public` remains `pg_database_owner`, with PUBLIC USAGE and implicit
CREATE for the new database owner. New database ACL is NULL/default rather than
copied from the attachment. The seed is private, not a selectable mutable
`template1` or a fabricated registry row.

The create executor accepts OWNER, default/`template1` TEMPLATE, default/UTF8
ENCODING (including numeric 6) and C/POSIX locale options. CREATEDB is the effective
role's direct attribute (or superuser), with SET ROLE authority required for the
new owner. Prepare is syntax-only; execution checks read-only/transaction
admission and publishes one complete target or nothing. Option recognition
preserves parser-decoded case. Value validation precedes owner and actor checks;
invalid locale providers return 42P17 before capability checks. Diagnostics
interpolate decoded names without string-literal escaping. Arbitrary template
sources (including `template0`), non-C/POSIX locales, provider and physical
strategy mechanisms return explicit 0A000 rather than succeeding without their
effects. DROP/RENAME, mutable template administration and database flags/settings
are not supplied by this create path.

`pg_database` captures every registered target in this cluster, never other DSNs.
Each row has its own stable identity, owner/ACL and runtime freezing horizon.
Encoding 6, provider `c`, C/C locales, `datistemplate=false`, `datallowconn=true`
and `datconnlimit=-1` are represented immutable defaults. Physical placement and
multixact horizons, ICU locale/rules and collation versions remain typed NULL.
Fresh/held executions capture current registry authority; returned Rows keep
their original inventory and types. Named database grants and owner changes
resolve registered noncurrent targets through that same authority; table access
remains database-local.

The shared mutex orders short metadata publication before local session registry
inspection for cluster-wide role limits. Never hold it across external I/O,
callbacks, relation-lock waits, session teardown or nested replacement: source
capture and destination install are separate nonnested phases. Role dependencies
include other targets' persistent objects/default ACLs; external role-reference
labels remain scoped to their owning database. Native controls capture the
selected runtime and preserve registry/roles/other targets during replacement.

Notification accounting shares this mutex and one immutable cluster head.
The existing transaction publisher captures every exact target/registry/listener
root, prepares replacements unlocked, and validates them alongside row/catalog
authority before any installation. Only local targets receive payload batches;
busy registered listeners across all targets retain global unread positions.
Idle publication/outer boundaries process eagerly. Usage reads the minimum
unread boundary without allocation or consuming Ready data. Capacity vetoes are
prepared before the fence and raised only for validated roots; the installation
fence remains allocation-, callback- and failure-free. Native replacement retains
the head and live listener roots; none is serialized.


## Hosts

Each implementation carries a compile-time assertion; root code names
subpackage types directly (no aliases).

| Root type | Implements |
| --- | --- |
| `statementHost` (a `StatementScope` conversion; `statement_host*.go`, `*_runtime.go`) | `exec.Host`, `dml.Host`, `dml.FinishHost`, `index.WriteHost`, `partition.RecipeHost`, `fdw.WriteRuntime`, `regobj.NoticeHost`, and the bodies of the `functions` evaluation capabilities (`ACLRuntime`, `AdvisoryRuntime`, `BlockingRuntime`, `CatalogInquiryRuntime`, `SessionInquiryRuntime`, `SequenceRuntime`, `TransactionInquiryRuntime`, `LargeObjectRuntime`, `PgrowlocksRuntime`, `FTSQueryRuntime`) that `*exec.Context` forwards |
| `ddlHost` | `ddl.Host`, `ddl.SchemaHost`, `routine.DDLHost`, `typedef.StageHost`, `table.Host`, `index.Host`, `index.KeyHost`, `fdw.Host`, `role.Host`, `role.StageHost`, `partition.Host`, `view.Host`, `view.StageHost` |
| `alterHost` | `table.AlterHost` |
| `extensionHost` (`extension_host.go`) | `extension.Host`, `extension.Stage` |
| `bindingHost` (`analysis_host.go`) | `analyze.Host` |
| `engineCatalogRuntime` (`catalog_source_runtime.go`) | `pgcatalog.Runtime` |
| `enginePreparation` (`exec_preparation.go`) | `exec.Preparation`, installed with `exec.InstallPreparation` |
| `restoreHost` (`restore_run.go`) | `control.Host` |
| `sessionHost` (an `Engine` conversion; `session_host.go`) | `session.Host` |

## Lock order

`replacementMu` and `TransactionOwner.Mu` precede `Engine.mu`, which precedes
`Session.Mu`, `SettingsStore.Mu` and `Engine.sessionMu`; the lock manager's
mutex is innermost and never taken under `Engine.mu`. Callbacks and external I/O
run outside these locks ([full rules](lock/README.md#mutex-ownership-and-lock-order)).

## Prepared statements

- Ordinary prepared SQL keeps one private analysis-identity cell
  (`preparedAnalysis` in `prepared_rebind.go`, owned by `Engine.mu` and
  physical-session admission), not an executable plan, data or permission cache.
- While the cell is valid, admission preserves per-occurrence target, nested,
  view and unused-CTE identities. Only dependency, effective-path, namespace or
  DISCARD invalidation reanalyzes the original source; creating a shadow in an
  existing schema is not by itself a trigger. Invalidation reuses the effective
  namespace comparator and the dependencies collected by analysis, never an
  all-DDL epoch.
- Admitted explicit user-type casts capture identity per source occurrence,
  including supported companion casts. Identity-only revisions for enum,
  composite and installed types compare immutable executable definitions and
  authority, with child types in the same dependency cell. Domains retain their
  domain-specific stamps.
  Reanalysis normally retires these casts alongside the existing cast group.
  An EAN13 owner/ACL-only invalidation in the same resolution environment may
  carry its nominal cast identity into a single candidate analysis. Executable
  analysis is still invalidated; current lookup, authority and coercion run
  before fresh captures publish. A failed candidate leaves the original casts
  retired. No alias repairs obsolete source spelling.
  Direct held EAN13 literals use candidate-only datum reownership from the exact
  captured analysis descriptor to the genuine current installed manifest member,
  under the same environment and definition gates. A local pointer import
  preserves packed payloads and NULL framing without changing ordinary Retag or
  catalog import roots. Identity-only attempts leave original temporals intact;
  successful reanalysis publishes fresh temporals and source analysis together.
- Full invalidation publishes atomically, after analysis, signature and
  provenance succeed: genuine temporal scalar/array candidates, sequence/regtype
  candidates and identity stamps. It preserves native logical enum, domain and
  composite captures, arrays with catalog-owned leaves (including embedded
  temporal fields) and existing lifetime diagnostics. Never re-input old enum
  labels or rescue replacement logical identities. A later importer or execution
  failure need not undo a successful analysis.
- Fixed formals and results stay immutable; current data, ACLs and compatible
  metadata stay fresh. The resolution environment is captured before any
  relation wait: never retarget after lock admission or certify an older queued
  resolution under a newer environment.
- Snapshot-requiring analysis may establish a stable transaction's retained
  public value and tuple roots/frontier. RC analysis never freezes execution:
  query source admission selects its pair after relation waits, while utility
  entry selects before utility-specific waits. Later roots and cursor producers
  consume that same immutable pair; expression evaluation never recaptures it.
- DML synthetic query roots are not stable AST owners: retain the original table
  occurrences. Writable views retain their logical target identity, not the
  translated physical base.
- Shadow retention applies to statement-boundary shadows that already exist, not
  to changes made during admission waits. Record the actual lexical resolution
  independently of the retained target/source; when that observation changes
  during a wait, every route returns native `ErrCatalogReplaced`, with no warmup
  exemption, retarget or replay. Keep the direct, new-prepare and held conflict
  tests unchanged.

## Read-only utility execution

- `readOnlyUtilityEligibility` (`transaction_isolation.go`) rejects supported
  modifying utility tags before object resolution, authorization or relation
  locks. DML keeps its analysis-before-read-only diagnostics. Prepare never
  applies this execution rejection.
- `executionEligibility` admits explicit-transaction utilities only within its
  bounded exceptions: selected ALTER TABLE nullability, named NOT NULL and DROP
  COLUMN lists, and sequence DDL. Do not enable unrelated utilities.

## Immutable settings registry

`Engine.settings` holds one [`settings`](settings/README.md) `Registry` by value,
built before provider registration and publication; providers borrow a pointer
to it, and descriptor validation and grant-name canonicalization use the same
registry. Notice levels, physical-session values, the settings journal and
timeout publication belong to [`session`](session/README.md).

## System catalog producer contract

Producers, the OID registry and descriptor specs live in
[`pgcatalog`](pgcatalog/README.md); the root keeps orchestration.

- `openCatalogSourceLocked` (`catalog_source_capture.go`) looks up
  `pgcatalog.ProducerFor` and chooses the per-OID catalog/role roots,
  presentation and unlock timing; it is not a second dispatcher. Only `pg_locks`
  and lazy `pg_sequences` bypass producers.
- `engineCatalogRuntime` exposes values per call, never Engine, StatementScope,
  AuthorizationView or bindings. Settings rows capture one coherent
  physical-session snapshot after unlocking `Engine.mu`; `pg_locks_capture.go`
  keeps an unlock/capture/relock epoch fence.
- `catalog_sources.go` registers descriptors from `pgcatalog.Specs`, preserving
  registration order, OIDs, columns and row/array links.
- Provenance is same-pointer authority (`catalogStore.owner()`), never equal
  names, OIDs or epochs.

## Catalog writes and built-in seed

- `catalog.AdvanceRevisionFrontier` is pure and saturating and never reserves
  the global allocator; publication commits its local frontier only at
  successful installation under `Engine.mu`.
- `CatalogStage` embeds `catalog.Candidate`, which owns the catalog-only
  publication fences, the apply step, the history markers and the creation
  ACLs; DDL authorization, lifecycle, rowtype synchronization, storage
  coordination, the extension materialized-view fence and engine-dependent
  validators stay in the root. Recipe rewrites retain the original routine authority, never
  recovering it from an OID.
- Every engine seeds the same built-in catalog in one bulk load (`newEngine`'s
  `BeginBulkLoad` … `finishSeed`) from pure constructors; `builtin_catalog.go`
  validates the shared seed once per process, and each engine validates and
  binds only its database descriptor and option-dependent parts. Check
  `make bench` allocations when adding built-ins.

## Function runtimes

`functions` nodes own binding, immutable recipes and argument demand; the root
runtime bodies (`*_runtime.go`) keep the authority. No prepared node caches an
owner.

`fts_query_runtime.go` executes SQL rewrite rules inside the caller's admitted
operation, transaction and statement snapshot. Lexical scratch and zero inner
parameters are scoped stackwise; private staged writes remain visible. The
ordinary parser, analyzer, admission and query producers supply exact typed rule
rows, never a driver connection or a second SQL executor. The consumer fetches
100 rows before checking result shape and retains one bounded typed rule batch.
Deletion stops rule application, but the next batch is still fetched before
testing emptiness again. Original fetch errors remain distinct from prepare/open
errors. The cursor closes before restoring caller scratch; outer publication
remains eager.


### Catalog inquiry and session identity

`catalog_inquiry_runtime.go` and `session_inquiry_runtime.go` keep in the root
SQL-callable precedence, analyzer/coercion authority, statement snapshots, lock
and authorization recapture, pending-TEMP staging, schema caches and lazy
routine admission, as well as stored literal pairing, logical identity
remapping, prepared invalidation and catalog-replacement fences.

### Other runtimes

- Ordinary blocking reads never nest `Engine.mu` and manager mutexes.
  Safe-snapshot capture and transaction-inquiry XID/timestamp sampling run under
  `Engine.mu`; members are packed from the manager's snapshot clone outside it.
- `sequence_runtime.go` runs the catalog epoch fence (with TEMP epoch
  revalidation) before and after RowExclusive `acquireSequence`, then the ACL
  and read-only checks; call semantics belong to [`sequence`](sequence/README.md).
- `expr.Source.SourceAdmission` and `exec.OpenFunctionSource` own source
  admission; stage rewrites use `expr.Substitute` without re-resolution. Stored
  literal capture validates every admitted `expr.Capturable` operand before
  writing any annotation.
- Stored DEFAULT closure extends through a SQL routine only for a real checked
  call and never executes the callee body. The release identity index is
  diagnostic only (42883 versus 0A000); it never grants execution.

## Extensions

Records, decisions and the root's orchestration are in
[`extension`](extension/README.md). UPDATE keeps its read fences (including
ACLs) from the staged snapshot, not from descriptors observed after outside-lock
provider validation; CREATE and UPDATE share one final publication. Retirement
captures the complete target set, including automatic aliases, and rejects
incoming dependencies with `catalog.Conflict` before mutation.

Identity ALTER remints citext/EAN13 values through a transient exact
source-descriptor-to-current-descriptor map, never an OID-only permission.
Generation reownership restores the prior import map before returning and leaves
retained catalog/storage roots unchanged. Base rename, move and ownership do not
change installed I/O routine namespaces or owners; canonical manifest roles own
portable ownership records and identity replay.

## VACUUM and row mutation

- `vacuum.go` runs in one StatementScope and transaction/ticket; per-target
  holds return to manager checkpoints outside engine/session locks.
  `vacuum_publication.go` rechecks root/storage/shape/authority/gate tokens
  before atomic installation; peer DML causes recapture, never SQL replay.
- MultiXact records, counters and history belong to the lock manager. CHECK and
  branch admission precede the physical event; index and RETURNING evaluation
  follow it. Shared mutations journal successor/deletion events while private
  roots are locked and flush after the engine/owner unlock. No `Engine.mu` →
  manager-mutex acquisition exists. Finish discards private stages on failure.

## Server protocol and timeouts

The wire adapter ([`cmd/stuntgres`](../../cmd/stuntgres/README.md)) reuses the
session, transaction, prepared-analysis and typed-frame authorities.

- `session.ProtocolCycle` retains implicit transaction work, not an operation or
  admission lock; each message acquires its own command context. Wire portals
  share names with SQL cursors but own their position and lifetime.
- `ExecuteProtocolSimple` streams plain SELECT from the live producer; other
  row-returning commands fill a row store first, so an inner failure emits no
  partial rows. Stored typed rows are never rewritten or replayed.
- `CopyProtocol` hooks run outside engine/publication locks and admission; wire
  COPY FROM holds a cancellation owner through decoding, released on every exit.
- Session checks observe due transaction/idle budgets under `Session.Mu` before
  publishing a lease, with one timestamp for both slots. A statement timeout
  before `StartStatement` fails its explicit block synchronously through
  `StatementScope.Finish`'s disposal; no timer callback mutates the transaction.

## Native Dump and Restore

Drivers, the Emit table and hook entry types live in [`control`](control/README.md);
the root keeps Dump's `Engine.mu` window and Collect tables, the restore stage,
`restoreRun` with every `restoreHooks` entry, and the atomic `replaceCatalog`.

Private Restore definition lookups capture the live authenticated bootstrap
through the existing trusted-destination/import marker. Only the lookup permission
view is elevated: named owner, `$user`, session identity, creation checks and ACLs
remain separate. Ordinary operations and untrusted/foreign captures gain no lookup
authority.

## Testing

Root tests are package-level units of these contracts; public SQL behavior is
tested only in `reference/` ([docs/TESTING.md](../../docs/TESTING.md)). SHOW ALL
description rules are in the [`settings` README](settings/README.md#testing).
Add `RACE=1` for lifecycle, cancellation and contention selections.

- Fixtures never enter `Engine.Execute` while owning admission (that acquires
  its mutex recursively); create stored recipes before retaining admission.
- Reduced fixtures copy real access-method descriptors and handlers
  (`operatorFamilyFixtureState`), never production OID/name fallbacks.
- Lock-wait tests observe the exact queued request before triggering a caller
  deadline (`lockWaitCaller`) and use `Manager.ExpireWait`, never sleeps. Worker
  goroutines return outcomes; only the test goroutine asserts.
- Snapshot units publish controlled registry/tuple frontiers and select through
  real retained views and heap sources. Utility timing observes an actual queued
  target lock; safe-snapshot units check accepted and discarded public/root pairs.

### Advisory timer scheduling and shared result waits

- Timer cases of `TestAdvisorySessionQueueTimersDeadlock` run under
  `testing/synctest` with real deadlines, `context.AfterFunc` and manager lock
  timers, never manual expiration. Queue the exact request before advancing
  virtual time, inspect state only after `synctest.Wait`, and join the delayed
  consumer on every path. Timeouts keep location-free 55P03/57014 diagnostics
  and leave no grant, pending request or journal entry.
- `deadlockResult` waits for the authoritative `Done` (never `Terminal` alone),
  reads `Err` only after `Done`, and uses one stopped one-second guard.

```sh
make check RUN='^TestPreparedRebind'                  # prepared analysis identity
make check RUN='^TestDatabaseCreate'                   # registry, template, publication, target authority
make check RUN='^TestSetting'                         # settings registry
make check RUN='^Test(CatalogSource|InfoUserMappingsProducer|PGSettingsCatalogACL)'  # producers; one catalog: ^TestPG<Name>
make check RUN='^Test(AlterDropColumnRevisionFrontier|CatalogConcurrentReplacement|AccessMethod|Builtin)'  # catalog writes, seed
make check RUN='^Test(CatalogRelations|NamespaceIdentity|ObjectAddress)'  # catalog inquiry and identity
make check RUN='^TestNotification'                     # immutable session/cluster queues and publication
make check RUN='^TestIsnProvider'                      # installed identifier authority and import
make check RUN='^Test(Advisory|BlockingPids|DeadlockResultScheduling|RelationDeadlock|RelationLockWaits|PGLocks)'  # advisory, waits, deadlocks
make check RUN='^Test(UpdateCore|TransactionIdentity|CommitTime)'  # MultiXact, transaction identity
make check RUN='^TestSnapshot'                        # binding, retained command/transaction pairs and physical frontier filters
make check RUN='^Test(Serial|SourceProtocol|ViewLiteralProtocol)'  # sequences, sources, stored capture
make check RUN='^Test([A-Za-z]+RoutineOriginalAuthority|SQLRoutine|UnavailableFunctionIdentityBoundary)'  # routines
make check RUN='^Test(Extension|PGDepend|CatalogDrop|DropOwned)'  # extensions, drop fences
make check RUN='^Test(Vacuum|Protocol|SessionTimeout|StatementTimeoutAdmission)'  # VACUUM, protocol, timeouts
```
