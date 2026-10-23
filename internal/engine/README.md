# internal/engine

The root engine is the analysis host and owns transactions, settings and
lifecycle publication: `Engine.mu`, the catalog store, statement scopes and
admission, catalog/storage/visibility installation, and the orchestration of
native Dump/Restore and the wire protocol. Subsystem logic lives in the layered
subpackages ([`docs/ENGINE-SPLIT.md`](../../docs/ENGINE-SPLIT.md) §3), each with
its own README. Subpackages never import the root; they reach it through the
host interfaces below. Put new code in the lowest package that can own it
(new builtins go to the [`functions`](functions/README.md) registry).

## Hosts

The root implements each subpackage's host interface, with a compile-time
assertion beside the implementation:

| Root type | Implements |
| --- | --- |
| `statementHost` (a `StatementScope` conversion; `statement_host*.go`, `*_runtime.go`) | `exec.Host`, `dml.Host`, `dml.FinishHost`, `index.WriteHost`, `partition.RecipeHost`, `fdw.WriteRuntime`, `regobj.NoticeHost`, and the bodies of the `functions` evaluation capabilities (`ACLRuntime`, `AdvisoryRuntime`, `BlockingRuntime`, `CatalogInquiryRuntime`, `SessionInquiryRuntime`, `SequenceRuntime`, `TransactionInquiryRuntime`, `LargeObjectRuntime`, `PgrowlocksRuntime`) that `*exec.Context` forwards |
| `ddlHost` | `ddl.Host`, `ddl.SchemaHost`, `routine.DDLHost`, `typedef.StageHost`, `table.Host`, `index.Host`, `index.KeyHost`, `fdw.Host`, `role.Host`, `role.StageHost`, `partition.Host`, `view.Host`, `view.StageHost` |
| `alterHost` | `table.AlterHost` |
| `extensionHost` (`extension_host.go`) | `extension.Host`, `extension.Stage` |
| `bindingHost` (`analysis_host.go`) | `analyze.Host` |
| `engineCatalogRuntime` (`catalog_source_runtime.go`) | `pgcatalog.Runtime` |
| `enginePreparation` (`exec_preparation.go`) | `exec.Preparation`, installed with `exec.InstallPreparation` |
| `restoreHost` (`restore_run.go`) | `control.Host` |
| `sessionHost` (an `Engine` conversion; `session_host.go`) | `session.Host` |

Root code names subpackage types directly (`settings.`, `session.`, `exec.`,
`pgcatalog.`, ...); there are no aliases.

## Lock order

