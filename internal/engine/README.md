# internal/engine

The root engine is the analysis host and owns transactions, settings and
lifecycle publication: `Engine.mu`, the catalog store (`catalog.Store`, see
[its README](catalog/README.md)), statement scopes and admission,
catalog/storage/visibility installation, and the orchestration of native
Dump/Restore and the wire protocol. Subsystem logic lives in layered
subpackages ([`docs/ENGINE-SPLIT.md`](../../docs/ENGINE-SPLIT.md) §3) that never
import the root and reach it through the hosts below.

Trigger definitions and lifecycle utilities publish immutable relation payloads
through the ordinary catalog stage and transaction journal. Definitions,
dependencies and row effects commit together or restore at rollback/savepoints;
each DML activation captures the current owner-private trigger list, not a list
cached by its prepared handle. CREATE/REPLACE admits ShareRowExclusive on the
root and descendants; rename admits AccessExclusive before mutation. Addressed
DROP and extension metadata use the generic AccessShare target admission;
`view.PlanTrigger` then decides the change under `Engine.mu`. Clone
synchronization (`view.SynchronizeTriggersLocked`, called from
`ValidateCatalogStage`) preserves exact Parent/OID identity and fails a
descendant name collision before publication. Extension x
retirement uses the shared dependency closure, including the partition-owner
deletion guard; CASCADE does not grant upward partition deletion authority.
Dump emits current definitions post-data followed by local dependency metadata.
Restore admits only qualified positive trigger dependency ALTERs, never arbitrary
rename/drop utilities.

## Procedural exception boundaries

`plpgsql_checkpoint.go` binds unnamed owner marks to the exact admitted statement,
capturing pending stage rows/deletions and transactional effect roots as well as
the owner savepoint. Row/value payloads remain immutable and shared; mutable maps,
event progress and append prefixes are owned by the checkpoint. Initializers run
before the boundary. Success releases it; failure restores effects before matching
and runs the handler outside the failed mark. Provider callbacks run unlocked,
cleanup ignores cancellation, and failed cleanup prevents further handler execution
while preserving the originating diagnostic and secondary failure.
The statement walk is `proc.PLRun` ([proc README](proc/README.md)); `plpgsql_host.go`
adapts it per invocation (`plHost`, allocated once with its run) and keeps
expression execution, conversion, checkpoints, cursors and RAISE/ASSERT here.
FOREACH supplies already-evaluated partial destinations through that host:
ordinary target binding/conversion reconstructs one detached ancestor and
publishes immediately, so subsequent list destinations see prior writes.
Cursor-FOR re-enters `PLRun.Block`, publishes FOUND and retires its owned portal
before propagating outward flow, restoring initially NULL cursor variables.
PL diagnostic signatures belong to the existing per-session routine-analysis
cache, seeded by CREATE validation or that session's first admitted invocation.
They use exact-signature visibility over the current definition overlay and
canonical diagnostic argument names. Path changes do not relabel a retained
signature; replacement immutable procedure carriers invalidate it. Invocation
scratch balances the selected signature across nested calls and failures, without
persisting session presentation in catalog definitions or checked PL programs.
RAISE and ASSERT reuse reached-only expression execution and external type output.
RAISE evaluates arguments and options in written order before buffered notice
filtering; option expressions and NULL checks precede duplicate detection.
Constructed errors carry their structured fields and statement action without an
expression frame. ASSERT reads the effective session setting before binding its
condition, and a true condition skips message binding. Neither message callbacks
nor invocation values are retained in immutable routine programs.
Successful nested messages retain the enclosing PL action and non-inline SQL
statement frames as well. Expression and SQL-command owners finalize only their
new buffered notice range after cleanup, using the caller identity captured
before lexical scratch activation. Query-loop producer execute/fetch/close ranges
exclude body execution, so a body message does not gain a spurious FOR frame.
No callback stack or rendered context is allocated when a range is empty; error
unwinding and later adapter callback delivery remain separate.
Static query loops (`plQueryRows`, reused per nesting depth) borrow the
[`exec`](exec/README.md) nested producer and CTE row owner under a live lexical
activation that spans the body. DML and modifying CTEs complete eagerly and
settle command effects before body execution, with no second result
materialization. Its `Close` closes the producers, formats their failures and
then ends the activation; open failures are already formatted.

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
shared [`access.RoleStore`](access/README.md) (principal domain, ACL authority,
committed roles) and database registry with a
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
  Admitted RC mutation targets retain immutable version/end roots with the pair,
  using inline single-target storage and deduplicated additional targets only.
  Eligibility comes from the actual snapshot-entry command AST; modifying CTEs
  additionally retain their registered target manifests, not an inferred caller.
  Source admission consumes those roots; it neither refreshes the frontier nor
  loses a candidate when a peer replaces its head before delayed capture.
  Latest-row arbitration remains responsible for the current committed successor.
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