The engine-wide mutex order and the lock manager's rules are in
[lock/README.md](lock/README.md#mutex-ownership-and-lock-order):
`replacementMu` and `TransactionOwner.Mu` precede `Engine.mu`, which precedes
`Session.Mu`, `SettingsStore.Mu` and `Engine.sessionMu`; the lock manager's
mutex is innermost and is never taken under `Engine.mu`. Callbacks and external
I/O run outside these locks.

## Prepared statements

- Ordinary prepared SQL keeps one private analysis-identity cell
  (`preparedAnalysis`, `prepared_rebind.go`), not an executable plan, data or
  permission cache. `Engine.mu` and physical-session admission own it.
- While the cell is valid, admission preserves per-occurrence target, nested,
  view and unused-CTE identities. Only dependency, effective-path, namespace or
  DISCARD invalidation reanalyzes the original source; creating a shadow in an
  existing schema is not by itself a trigger. Invalidation reuses the effective
  namespace comparator and the dependencies collected by analysis, never an
  all-DDL epoch.
- Full invalidation publishes atomically, after analysis, signature and
  provenance succeed: genuine temporal scalar/array candidates, sequence/regtype
  candidates and identity stamps. It preserves native logical enum, domain and
  composite captures, arrays with catalog-owned leaves (including embedded
  temporal fields) and existing lifetime diagnostics. Never re-input old enum
  labels or rescue replacement logical identities. A later importer or execution
  failure need not undo a successful analysis.
- Fixed formals and results stay immutable; current data, ACLs and compatible
  metadata stay fresh.
- The resolution environment is captured before any relation wait. Never
  retarget after lock admission, and never certify an older queued resolution
  under a newer environment.
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
  COLUMN lists, and sequence DDL. Do not enable unrelated utilities as a side
  effect.

## Immutable settings registry

Definitions, the builtin table, parsing, validation and the registry live in
[`settings`](settings/README.md). `Engine.settings` holds one
`settings.Registry` by value, built before provider registration and
publication; parameter providers borrow a pointer to it, and descriptor
validation and grant-name canonicalization use the same registry. Search-path
decisions are in [`analyze`](analyze/README.md); `schema_path.go` keeps the
wrappers that read `search_path` under the session settings mutex. Notice
levels, physical-session values, the settings journal and timeout publication
belong to [`session`](session/README.md).

## System catalog producer contract

Producers, their `Input`/`Runtime` contract, the OID registry and descriptor
specs live in [`pgcatalog`](pgcatalog/README.md). The root keeps the
orchestration:

- `openCatalogSourceLocked` (`catalog_source_capture.go`) looks up
  `pgcatalog.ProducerFor` and chooses the per-OID catalog/role roots,
  presentation and unlock timing; it is not a second dispatcher.
  `captureCatalogSourceRows` alone wraps producer rows into an `exec.Source`.
  Only `pg_locks` and lazy `pg_sequences` bypass producers.
- `engineCatalogRuntime` implements `pgcatalog.Runtime` per call and on demand,
  exposing values only: never Engine, StatementScope, AuthorizationView or
  bindings. Settings rows capture one coherent physical-session snapshot after
  unlocking `Engine.mu`.
- `pg_locks_capture.go` keeps the unlock/capture/relock epoch fence and never
  takes the lock manager mutex under `Engine.mu`.
- `catalog_sources.go` registers descriptors from `pgcatalog.Specs`, preserving
  registration order, OIDs, columns and row/array links.
- Provenance is same-pointer authority (`catalogStore.owner()` returns the
  stable `*catalog.Owner`), never equal names, OIDs or epochs.
- `pg_settings` consumers (direct, fresh and held) recheck relation and column
  SELECT authority; a revocation after the statement's metadata admission
  returns `ErrCatalogReplaced` and publishes no rows.

## Catalog writes

- `catalog.AdvanceRevisionFrontier` is pure and saturating; it never reserves
  the global allocator. Publication accumulates a local frontier and commits it
  only at successful catalog/storage/visibility installation under `Engine.mu`.
- `CatalogStage` embeds `catalog.Candidate`, whose overlay maps are private; DDL
  authorization, lifecycle, rowtype synchronization, storage coordination and
  engine-dependent validators stay in the root.
- Access-method descriptors and native handler associations are seeded in the
  ordinary catalog before family/class validation; `access_method.go` fences
  method/handler consumers at publication under `Engine.mu`. Heap resolves
  through captured method/handler validation to `visibility.HeapOperations`.
- `Engine.SetObjectACL` replacement clears constructor-synthetic ACL provenance
  for explicit default-equivalent, empty and owner-only ACLs.
- Shape completion, retagging, relocation and stored-recipe grafting retain the
  original routine authority; they never recover it from an OID.

## Built-in catalog seed

Every engine seeds the same built-in catalog from compiled tables; only the
current database descriptor depends on engine options. `builtin_catalog.go`
validates the seed once per process and shares its procedure-name index and
persistent name map; each engine validates and binds only its database. A seed
that depends on new engine options must be validated per engine and bound after
the shared names (or not shared). Seeding is one bulk load (`BeginBulkLoad` in
`newEngine`, ended by `finishSeed`) of values from pure constructors
(`routine.BuiltinProcedures`, `typedef.BuiltinConversionProcedures`, ...),
written by the `install*` methods in `newEngine`'s order. Check `make bench`
allocations when adding built-ins.

## Function runtimes

`functions` nodes own binding, immutable recipes and argument demand; the root
runtime bodies keep the authority.

### Catalog inquiry and session identity

`catalog_inquiry_runtime.go` and `session_inquiry_runtime.go` back the
capabilities. The root keeps SQL-callable precedence,
analyzer/coercion authority, statement catalog/role/comment/visibility
snapshots, locks and authorization recapture, `schemaInquiryPathLocked`,
pending-TEMP staging/publication, schema caches, FK/rule/view expression
authority, address lookup, catalog index interpretation, lazy routine
admission, deparse/tree engines and serial ownership lookup. Stored literal
pairing, logical identity remapping, prepared invalidation, snapshot manifests
and catalog-replacement fences also stay here.

### Other runtimes

- **Advisory and blocking.** `advisory_runtime.go` and `blocking_runtime.go`
  keep resource construction, current session/transaction selection, admission,
  wait/readmission, notices, manager reads and SSI safe waits. Ordinary blocking
  reads never nest `Engine.mu` and manager mutexes; safe-snapshot capture stays
  under `Engine.mu`. No prepared node caches an owner.
- **Sequences.** `sequence_runtime.go` runs, in this order: the evaluation
  prelude and live statement scope, OID-to-reference construction (and
  `lastval`'s session provider), the catalog epoch fence (including TEMP epoch
  revalidation) before and after RowExclusive `acquireSequence`, the ACL and
  read-only checks and `sequenceGeneratorLocked`. Privileges, the read-only
  error and call semantics are [`sequence`](sequence/README.md)'s
  `CallPrivileges`, `ReadOnlyCallError` and `CallLocked`.
- **Transaction inquiry.** `transaction_inquiry_runtime.go` implements
  `TransactionInquiryRuntime`; `transaction_identity_store.go` and
  `commit_timestamp_store.go` keep their domain/epoch fences. Last XID and
  timestamp are sampled together under `Engine.mu`; member records are packed
  from the manager's snapshot clone outside locks. Transaction identity, commit
  order and SSI belong to [`visibility`](visibility/README.md).
- **Large objects.** VOLATILE SQL body entry captures an immutable
  command-visible large-object READ catalog in `sqlRoutineScratch`, not the root
  effect journal; nested queries get fresh captures and unwind restores the
  outer view while root-owned command effects stay for publication.

## Closed function sources and stored capture

- `expr.Source.SourceAdmission` and `exec.OpenFunctionSource` own source
  admission and opening; root catalog/extension runtime operations keep their
  state and validation authority. Equivalent stage rewrites use
  `expr.Substitute` without provider re-resolution; catalog generation remapping
  uses Rebuild/Retag.
- Stored literal capture pairs the raw normalized preorder with explicitly
  admitted `expr.Capturable` written operands. Engine wrappers run first, and
  capture validates every operand before writing any annotation. SQL routine
  wrappers validate syntax and captured procedure authority before exposing
  written actuals.
- SQL/JSON display and stored replay interpret the node's `expr.Rendering`; the
  root unwraps only a recognized formatted-input conversion and supplies a
  RETURNING usage only from its owning conversion.

## SQL routine admission

- SQL routines extend stored DEFAULT closure only when a real checked SQL call
  occurs in it, never making a rejected volatile expression admissible; the
  callee body is not executed while admitting a default.
- An ordinary call without a native provider distinguishes a missing signature
  (42883) from a viable released core signature whose native capability is
  absent (0A000). The release identity index is diagnostic evidence only: it
  never grants execution, adds catalog rows or installs extension functions.
  Diagnostic arity honors trailing defaults from the native declaration
  inventory (or lookup-only release metadata); defaults are never evaluated.

## Extensions

- Provider records, members and manifests live in
  [`extension`](extension/README.md). Providers stage through `extensionHost`,
  always under `Engine.mu`; CREATE/UPDATE/DROP decisions are `extension`
  functions, and the root keeps dispatch, `Engine.mu`, publication and the
  removal pipeline.
- Versions use one immutable catalog inventory and the statement-owned stage.
  UPDATE keeps the exact extension and predecessor-member read fences (including
  ACLs) from the staged snapshot, not descriptors observed after outside-lock
  provider validation. CREATE and UPDATE share final authority/name/epoch
  publication; no retry, second registry or script runner.
- Retirement captures the complete target set, including automatic aliases. The
  incoming-dependency preflight inspects changed committed owners through their
  candidate descriptors and rejects with `catalog.Conflict` before mutation,
  never by adoption or a second cascade.

## VACUUM

- `vacuum.go` owns selection and admission: one StatementScope, one
  transaction/ticket. Per-target AccessShare/ShareUpdateExclusive holds return to
  manager checkpoints outside engine/session locks between targets.
- SKIP_LOCKED applies to selection and work locks. Only a genuine manager
  `ErrUnavailable` becomes the 55P03 warning/omission, after restoring that
  target's checkpoint; cancellation and timeouts stay errors. Warnings are
  delivered in encounter order after checkpoints and metadata mutexes are
  released.
- Expansion is breadth-first in direct-child OID order, deduplicated per root
  only. Only VACUUM opts into `analyze.ExplicitPath`'s peer-TEMP resolution.
- `vacuum_publication.go` materializes detached immutable candidates, remaps
  indexes without evaluating keys and rechecks root/storage/shape/authority/gate
  tokens before atomic installation. Peer DML causes recapture, never SQL
  replay; nothing fails after the install region's first write.
- `vacuum_metadata.go` keeps full-XID bounds, statistics and phase records under
  `Engine.mu`; progress visibility uses backend startup authentication
  authority. Relation births reconcile bounds in constant time.

## Row mutation and MultiXact

MultiXact records, counters and history belong to the lock manager. CHECK and
branch admission precede the physical event; index and RETURNING evaluation
follow it. Shared mutations journal successor/deletion events while private
roots are locked and flush after the engine/owner unlock, before caller-visible
evaluation. No `Engine.mu` → manager-mutex acquisition exists; status reads
ticket atomics. Ordinary UPDATE accumulates distinct accepted physical targets
by amortized append, and Finish discards private stages on failure.

## Server protocol

The wire adapter reuses the session, transaction, prepared-analysis and
typed-frame authorities.

- `session.ProtocolCycle` retains implicit transaction work, not an operation or
  admission lock; each message acquires its own command context. Sync closes
  implicit work, never an explicit transaction.
- Prepared SQL and wire statements share names. Wire portals share collision,
  Close visibility and the savepoint birth frontier with SQL cursors but own
  their position and lifetime. Closing a statement keeps its bound portals.
- Each Execute presents rows with its fresh scope settings and per-row borrowed
  catalog readers; stored typed rows are never rewritten or replayed to refresh
  presentation.
- `StatementScope.ExecuteProtocolSimple` uses the canonical execution core.
  Plain SELECT streams from the live producer (schema before row demand; emitted
  rows stand after a later error). RETURNING, modifying-CTE, utility and SQL
  EXECUTE fill a row store first, so an inner failure emits no RowDescription or
  partial rows.
- Simple Query removes only the unnamed portal, at planned-command execution or
  utility entry. Failed unnamed Parse drops the old statement but keeps bound
  portals; named Parse publishes after analysis and never replaces a duplicate.
- Completion and retirement are distinct: exhausted row portals stay fetchable;
  completed non-row commands reject Execute with 55000. A failed portal keeps its
  name (Execute 55000, duplicate Bind 42P03) until Close or retirement. Terminal
  disposal never resumes an executor or replaces the primary error.
- COPY uses the native text/CSV/binary executor (`StatementScope.CopyProtocol`);
  hooks run outside engine/publication locks and admission, and the terminal
  input hook can veto rows before publication.
- Binary providers initialize at fixed points: COPY FROM receive functions
  before CopyInResponse (every live attribute, in order); COPY TO send functions
  after CopyOutResponse, before any row demand; row destinations on their first
  actual tuple (never at Bind, Describe or for empty results). A binary NULL Bind
  argument resolves its receive provider before typed NULL construction.
- Wire COPY FROM holds a cancellation owner from before CopyInResponse through
  decoding; the caller arms it before Start and cleanup covers every exit.
  Conversion errors keep row/column context and win; a successful conversion
  observes pending cancellation before returning a row.
- `Session.EndRequested` observes ending without a lease; ending cancels any
  held input view, so a user-cancel cause cannot hide termination.

## Session timeouts

- Session checks and operation admission observe due transaction/idle budgets
  under `Session.Mu` before publishing a lease or invalidating the idle slot.
  One timestamp feeds the expiry decision for both slots, so delayed callback
  delivery cannot admit work on an expired owner.
- A command's own statement timeout before `StartStatement` synchronously fails
  its explicit block through the same disposal as `StatementScope.Finish`; no
  timer callback mutates the transaction. Ordinary caller cancellation keeps its
  context identity, and a timed-out waiter never disposes a live COPY
  reservation.

## Native Dump and Restore

The drivers, Emit table and hook entry types live in
[`control`](control/README.md). The root keeps the `Engine.mu` window and the
Collect tables of Dump, the restore stage, `restoreRun` with the `restoreHooks`
table and every entry, and the atomic `replaceCatalog`.

## Testing

Root tests are package-level units of these contracts; public SQL behavior is
tested only in `reference/` ([docs/TESTING.md](../../docs/TESTING.md)).

- Fixtures must not enter `Engine.Execute` while already owning admission
  (that recursively acquires its mutex); create stored recipes before retaining
  statement admission.
- Reduced structural fixtures keep real access-method descriptors and native
  handler procedures; `operatorFamilyFixtureState` copies them from the
  bootstrap catalog. Missing fixture authority is never repaired by production
  OID/name fallbacks.
- Lock-wait tests observe the exact queued request before triggering a caller
  deadline (`lockWaitCaller`), and use logical expiry (`Manager.ExpireWait`)
  instead of sleeps. Worker goroutines return outcomes; only the test goroutine
  asserts.
- SHOW ALL descriptions are asserted independently against PostgreSQL 18.0
  `short_desc` (see the [`settings` README](settings/README.md#testing)).

### Advisory timer scheduling and shared result waits

- Timer cases of `TestAdvisorySessionQueueTimersDeadlock` run under
  `testing/synctest` with real `context.WithDeadline`, `context.AfterFunc` and
  manager lock timers, never manual expiration or a second timer authority. The
  exact request is queued before virtual time advances; state is inspected only
  after `synctest.Wait`, and the delayed consumer is joined on every path.
- Timeouts keep exact location-free 55P03/57014 diagnostics and leave no grant,
  pending request or journal entry; the blocker and prior holds survive.
- `deadlockResult` waits for the authoritative `Done` (never `Terminal` alone),
  reads `Err` only after `Done`, and uses one stopped one-second guard that is
  never refreshed.

```sh
make check RUN='^TestPreparedRebind'                  # prepared analysis identity
make check RUN='^TestSetting'                         # settings registry
make check RUN='^Test(CatalogSource|InfoUserMappingsProducer|PGSettingsCatalogACL)'  # producers; one catalog: ^TestPG<Name>
make check RUN='^Test(AlterDropColumnRevisionFrontier|AlterSchema|CatalogConcurrentReplacement|DefaultPrivileges|ObjectACLType)'  # catalog writes
make check RUN='^TestAccessMethod'                    # access methods
make check RUN='^TestBuiltin'                         # built-in seed
make check RUN='^Test(CatalogRelations|NamespaceIdentity|ObjectAddress)'  # catalog inquiry and identity
make check RUN='^Test(Advisory|BlockingPids|DeadlockResultScheduling|RelationDeadlock)'  # advisory, blocking, deadlocks
make check RUN='^Test(RelationLockWaits|RowLockView|PGLocks)'  # lock waits, pg_locks
make check RUN='^TestUpdateCore'                      # MultiXact and UPDATE core
make check RUN='^Test(TransactionIdentity|CommitTime)'  # transaction identity, commit time
make check RUN='^TestSerial'                          # serial and sequence defaults
make check RUN='^Test(SourceProtocol|Series|Subscripts|NumericSeries|ArrayRelational)'  # function sources
make check RUN='^Test(ViewLiteralProtocol|SQLJSON)'   # stored capture, SQL/JSON rendering
make check RUN='^Test([A-Za-z]+RoutineOriginalAuthority|JSONBRecordRoutine|TIDRoutineAuthority|RoutineBody)'  # routine authority
make check RUN='^Test(SQLRoutine|UnavailableFunctionIdentityBoundary|LargeObjectSQLRoutineScratch)'  # SQL routines
make check RUN='^Test(Earth|Extension|AvailableExtensions)'  # extensions
make check RUN='^Test(PGDepend|CatalogDrop|DropOwned)'  # drop and dependency fences
make check RUN='^TestVacuum'                          # VACUUM
make check RUN='^Test(Protocol|PreparedSQLActual)'    # server protocol
make check RUN='^Test(SessionTimeout|StatementTimeoutAdmission)'  # timeouts
make check RUN='^TestQuery(Search|Cycle)'             # recursive SEARCH/CYCLE
```

Add `RACE=1` for lifecycle, cancellation and contention selections.