- Ordinary modifying utilities reject read-only execution before object
  resolution, authorization or relation locks; Prepare remains effect-free.
  CTAS/SELECT INTO and CREATE MATERIALIZED VIEW retain source analysis, source
  locking and snapshot admission before their read-only execution check. WITH
  NO DATA resolves source names and output types but does not demand the skipped
  SELECT executor's privileges.
- `session.TransactionalSchemaUtility` selects the table, schema, rule, trigger,
  view and materialized-view DDL (including ordinary/concurrent REFRESH), policy
  definitions, domain/composite creation and alterations, type/domain identity/drop,
  shipped function and aggregate creation, routine identity ALTER/DROP,
  operator CREATE/ALTER/DROP, CREATE/ALTER/DROP COLLATION and CONVERSION,
  CREATE/DROP CAST, generic table/type/domain/database OWNER and REASSIGN/DROP
  OWNED that Describe and Execute run on the existing transaction journal
  ([policy](session/README.md#what-it-owns)). Feature-specific view/materialized OWNER
  is AlterView; collation/conversion OWNER is included. Procedure creation,
  language lifecycle and concurrent index variants keep their independent restrictions.
  Definition mutation admission captures original identities, waits outside
  `Engine.mu`, then builds at most one final stage from current authority when
  needed. ALTER retains its original target independently of DROP roots, even for
  no-op actions; neither final replacements nor removals may expand that set.
  A renamed or relocated original cannot redirect an unqualified ALTER to a
  search-path fallback. Native returns ordinary `ErrCatalogReplaced`, without
  SQLSTATE, where PostgreSQL may alter the fallback after the wait. This fence
  aborts the explicit transaction and clears its savepoints: fully roll back,
  then start a fresh transaction on the same owner for qualified recovery.
  Conditional DROP cannot expand its captured targets. Provisional notices are
  discarded without losing earlier message notices. This is bounded admission
  recapture, not command retry.
- Ordinary non-concurrent CREATE INDEX (including UNIQUE and shipped methods)
  is admitted separately at Execute. It uses the same private catalog/storage,
  journal, Finish and combined publication path as table/schema DDL. Its canonical
  owner-authorized Share grant is retained through transaction end; savepoints
  restore only postmark contributions and locks. CONCURRENTLY retains its
  independent boundary.
- Ordinary DROP INDEX and ALTER INDEX rename/SET/RESET use bounded maintenance
  eligibility at Execute and the same CatalogStage/Journal/Finish/combined
  publication path, including retained simple/extended transactions. Exact
  captured grants fence identity through waits; owner-private descriptor,
  dependency and root changes commit with rows or restore at rollback/savepoint.
  Ordinary ALTER holds SUE on the index, permitting parent DML; DROP retains AX
  on parent and index. Partition DROP/ATTACH rules remain unchanged. Describe
  stays syntax-only and held Execute refreshes authority. REINDEX keeps its
  writable-explicit refusal; no blanket utility exemption is introduced.
- CREATE/ALTER/DROP POLICY, including rename, use `view.PlanPolicy` and the shared
  CatalogStage/Journal/Finish/combined publication path in autocommit, explicit
  SQL/Go transactions and retained messages. Definitions, table-local identities,
  role bindings and dependencies resolve against the owner's private catalog;
  savepoints and rollback restore them with rows. AccessExclusive on the target
  is retained through transaction end, so fresh peer table reads wait for release.
  Subsequent direct and held executions select existing RLS programs from current
  private definitions and authority; already-returned results stay immutable.

Rule definitions and original/action rows share the transaction journal and
combined publication. CREATE/REPLACE, RENAME and DROP preserve exact rule identity
and dependency authority across private changes, savepoints and held execution.
Event AX and action target/source locks retain their canonical transaction
lifetime; peer DML waits for publication or rollback. Stored recipes and prepared
consumers select the owner's current private definition, never a cached authority.

Ownership transfer stages resolve earlier successful private definitions and preserve exact
object identities, associations and storage. Relation OWNER refreshes authority
after target admission and checks the locked ref; type/bulk operations retain
their captured complete root union rather than retargeting after a wait. Stage
capture and Finish check the consulted private catalog/role/database/provider
predecessors before effects survive. REASSIGN/DROP OWNED use the existing closure,
ACL/default/policy and grantor-support algorithms, with no intermediate global
publication or rollback-by-undo. Database-owner changes use the shared projection
publication path; bulk relation/type/schema cleanup remains local to the current database.
DROP OWNED policy deletion also contributes the actual surviving containing table
to the same canonical AccessExclusive lock set before staging. The complete source
union must remove every target of at least one policy; partial target rewrites and
ACL-only cleanup do not add this lock. These tables are not ownership roots and do
not pull in indexes or owned sequences. Existing tickets retain AX until rollback
to the acquiring mark or transaction end, so peer row reads wait for release;
nonconflicting catalog reads still see the committed definitions.

DROP OWNED's nonnil cleanup role candidate is validated against the session's
current private predecessor and attached to `Journal.Roles`; the first actual role
write captures committed `RolesBase` for its exact delta. Later private candidates
chain against their current private predecessor. Read/publication refresh merges
disjoint committed changes, but rejects changed touched role/name descriptors or
membership predecessors (Role/Member/Grantor and option bits). A successful refresh
clones the journal and advances both base and candidate without changing saved
roots or marks. A nil candidate neither clears pending roles nor creates a
role-write fence.
Savepoints restore these paired private roots; combined commit publishes catalog,
defaults, storage and any role changes together. Completed metadata-only ownership
does not retain execution authorization as an outer-commit role fence.

View and materialized definitions resolve against the owner's private catalog,
including private source relations and providers. Materialized producers consume
captured source roots plus earlier own writes once; detached result rows and the
producer transaction enter the same statement journal as the definition. NO DATA
does not demand a producer. Stored readers recapture private definitions,
attribute layouts, authorization and population state; retained results stay
immutable. Empty catalog-provider rows remain an owned empty anonymous source,
not a missing physical source. Finish, savepoints and combined commit retain or
discard these paired catalog/data roots through the existing publication path.
Demanded query-derived producers (including empty results) and materialized
replacements retain their committed role predecessor as a journal read fence
until final publication. This is separate from `RolesBase` for private role
writes and from execution authorization, which is checked through Finish.
Creation WITH NO DATA has no demanded producer; REFRESH WITH NO DATA still has
a pending replacement. Completed metadata-only index maintenance owns neither
producer state and does not retain this role-read fence. Savepoints restore
producer fences with the same journal as their rows and storage predecessors.

Refresh replacements retain the first committed predecessor and only the latest
private row/index candidates. Savepoints restore those roots and population with
the owner's source writes; combined publication installs them once. Refresh takes
AccessExclusive (ordinary) or Exclusive (concurrent) on the existing transaction
ticket through transaction end. A queued refresh keeps its admitted source
snapshot rather than silently recapturing after the wait. Only uninterrupted
concurrent replacements preserve the concurrent-reader lineage.

Type/domain candidates use the same private catalog, storage and index roots in
explicit transactions and retained messages. Historical domain validation scans
selected committed heads plus own writes, excluding replaced/deleted predecessors.
Composite evolution transforms heap layouts and own rows together; logical-key
validation includes own rows at the ALTER statement, with produced index outputs
retained rather than replayed at Finish. Savepoints restore these paired roots.
Type metadata edits retain exact source TypeExclusive authority. Composite rename
and attribute evolution also own the real backing relation AX; attribute propagation
then locks its typed-table closure in deterministic order. Schema moves do not add
backing AX. Grants recapture exact private providers and authorization, never a
same-named replacement or a fabricated scalar relation.

- `session.ExplicitUtility` admits the shipped COMMENT kinds (TABLE, COLUMN,
  FOREIGN TABLE, SCHEMA, ROLE and SEQUENCE) in SQL BEGIN and Go BeginTx.
  Describe remains effect-free; Execute preserves read-only and aborted-state
  precedence and refreshes authorization for held statements.

Object comments use `CatalogStage` and the existing private journal; shared role
comments use its immutable role root. Owners see private text, while peers,
including other databases for role comments, see committed text until combined
commit. Rollback and savepoints restore the same roots. Relation COMMENT keeps
its SUE ticket contribution until transaction end or rollback to an earlier
savepoint; RELEASE retains it. Waits validate the captured object identity,
never a same-name replacement.

Internal resolution pairs the owner's private catalog and role authority.
Catalog inquiry rows capture their statement's metadata values; retained results
remain immutable after later changes or rollback. Those row snapshots are not
permission to reuse stale internal identity or authorization.

Relation/schema/type/routine/operator candidates resolve against current committed
state plus the owner's private catalog and storage in explicit, autocommit and
retained-message transactions. `publishCatalogOperation` claims only changed
namespace, relation, type, procedure and operator destination names through the
existing lock ticket; overloaded names include their full input signature.
It retains the normal authority/provider/touched-predecessor preflight. Namespace
existence uses real AS resources and DROP uses AX; rename/owner changes do not
serialize all namespace members. Ordinary table DROP acquires AX on every
captured root before building its one dependency stage from grant-time
authority. Waiting never adopts a same-named replacement.
Routine DROP likewise holds captured procedure roots AX before dependency
discovery. Checked standard-body/default/aggregate/operator providers hold real
procedure AS through transaction end; grant-time identity checks never retarget
a dropped provider. Type and definition-source reads join ordinary stage/journal
fences. Quoted bodies do not acquire hard edges by scanning their source.
Schema DROP carries an ordered namespace/name manifest, including conditional
absences, across admission. Current bindings and ownership are validated in the
same Engine.mu section that constructs its identity-bound stage; any changed
binding fences the whole statement instead of adding or retargeting a victim.
Conditional skip re-resolves the original creation namespace with current
USAGE/CREATE and source position; a changed destination cannot authorize a skip.
Shared claims preserve existing standalone index generated-name/conditional
conflict recovery, not retries of selected table/schema commands.

Descriptors, rowtypes, indexes, constraints, owned definitions and rows publish
through the same journal/Finish/combined commit path. Savepoints restore its
roots and lock checkpoint; no DDL undo journal or global catalog epoch is added.
Native replacement fences recovery before savepoint lookup or participant
callbacks; disposed old marks cannot resurrect replaced definitions.
Query-derived creation claims its destination before producer evaluation.

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
- Provenance is same-pointer authority (`&e.catalog.Owner`), never equal
  names, OIDs or epochs.

## Catalog writes and built-in seed

- `catalog.RevisionFrontier` is pure and saturating and never reserves
  the global allocator; publication commits its local frontier only at
  successful installation under `Engine.mu`.
- Persistent replacement reserves OIDs with an operation-local base/next cursor
  through the same `catalog.Store.AllocateOIDFromLocked` checks as ordinary
  issuance. All preservation OID users share it. Only final catalog installation
  commits its frontier; no-fit, cancellation and later validation failures leave
  live capacity unchanged. Unlocked repair and final installation fence the
  live OID frontier, including aborted ordinary allocations without a new root.
  Failed candidates never reset another operation's consumed identities.
- `CatalogStage` embeds `catalog.Candidate`, which owns the catalog-only
  publication fences, the apply step, the history markers and the creation
  ACLs. Publication's value sub-steps belong to their packages: row shapes,
  candidate relations, birth stamping and replacement capacity to
  [`storage`](storage/README.md), index captures and candidate fences to
  [`index`](index/README.md), statement catalog authority and journal shape
  recording to [`session`](session/README.md). The root keeps the skeleton:
  the `publishCatalog` lock and retry loop, `authorizationLocked`, the
  session-mutex revalidation, XID assignment and the install block,
  `replaceCatalog`, `stageCatalogOverlay`, the fence order, DDL
  authorization, lifecycle, rowtype synchronization, the extension
  materialized-view fence and the post-apply validators. Recipe rewrites
  retain the original routine authority, never recovering it from an OID.
  Effect-free dependency staging does not require an admitted transaction:
  definition-retirement locks belong only to an execution scope's real ticket.
  Direct validated publication still enforces captured identity, epoch and
  incoming-dependency fences without manufacturing a transaction for preparation.
- Every engine seeds the same built-in catalog in one bulk load (`newEngine`'s
  `BeginBulkLoad` … `FinishSeedLocked`) through the
  [`catalog`](catalog/README.md) seed steps. The root keeps their order, the
  capacity, the process's `catalog.SeedShare` (`builtin_catalog.go`), the
  builtin domain checks, the catalog source descriptors and the database
  descriptor; each engine validates and binds only its option-dependent
  parts. Check `make bench` allocations when adding built-ins.

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

PL cursor execution uses the physical-session cursor registry and the ordinary
query binder/capture/motion owners. Lower install/fetch/close primitives, including
promoted holdable movement, do not complete the containing command; SQL cursor
wrappers retain their own transaction gates and completion flags. PL OPEN captures
a producer and immutable typed inputs inside balanced routine scratch without
eager result materialization.

Bound actuals form one ordinary uncoerced declaration-ordered projection: bind
all targets before any evaluation, consume at most its first row through the
shared producer, publish FOUND from that successful query's row presence, then
convert/assign formal values. A formal conversion error retains the new FOUND;
argument-query binding or evaluation failure leaves its previous value unchanged.
Empty projection NULL-fills formals and sets FOUND false; a row containing NULL
still sets it true. Ordinary PL scalar expressions check a second row and reject
multiple rows; SQL scalar routines retain their separate one-column first-row
demand. Original actual spans
map errors into the whole comma-joined argument query, with formal aliases on every
item when any argument is named. Dynamic USING remains individually written-order.

An unnamed OPEN carries a nil requested name through nested actual/command/USING
evaluation and query preparation. Only the shared installer allocates its unique
name after callbacks and publication preflight, then returns the installed identity
for successful variable publication. Explicit names retain the early collision
check; installation also rejects collisions without replacing a live portal.
Neither path mutates retained syntax or reserves a name before evaluation.
An initially NULL cursor receives its generated name only after installation.
That implicit assignment checks the declaration's CONSTANT metadata before
publishing the typed name; earlier command, USING and query failures keep their
precedence. A non-NULL constant cursor name requires no assignment and remains
valid. Constant declaration initialization retains its ordinary conversion rules.


Ordinary portals outlive routine frames and close at transaction/session end.
Cursor FOR retains its opened portal identity and descriptor independently of
subsequent cursor-variable assignments. It pins that portal against user CLOSE
(including copied names), unpins and closes its exact portal on every exit.
Explicit body FETCH/MOVE still resolve their supplied names and share position.
Terminal transaction/session cleanup can forcibly retire protected portals.
FETCH builds descriptor-shaped NULL rows for empty RECORD targets and publishes
converted scalar targets atomically. FOUND remains invocation-private.
FETCH/MOVE resolve and validate a physical-session portal before binding or
evaluating their count. The retained identity survives count evaluation and is
revalidated by the same motion owner, never redirected to a same-name replacement.
Missing portals therefore beat NULL/error/volatile counts without their effects.
Omitted SCROLL uses `exec.SupportsBackwardCursor` over the actual producer and
captured source graph, independently of explicit options and rowmark restrictions.
This is not PostgreSQL cost-planner replication; portable backward demand should
request SCROLL explicitly.

Dynamic USING binds a closed positional frame through ordinary analysis, so a
missing placeholder retains its SQL position and diagnostic rather than becoming
a public driver-arity error. Target conversion carries the current admitted
routine authority explicitly, including fresh scalar/composite domain checks.
PL expression diagnostics retain preparation versus simple-scalar execution
provenance; complex query expressions retain their query traceback. A coercion's
preflight computation of imported parameters records its actual execution origin,
while SQL constants and routine admission retain their planning origin. The current
query's simple/SPI route applies in both preflight and the executor, without a second
expression evaluator or an error-code heuristic.

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

Trigger payload/frame/runtime/remap/stage units share
`RUN='^TestTriggerCore(Catalog|Frame|Return|Runtime|Persistence|AtomicStage|PartitionPath|WhenProvider)$'`.
Trigger lifecycle identity, clone, source-fence, dependency and remap units use
`RUN='^TestTriggerDDL'` and construct metadata with the same trigger fixture.

Root tests are package-level units of these contracts; public SQL behavior is
tested only in `reference/` ([docs/TESTING.md](../../docs/TESTING.md)). SHOW ALL
description rules are in the [`settings` README](settings/README.md#testing).
Add `RACE=1` for lifecycle, cancellation and contention selections.
PL cursor compiler/provider/ownership units: `RUN='^TestPLpgSQLCursor'`.
Internal producer lifetime/cancellation units use `RUN='^TestPLpgSQLQueryLoop(Lifetime|Cancellation)$'`.
Lo provider units use `RUN='^TestLo(Provider|OwnerMetadata|ManifestAdmission)'`.
They stage concrete domain/companion recipes through the shared extension host,
validate immutable manifests and symbolic ownership, and exercise genuine
collision/allocation and destination-role replacement fences without public SQL.

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

Table-metadata producer units reuse the engine-seeded catalog and authorization
fixture through the real producer registry. They assert source-owned domain
cells, retained captures, typed empty results and rejection of malformed or
foreign capture authority: `RUN='^TestInfoTables'`. Public SQL and native-control
witnesses belong to the corresponding `reference/catalogs` family.
Routine-metadata units use the same real capture fixture and staged extension
constructors for declaration completeness, independent source bags, redaction,
empty/canceled results and generation fences: `RUN='^TestInfoRoutines'`.

### Advisory timer scheduling and shared result waits

- Timer cases of `TestAdvisorySessionQueueTimersDeadlock` run under
  `testing/synctest` with real deadlines, `context.AfterFunc` and manager lock
  timers, never manual expiration. Queue the exact request before advancing
  virtual time, inspect state only after `synctest.Wait`, and join the delayed
  consumer on every path. Timeouts keep location-free 55P03/57014 diagnostics
  and leave no grant, pending request or journal entry.
- Binary COPY timeout leaves use `testing/synctest` with actual production timers
  and blocked payload/post-trailer EOF reader callbacks. Settle with
  `synctest.Wait` and assert the operation is unexpired and unfinished before
  interruption; release and join callbacks/operations on every path before
  session disposal.
- `deadlockResult` waits for the authoritative `Done` (never `Terminal` alone),
  reads `Err` only after `Done`, and uses one stopped one-second guard.

```sh
make check RUN='^TestPreparedRebind'                  # prepared analysis identity
make check RUN='^TestRangeType(Parser|Providers|Bounds|Multirange|Atomicity|IdentityRemap|DependencyClosure|Adapter|Constructors|ExactEndpoints)$'
make check RUN='^TestDatabaseCreate'                   # registry, template, publication, target authority
make check RUN='^TestSetting'                         # settings registry
make check RUN='^Test(CatalogSource|InfoUserMappingsProducer|PGSettingsCatalogACL)'  # producers; one catalog: ^TestPG<Name>
make check RUN='^Test(AlterDropColumnRevisionFrontier|CatalogConcurrentReplacement|AccessMethod|Builtin)'  # catalog writes, seed
make check RUN='^Test(CatalogRelations|NamespaceIdentity|ObjectAddress)'  # catalog inquiry and identity
make check RUN='^TestNotification'                     # immutable session/cluster queues and publication
make check RUN='^TestIsnProvider'                      # installed identifier authority and import
make check RUN='^Test(Advisory|BlockingPids|DeadlockResultScheduling|RelationDeadlock|RelationLockWaits|PGLocks)'  # advisory, waits, deadlocks
make check RUN='^Test(UpdateCore|TransactionIdentity|CommitTime)'  # MultiXact, transaction identity
make check RUN='^TestTransactionalSchema(Admission|PrivateProviders|Publication|DropManifest)$'  # utility policy and private catalog stages
make check RUN='^(TestAlterDefaultGrantTimeDescriptor|TestNotNullAXGrantTimeDescriptorAndRetargetFence|TestAlterRenameLockGrantTime|TestQueryJoinsSecondLeafGrantRebind)$'  # captured identity after actual AX waits
make check RUN='^(TestAlterSchemaPublicationFences|TestAlterSchemaDependentGrantTimeName|TestCommentLocks)$'  # late destination collisions and queued comment identity
make check RUN='^(TestErrorFieldsExecutePublicationConflict|TestAlterRenamePublication|TestBtreeIndexConcurrentOrdinaryBuilds|TestRoleNoticesConflictSkipRechecksCreateAuthorization|TestSavepointsAuthorityAndCompletion)$'  # shared claim compatibility, diagnostics and completion
make check RUN='^(TestQueryTableCreateSnapshotAdmission|TestSafeSnapshotAbandon)$'  # failed acquisition discards snapshot ownership
make check RUN='^TestSnapshot'                        # binding, retained command/transaction pairs and physical frontier filters
make check RUN='^Test(Serial|SourceProtocol|ViewLiteralProtocol)'  # sequences, sources, stored capture
make check RUN='^Test([A-Za-z]+RoutineOriginalAuthority|SQLRoutine|UnavailableFunctionIdentityBoundary)'  # routines
make check RUN='^Test(Extension|PGDepend|CatalogDrop|DropOwned)'  # extensions, drop fences
make check RUN='^Test(Vacuum|Protocol|SessionTimeout|StatementTimeoutAdmission)'  # VACUUM, protocol, timeouts
```
