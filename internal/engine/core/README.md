# internal/engine/core

Core is the engine's runtime: `Engine` state and `Engine.Mu`, the catalog store
(`catalog.Store`, see [its README](../catalog/README.md)), statement scopes and
admission, lock waits, `CatalogStage`, publication and replacement, the
transaction lifecycle, sessions and TEMP, the hosts and binder construction,
protocol, prepared SQL, cursors, the bound DML and query path and the
procedural runtime ([`docs/ENGINE-SPLIT.md`](../../../docs/ENGINE-SPLIT.md) §3,
L15). It imports the layered subpackages and never the root or a family; the
family statement executors (the `*cmds` packages) call its exported API.

EXPLAIN shares the description-only binder, source admission and rule/view
validation with Prepare. Its command boundary returns before execution fold
planning, source capture, provider callbacks, DML startup, modifying CTE
activation and tuple locking. Bound graphs and expression presentation remain
activation-local; prepared descriptions retain only ordinary immutable
dependency/parameter captures and the wrapper's single `QUERY PLAN` TEXT schema.
Their fold owner is inert: EXPLAIN never grants child fold-publication rights,
even during Describe or protocol Parse/Bind, and retains no accepted generation.
The renderer describes actual logical stages and rewritten commands, with
detached maintenance statistics or labelled deterministic heuristics. It never
counts child rows or evaluates expressions to produce a plan. Results own their
text lines and complete with the non-counted EXPLAIN command tag.
Window clauses are resolved through their owning stage's canonical specification
indices, and SRF outputs through that stage's fixed call/scalar slots. Verbose
outputs omit private grouping identities and reserved, not-yet-produced SRF
slots; neither is an executable SQL output expression.
UPDATE, DELETE and MERGE RETURNING expressions and their correlated subplans
retain the combined target/source candidate frame. INSERT RETURNING instead
uses the produced target row with its accepted target alias, independently of
input-query labels. OLD/NEW presentation does not replace source ownership.

ANALYZE column facts resolve through the statement/transaction Journal overlay
of `storage.Store.RelationMetadata`, the single canonical ANALYZE metadata root
for heap and partition relations alike. Statistics-only commits validate epoch,
committed descriptor and optional real-heap identity, then merge selected
compatible attributes at the no-failure boundary without row-root replacement
or DML counters. Clone/savepoint/prepared ownership
uses the ordinary Journal lifecycle. Relation scan estimates remain independent
nontransactional maintenance metadata. EXPLAIN captures immutable compatible
typed facts by stable attribute and SQL occurrence, compares detached values
without evaluating expressions, and preserves fallback estimates for unsupported
predicates. Equality/NULL inputs are exact; bounded distribution/range and
independence assumptions are logical approximations, not physical plan costs.
Partition parents never own entries in the physical `Storage.Relations` map.
Their statistics, row estimates and analyze counters live in the same metadata
root as other relations. New private-object metadata remains on its creation
journal until commit; committed-object maintenance remains nontransactional.
DDL prunes dropped objects and incompatible column facts. Native Reset/Restore
discards imported/persistent/reminted planner metadata, but preserves compatible
facts and counters for genuinely retained destination TEMP identities through
the existing TEMP-retention authority, never address coincidence.
Retained TEMP statistics definitions advance their checked relation-reference
epoch with the replacement, without applying imported identity mappings.
Incompatible non-retained dependencies reject replacement before installation.
Their ndistinct payloads reconstruct from checked immutable items when the
candidate's builtin descriptor changes. All fallible construction precedes
installation; retained captures keep their original typed values and backing.
Parent inherited cardinality is retained separately from independently stale
child estimates, so ONLY parent refresh works. EXPLAIN requires a current
hierarchy before using it. A successful local-only refresh clears obsolete
inherited population authority and column flavors; Journal tombstones preserve
that structural invalidation transactionally.

A private TRUNCATE creates an unscanned successor. A subsequent ANALYZE records
its actual scan in that private heap as well as nontransactional committed
maintenance. Commit preserves this later scan after the truncate reset; another
TRUNCATE starts a new unscanned successor instead. The private maintenance
witness survives savepoint rollback independently of transactional column facts,
and compatible schema successors retain it. No second cardinality owner or
transaction-private copy of the committed metadata map is introduced.


Routine invocation activates concrete checked inputs/results without changing
catalog declarations. Session-private SQL analysis and PL program retention each
keep only the last specialization, keyed by complete usages, declaration/name
and input collation. Nested invocations retain their own frames and immutable
program projections. Canonical declaration specialization projects each PL OUT
formal from its invocation tuple member. Compilation owns a private definition
and formal slice even when usages are unchanged; only genuinely unshaped RECORD
OUT transport is deferred, never an already-shaped member. RECORD inputs unwrap
actual tuple descriptors before PL compilation, so retained `%TYPE` copies and
parameter frames agree without changing caller transport metadata. Scalar
RECORD packing and result
coercions are planned before producers open. Terminal validation converts only
the admitted catalog's unshaped RECORD declaration to a private executable
usage; it does not grant authority to foreign same-OID descriptors. First-row
demand retains every projected column for composite SQL results and PL cursor
actuals, while scalar SQL results retain their one-column width check. All
projected expressions are evaluated before ordinal result/argument conversion.
An admitted non-VOID SQL invocation with no parsed commands still requires a
final result: it fails before specialization or successful analysis publication.
Empty VOID invocations remain valid; rejected analysis never replaces another
routine's accepted command backing.

Routine command policy is invocation-local and carries no cached actor.
Reached SQL and PL commands classify their analyzed executable binding before
opening producers, including writable CTEs and stored-source row marks.
Stable/immutable bodies borrow the calling query's coherent roots and private
write frontier; volatile commands merge reached staged changes and acquire a
fresh read-committed frontier. Transaction-stable isolation retains its roots.
Each command owns and releases its local visibility view without completing
the root operation or mutating the caller's retained backing.

Ordinary SQL/PL calls enter routine environments after reached caller admission
and strict suppression. Admitted DML and event-trigger entries use the same
balanced environment and lexical volatility/snapshot policy, but retain
installation-time EXECUTE admission without ordinary-call rechecks or strict
NULL suppression. Each firing uses current function owner/configuration/
volatility; relation and WHEN roots remain command-owned. A definer supersedes
an enclosing RI actor only for its body, without weakening genuine restrictions
or later RI relation-write authority. Trigger frame/depth/diagnostic and
SAVE/policy unwind covers success, error, partial entry and cancellation;
event start/end/drop/rewrite retain their atomic utility owner.
Trigger-bearing admitted mutation targets also retain dynamic source roots at
the calling command's initial data-view capture: lazy trigger-body reads are
not outer expression dependencies. STABLE/IMMUTABLE trigger commands borrow
those fixed roots and frontier without recapturing after firing. Trigger-free
DML does not retain an additional dynamic-root map.
`enterRoutineEnvironment` pushes a distinct
operation-local definer actor and installs ordered configuration through the
session's SET/LOCAL/SAVE journal. Invokers inherit the enclosing actor;
session/authenticated identity remains unchanged. Definers do not create
maintenance restrictions, detached settings or an implicit safe lookup path.
An enclosing maintenance restriction still applies. Entry failure and every
return/error/cancel unwind partial settings and actor state, preserving the
primary diagnostic. Identity, codecs, lexical namespace inquiry, row-security
and operation policy refresh after effective changes and restoration; demanded
catalog/visibility snapshots and output spellings remain query-owned.
Entry recaptures presentation after the complete configuration list, independently
of whether a routine command is already active.
`is_superuser` reads the effective current role's own attribute under that same
authority for current_setting, SHOW and pg_settings, never inherited membership.
Nested commands resolve the first temporary namespace from their shared
statement candidate before the transaction journal or session publishes it.
Private-catalog namespace scanning is isolated from staged/active lookup so
unchanged prepared sequence environment validation remains allocation-free.
`DDLHost.ValidateRoutineSetting` validates without installation. FROM CURRENT
captures at execution, in the existing ordered attribute fold. External Normalize
suspends that fold on its stack, releases Engine.Mu and the active statement's
replacement reservation, and executes exactly once without store/session locks.
Continuation readmits the live operation, settings attachment, parameter/read
authority and original catalog/role/session authority before using the value.
Any stale authority rejects the command; definition-lock admission cannot
reconstruct a callback-bearing candidate and repeat external work. Registry
definitions remain immutable engine-owned inputs. Declaration array admission
runs current-transaction check hooks
against the DDL snapshot without assigning modes: isolation admits idempotent
values, read-only admits changes except read-only to read-write after a snapshot,
and deferrable refuses even idempotent values after a snapshot. CREATE/REPLACE
then preflight validator SAVE admission; ALTER does not invoke that validator.
RESET and SAVE refusals precede check hooks and mode mutation. The existing
private replay directive suppresses only language-validator admission, not
declaration array checks. Owner admission serializes nonmutating mode inspection;
the declaration host never acquires the transaction mutex under Engine.Mu.
Identity mutation restrictions precede setting-expression registry lookup,
retaining distinct definer and enclosing maintenance diagnostics. Ordinary
setting expressions and SET/RESET utilities share the live installation path.
Publication lock order is Engine.Mu -> Settings.Mu -> Session.Mu.
An actual setting normalizer callback also releases the statement's replacement
reservation through the shared external-work handoff. Parameter authority is
rechecked after the callback; installation readmits the live operation and
settings attachment under the publication locks. Normalization without a callback
retains its existing reservation. Providers/external work never run in that window.

SET DEFAULT referential actions retain a statement-owned `exec.RoutineProgram`
while preparing the child recipe, including original calls erased by immutable
folding. Each call records the actual child-owner actor and defining manifest.
The existing post-wait and final-publication fences readmit those originals
against current authority without evaluating defaults or CHECK truth again.
Action diagnostics retain the generated RI statement after caller restoration;
neither initiating-user EXECUTE nor a cooked value substitutes for owner admission.

## Hooks

`NewEngine` (`engine.go`) seeds one engine's state: the stores, settings,
visibility, locks, roles, the catalog providers (feature-owned ones first,
then the builtin ones including `RelationProvider`), the bundled extension
registry and the builtin catalog seed. The composition then sets two fixed
fields, shared by every database target:
- `Engine.Statements` (`Statements`, `statements.go`), the family statement
  dispatch. `executeStatement` keeps the core cases (LISTEN/NOTIFY, TRUNCATE,
  DISCARD, PREPARE/DEALLOCATE, FETCH/CLOSE, DO, SET/SHOW/RESET, setting calls)
  and the DECLARE CURSOR checks, and hands every other statement to
  `Statements.Execute` with nothing held. After the authorization capture,
  `Statements.ExecuteLocked` takes the locked family handlers (ALTER TABLE and
  its forms, REFRESH, DROP TABLE, CREATE TABLE); it is a lock-domain
  continuation, entered with `Engine.Mu` held, that releases it on every
  handled return.
- `Engine.Factory` (`EngineFactory`), the composed constructor that builds the
  private `template1` seed and the Restore stage.

Event-trigger definition DDL also goes through `Statements.Execute` to the
schema family. Its successful completion uses `CompleteUtility`, preserving any
pending statement journal; core owns callback execution, not schema execution.

Standalone CALL follows analyzed command admission, fold publication and the
shared SQL/PL invocation owner, not synthetic SELECT or a second connection.
Describe exposes declaration-owned OUT/INOUT labels with checked invocation
member usages; execution exports one detached tuple through `anonymousResult`,
or no descriptor without outputs. CALL's
completion count is zero regardless of body effects. Quoted SQL-body CALL with
outputs fails before producers; no-output calls use the ordinary command runner.
Unquoted SQL bodies reject CALL during definition validation.

PL transaction control rotates the existing root owner through the same checked
stage merge and end/publication path as SQL controls. A successor is implicit,
not a new root statement; the statement budget and lexical PL frame remain.
Saved routine scratch is qualified by TxRef and rebased after rotation rather
than restoring obsolete snapshots, writes, catalog authority or completion.
Nested relation admission selects a missing stable snapshot through the common
isolation phase before analysis and source capture. Successor snapshots remain
lazy: snapshot-free utilities preserve the immediate characteristics window,
while read-committed commands keep their data-admission frontier.
Static PL SET TRANSACTION uses the common utility description and execution
dispatch; characteristics have no result descriptor or lexical parameters and
retain the existing owner mode checks and utility completion.
Invocation atomicity is captured before command mutation, separately from
protocol/message retention, and scoped through direct CALL/DO, functions,
dynamic commands, routine attributes and active exception marks. SQL-language
body execution adds an atomic barrier for nested CALL/DO, even when its outer
procedure invocation is eligible; every exit restores the caller's policy.
Direct PL/pgSQL CALL/DO nesting preserves eligibility. Before either ending
command, each active loop source is strategy-checked immediately before
holding it; the first strategy or materialization error stops promotion before
publication. Query-FOR producers hold remaining rows; scrollable pinned bound
cursors rewind to hold the entire result under their existing cursor semantics.
Ordinary open cursors close; held loop storage belongs to loop cleanup.


Text-search conversion captures a real configuration and its distinct immutable
dictionary runtimes from one private catalog generation under `Engine.Mu`, then
releases it before tokenization/lexization. Mapping changes publish through
ordinary schema stages and definition locks. Generic folds retain immutable
constant vectors; runtime arguments consume current mappings without local caches.
The prepared identifier owner keeps original REGCONFIG occurrence provenance
separate from current operand observations. Operand-only reanalysis carries an
attempt-private eligible input projection through the existing binder transport;
current dependencies are collected by original OID, never by its old spelling.
Namespace generation or resolution-environment changes instead request ordinary
source input. Session, private-journal and DISCARD fences retire projections.
Signature, native replacement, authority and context fences precede
complete analysis publication. This identifier lifetime does not retain runtime
conversion results or weaken independent fold ownership.
`DDLHost.SearchConfigurationNamespaces` resolves every existing configuration
DDL source (targets, COPY, PARSER, mapping lists and both replacement operands)
with current caller authority, including during trusted Restore destination
construction. It delegates to shared namespace resolution with a by-value
authority and a private qualifier copy without expression location; caller
authority and source AST remain unchanged. Generic dictionary, parser and
operator lookup retain their expression positions and trusted definition policy.

Code shared by families with no arrow between them sits here: relation
ownership (`ExecuteRelationOwnership`, `OwnershipStage`, `PublishOwnership`)
and operation role installation (`InstallOperationRolesLocked`). Family code
waits on relation targets and bound view relations through
`StatementScope.AcquireRelationTarget` and `AcquireBoundViewRelationModes`;
their unexported bodies keep the frames that read-only reference fixtures
match.

Resource setting simulation keeps TEMP initialization separate from the
transaction TEMP footprint. Successful opened query/DML/COPY table sources
and accepted write staging call `initializeTemporaryBuffersLocked`
under Engine.Mu, then take Settings.Mu to capture the owned session's typed
count. Foreign namespaces, sequences, CREATE/binding and Prepare do not
initialize it. No new physical buffer/cache allocation is introduced.
Setting publication checks explicit candidates before Touch/install under
the existing Engine.Mu -> Settings.Mu -> Session.Mu window; reset candidates
and journal restoration bypass the sticky-count check.
`TestResourceTemporaryInitializedValidation` checks malformed, range and
normalized-but-different candidates through direct/prepared SET and bound
set_config after initialization. It compares the real Actions, typed/text
overrides, defaults and provider provenance before Finish can restore a failed
statement, then checks current value/source after savepoint recovery.
The public resource Lifecycle definition additionally isolates first use of
empty native SELECT/UPDATE/DELETE/MERGE/COPY TO sources on fresh physical
owners, separately from populated shared PostgreSQL preservation witnesses.

## Installed byte pageinspect runtime

`pageinspect_capture.go` checks current installed identity and superuser
authority after STRICT argument evaluation. BRIN item decoding opens the exact
regclass descriptor with a temporary AccessShare contribution, waits outside
metadata locks, and readmits the same descriptor before capturing per-column
stored types. It never reads table rows or fabricates physical pages. The
execution context decodes caller-owned bytea outside metadata locks through
the SQL-independent native readers in `sqltype`. B-tree and FSM byte decoding
need no relation capture; physical relname page acquisition is not an engine provider
overload and local table contents are never synthesized into raw index pages.

## Installed crosstab runtime

The crosstab statement host validates the captured procedure against current
authority before argument evaluation, including strict-NULL calls. Nested source
SQL borrows the existing SQL-routine activation, caller catalog/visibility and
effective settings, with an isolated parameter frame. The whole SQL string is
prepared; only admitted non-volatile commands execute, and the last result owns
the pivot input. Each executed command has its own SQL-routine activation, so
expected analysis, relation admission and source authority from an earlier
command cannot authorize a later command. Commands share caller visibility,
catalog frontier and effective settings, not query scratch. Every exit closes
cursors and restores activation scratch. Parsing, typed codecs and source
execution run outside `Engine.Mu`.

## Installed amcheck B-tree runtime

The sole `StockAmcheck` installation retains `verify_heapam` at slot zero and
seven scalar B-tree overloads at slots one through seven. Heap verification's
record-set source and B-tree VOID expressions retain distinct binding,
transport, admission and runtime paths.

`connectby_runtime.go` executes raw hierarchy SQL through ordinary nested
parse/bind/execute activation under the caller's current catalog, retained
snapshot frontier, settings and authority. Runtime-named sources borrow the
outer snapshot's retained roots, not its fixed source-membership set. The first
recursive source/RLS capture installs an invocation-local view; later reads
validate and reuse it. Restoration releases that local view without changing
the outer view, including when the caller is DML. Each sibling query is fully
materialized and closed before restoring outer scratch. An explicit frame stack owns depth-first
traversal; only a completely successful traversal publishes rows. SQL-boundary
errors receive internal-query diagnostics; later descriptor, conversion and
cycle errors remain outside that boundary.
Read-only SPI classification rejects utilities before their execution checks;
optimizable statements retain ordinary analysis before the nonvolatile-function
gate. Transaction commands and frontend COPY instead terminate the statement
list with no children, matching SPI's negative return status. Successful
read-only SELECT lists retain last-result semantics.
Setting function calls are ordinary SELECT expressions here too: their canonical
function identities retain live EXECUTE checks and setting effects, while SPI
readonly classification continues to inspect the analyzed SQL command and row
marks rather than prohibiting every volatile expression.
Connectby enables an invocation-private read-only SPI demand. The ordinary
description seam classifies the still-live canonical binding after analysis and
authority checks, omitting analysis-only occurrences. It consumes marks at every
derived/scalar/CTE query level and recursively follows stored-source occurrence
marks. Nested row locking is rejected before tuple-lock execution; unused locking
CTEs remain read-only, and ts_rewrite's separate gate is unchanged.
Stored expansion propagates and merges inherited tuple marks through
`stored.ViewLeafRowMark`, with a path-local recursion guard. Diagnostic marker
presence is distinct from locking strength: a retained stored-query boundary
marked by its caller contributes a preceding reference/COPY marker with no
locking strength, yielding the plain `SELECT` tag while still rejecting SPI
execution. Explicit locking, ordering, bounds, WITH and security-barrier metadata
retain these represented boundaries; inherited locking alone on a plain view
does not. Unrelated later strengths do not replace the first marker's tag.
The canonical bound query walk establishes parent-before-child scope order.
Within each level, `RowMark.Order` preserves the first written locking-clause
target order independently of FROM order and later strength upgrades. Orders
restart at query boundaries and are never globally sorted. Frozen-source
ownership comes from the retained local FROM graph, stopping at derived and
projection boundaries; source metadata is neither reordered nor mutated.

`amcheck_*.go` captures exact target/parent and current routine authority, then
acquires heap followed by index outside metadata locks. A non-index target has
no heap identity and is rejected only after locking the target itself. Method,
foreign-TEMP and validity checks follow both locks. Only queued acquisition
drops the replacement reservation; every readmission retains original identities
and checks current EXECUTE without advancing checkability ahead of lock admission.
Each admitted heap/target open records the existing sticky transaction TEMP
footprint before checkability, including numeric identities and rejected foreign
TEMP attempts. Savepoint rollback does not erase it; EXECUTE denial, strict NULL,
and cancellation before that open do not create it. This PREPARE TRANSACTION
accounting is distinct from physical-session `temp_buffers` initialization.
Immutable baseline and private-output captures feed `index.CheckBtree` without
callbacks under metadata locks. Basic checks use AccessShare and parent checks
Share, released at invocation exit while retaining preexisting transaction grants.
A serial operation-local table-owner frame supplies restricted initial lookup and
detached effective settings across nested SQL/PL activation; every exit restores
caller authority and presentation. Sequence, large-object and pgrowlocks callable
admission/readmission honors this restricted owner ahead of enclosing RI/rule
actors, while ordinary writes retain their existing RI/rule authority.

Native logical checks cover order, parent association, demanded heap membership,
root search and visible uniqueness. They do not claim physical page/downlink,
WAL/LSN or private-overlay ordered-tree verification; see
[`index`](../index/README.md). Installed member identity and arbitrary current ACL
provenance use the shared controls codec. A retained persistent regclass literal
still fails ordinary replacement fencing rather than adopting a restored index.

## Event-trigger command ownership

`session.EventCommand` classifies eligible transactional utilities before target
lookup, independently of completion tags. Core opens a genuine statement-private
journal before `ddl_command_start`; main DDL, consumed logical drops, semantic
rewrites and `ddl_command_end` retain it through ordinary `Finish`. Nested SQL
shares that journal and the write stage, but owns fresh lexical command scratch.
`CompleteUtility` never marks a pending journal as already published.

Each phase captures matching enabled definitions in name order under `Engine.Mu`
and invokes checked current functions unlocked. Prepared handles retain neither
callback lists nor function bodies. Exception checkpoints restore command-local
drop facts alongside the catalog, row and effect roots. Internal replacement
effects do not become user-visible drops.

`PublishAlterMetadata` consumes structurally validated metadata before heap work.
`TransferAlterStage` retains ordinary normalization and admission; one fresh
`FinishAlterValidation` stage demands registered providers after all physical
work, over exact final descriptors without another revision or resurrection.
`BeginAlterPhysical` checks exact descriptor, provider, index-set and private
storage successors before and after the rewrite callback, separately from the
journal's original committed storage and publication fences. Only this ALTER's
admitted touched topology receipts advance to authentic private successors.
Destination-name claims remain pending until Finish; exception rollback restores
their suffix. General authority retains the published role root and installed
session revision. Shared transfer methods live in core; family executors retain
their physical work records and old tuple layouts.
Existing FOREIGN KEY validation captures the stored target/supporting index,
not a new REFERENCES declaration or privilege grant. Admission takes SUE on the
child and RowShare on each deduplicated parent; stronger ADD/shape companions
dominate, including self references. Captured identities and private storage are
rechecked after unlocked waits. Validity-only promotion does not recreate a key
or request ADD-provider SRE/REFERENCES authority. Selected historical scans use
the ordinary RI comparison/disclosure and latest private effective rows.
ADD FK retains its separate name/storage/supporting-key capture and reopens
current REFERENCES authority after waits during ordinary definition cooking.
Stored-key validation's exact descriptor-original fences do not apply to ADD-only
targets; a queued ADD reports current privilege denial rather than an ACL-pointer
replacement conflict.
Pure validity-only metadata carries heap-pointer identity fences, not
tuple-generation/root freezes or new storage shapes. Concurrent enforced DML can
commit before or after promotion without a lost heap/index-root publication.
Shape/ADD/TYPE companions and earlier own transaction shapes retain their strict
fences. `PublishAlterMetadata` carries this explicit per-target distinction into
the existing physical source proof; descriptor, provider and epoch fences remain.
`ActiveScopeOfLease` alone supplies unfinished statement authority; a retained
finished lease remains useful for accounting, not private catalog or role lookup.

`EventCommand` holds lexical tag/drop facts, not publication authority.
`BeginEventCommand`, `RecordEventDrop` and `FinishEventCommand` pair entry with
exact enclosing-command restoration. `ProjectedRoots` and `WriteStage.Replacing`
distinguish complete private roots from appended suffixes; `DiscardRemovedWrites`
uses the same conversion as column projection. `ActivateSQLRoutine` owns fresh
lexical admission and restores the exact outer `MaterializedSlot` once on exit.
Volatile nested commands snapshot complete projected private roots instead of
merging their removed entries with older transaction writes. Thus AFTER TRUNCATE
reads the staged empty target even when earlier commands wrote it in the same
transaction; read-only routine policies retain their calling-query frontier.


## Procedural exception boundaries

`plpgsql_checkpoint.go` binds unnamed owner marks to the exact admitted statement,
capturing pending stage rows/deletions and transactional effect roots as well as
the owner savepoint. Row/value payloads remain immutable and shared; mutable maps,
event progress and append prefixes are owned by the checkpoint. Initializers run
before the boundary. Success releases it; failure restores effects before matching
and runs the handler outside the failed mark. Provider callbacks run unlocked,
cleanup ignores cancellation, and failed cleanup prevents further handler execution
while preserving the originating diagnostic and secondary failure.
`BeginPLException` returns an opaque statement-owned mark; `EndPLException`
consumes it once in LIFO order, releasing success or restoring the failed suffix
before handler execution.
The statement walk is `proc.PLRun` ([proc README](../proc/README.md)); `plpgsql_host.go`
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
Live PL actions and non-inline SQL statement numbers use an operation-private
borrowed chain outside lexical scratch. Invocation hosts own their nodes; each
evaluation/producer restores the suspended action on success, failure and cancel.
Only requested current context renders the chain. Caught context remains the
original handled error. GET projects canonical fields through ordinary target
assignment and reads the admitted declaration's actual RoutineOID.
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
[`exec`](../exec/README.md) nested producer and CTE row owner under a live lexical
activation that spans the body. DML and modifying CTEs complete eagerly and
settle command effects before body execution, with no second result
materialization. Its `Close` closes the producers, formats their failures and
then ends the activation; open failures are already formatted.

Static capture uses the same lexical activation, admission, command completion
and SQL diagnostic formatter. SELECT demands one row, or two for STRICT, through
an activation-local capture mode. Transferred writable CTEs retain actual
first-demand/exhaustion ownership: capture closes the main producer and completes
remaining auxiliaries while its nested command frame is still live, then drains
that frame before FOUND, cardinality or target assignment. Execution failures
remain ordinary catchable SQL errors; only resource-close failures are fatal PL
cleanup errors. Failure paths abort producers before retiring the frame or
restoring activation. Ordinary SQL and query-loop demand retain their separate
completion policies.
Lexical restoration also collects the foreign owner's retained close failure,
including producers already finalized inside scalar subqueries. It combines
that failure with the primary as fatal `PLExceptionCleanupError` before restoring
scratch, preserving both causes and the primary diagnostic without closing a
producer twice or duplicating an already-classified cleanup failure.
Static PL commands use ordinary statement admission and utility effects under a
fresh lexical activation. Commands and PERFORM fully drain projections before
publishing status or diagnosing an unhandled tuple descriptor; all producers
close before scratch restoration. PL CALL keeps its one common selected binding,
validates every coerced output destination/constant before input/default demand,
then consumes the handler's detached tuple. Copyback publishes successful
assignments in formal order through the assignment owner; a later failed value
does not undo earlier locals or alter the failed destination. CALL and utilities
preserve caller FOUND. Exception/database rollback remains with the shared scope.
Processed-row status is frame-private: existing drains count without retaining
discarded rows; DML/capture/cursor publishers use their actual demand counts.
Bare SELECT publishes its primary drained count and FOUND before the missing-
destination error; ancillary rowsets do not contribute to that count. FETCH
publishes count and FOUND together only after successful destination conversion,
detached-batch close and lexical cleanup; failed FETCH preserves prior status.
Failed cursor motion retires its producer through the existing cursor owner;
cleanup failure is fatal and retains both the primary diagnostic and cleanup cause.
CALL preserves caller status; utilities publish their completion count without
changing FOUND. Scalar execution reuses admitted simple-expression provenance;
SQL evaluation publishes its outer row count. Same checked expression-site
reentry uses an operation-private active chain and the SQL fallback, not mutable
program flags or a blanket recursive-call rule.
The command-level SELECT-CTE invocation owns its own captured sources, separate
from each modifier's input. Raw selection and frozen-layout bridging run outside
`Engine.Mu`, before any auxiliary effects, followed by grant/context revalidation.
Every installed CTE state and any `CTEOwnedRows` transfer retains that returned
head. Modifier-only programs keep their source-free authority-header fast path.
Frozen routine admission sends the main command and its actual immutable modifier
bindings through the existing bound-command grant/readmission owner. Only after
real target/source grants does it install exact statement-keyed mutation contexts.
Main SELECT keeps an empty target; main DML and each child keep their own physical
reference, descriptor, storage and view-write state. Bound view/hierarchy targets
use incumbent admission outside `Engine.Mu`, followed by all-context identity and
authorization revalidation before effects. Lexical names never reacquire or
retarget a frozen child, and ordinary command scratch restores the caller.
Target lock requests are separate from returned local data-view roots. Foreign
targets retain real RX grants and current ACL/identity checks, while only
`RelationKind.Stored()` targets enter heap capture, matching source collection.
Their local query inputs still acquire and capture their actual stored roots.
DML installs a command-private capture counter: the second successful RETURNING
projection signals cardinality immediately, before a third candidate. Projection
errors precede that signal; existing checkpoints discard the staged prefix.
Rule rewriting transfers only that policy to its designated RETURNING provider
(or count provider when absent) before preparing command scratch. Unrelated rule
actions, auxiliary mutations and nested commands do not inherit it; ordinary DML
without capture remains uncapped.
Successful commands retain ordinary complete-mutation semantics. Capture closes
all producers and finishes its nested command before restoring activation,
publishes FOUND before cardinality and assignment, and publishes list
destinations sequentially, unlike FETCH.
Static Command and Capture borrow their admitted execution text in the same
operation-local live query chain as dynamic SQL. PERFORM reuses its existing
SELECT-head translation; Capture reuses the parser's INTO-redacted text with
trailing SQL whitespace removed. The query node spans actual execution, producer
draining/close and event/command completion, then retires before late cardinality,
conversion or CALL output copyback. Failed execution retains the node through
fallback cleanup and activation restoration. The outer PL action remains active
through late assignment; ordinary simple expressions do not gain SQL query frames.
Live context renders lazily and remains separate from error-unwind callbacks.
Dynamic PL commands evaluate their command and typed USING values once, then
parse the complete computed input on each reached execution. The ordinary
routine activation/binder executes it without PL lexical substitution. An explicit
activation-local flag closes the USING parameter namespace even when empty; the
shared analysis seam reports undefined parameters before importing values. Fresh
activations reset that flag and restoration preserves the caller's exact demand,
without changing public parameter inference or cursor/static binding. All
producers drain and close before command completion, activation restoration,
STRICT cardinality or target publication. Ordinary EXECUTE publishes the final
command's complete primary SELECT count or genuine DML affected count only after
all commands complete, before INTO descriptor/cardinality/conversion checks, and
never sets FOUND. Its full count is separate from saturated INTO cardinality;
execution or cleanup failure does not publish a successful count.
RETURN QUERY EXECUTE uses the existing SQL-routine set sink, validates exact
tuple structure even for empty producers, appends detached values, and publishes
the instruction-local sink delta as ROW_COUNT and its presence as FOUND after
successful completion. The sink never enters definitions or session specializations.
The balanced outer PL action spans command/USING operands, execution, cleanup and
postexecution assignment. Each executing command borrows the complete computed
SQL in an operation-local live node through all producer/command/activation cleanup;
the lazy context renderer emits its SQL statement frame only on request. That node
is absent during operand evaluation and removed before INTO checks or assignment.
Dynamic error callbacks remain separate: expression and post-execution
assignment/cardinality keep their own provenance without duplicate query frames.

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

Each exact Driver DSN owns one `DatabaseCluster`: a pointer-stable metadata mutex,
shared [`access.RoleStore`](../access/README.md) (principal domain, ACL authority,
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

Persisted defaults share the immutable role publication and its existing
transaction journal. Scope keys use principal references and cluster database
identities, never names or local replacement epochs. Settings-only writes retain
graph authority lineage but still participate in complete-record three-way
merge, savepoints and final publication. Scope writers use cluster lock authority.
Physical startup captures one committed defaults root for the login principal
and selected database before callbacks; provider-present values win per name.
Typed reset values and their sources remain fixed for that physical lifetime.
Startup role selection does not recursively load another role's defaults.
The existing physical `SessionRoleState` capture supplies SHOW/current_setting
and FROM CURRENT role text, independently of definer/replay/RI effective actors
and live descriptor renames. Startup retains the accepted reset ref/text pair,
including active self selection. Explicit session authorization clears the role;
RESET/DEFAULT and DISCARD restore the captured pair. Persisted role validation
uses the same source-independent restricted-parameter guard as live mutation.
Validation clips a local role candidate with `settings.NormalizeCatalogName`
before lookup but retains original extracted catalog text. Startup normalizes
only the selected provider-or-persisted input and captures that accepted text
for both live and reset role state; clipping NOTICE precedes lookup diagnostics.
Rejected persisted startup roles warn and retain login identity; callbacks run
outside locks. Private Restore bypasses persisted startup defaults, while
destination defaults survive replacement.

Creation clones/remints a lazily retained immutable real UTF8/C builtin seed,
not the application's changing default database. Mutable rows, indexes and
sequence generators are independent. Copied system owners remain bootstrap
`postgres`; `public` remains `pg_database_owner`, with PUBLIC USAGE and implicit
CREATE for the new database owner. New database ACL is NULL/default rather than
copied from the attachment. The seed is private, not a selectable mutable
`template1` or a fabricated registry row.

The CREATE DATABASE executor and its option rules are in
[`rolecmds`](../rolecmds/README.md#database-creation).

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

Each implementation carries a compile-time assertion; core code names
subpackage types directly (no aliases). Family hosts embed `DDLHost` and live
with their family ([root README](../README.md#hosts)).

| Core type | Implements |
| --- | --- |
| `statementHost` (a `StatementScope` conversion; `statement_host*.go`, `*_runtime.go`) | `exec.Host`, `dml.Host`, `dml.FinishHost`, `index.WriteHost`, `partition.RecipeHost`, `fdw.WriteRuntime`, `regobj.NoticeHost`, and the bodies of the `functions` evaluation capabilities (`ACLRuntime`, `AdvisoryRuntime`, `BlockingRuntime`, `CatalogInquiryRuntime`, `SessionInquiryRuntime`, `SequenceRuntime`, `TransactionInquiryRuntime`, `LargeObjectRuntime`, `PgrowlocksRuntime`, `FTSQueryRuntime`) that `*exec.Context` forwards |
| `DDLHost` (`ddl_host.go`): the core base, embedded by every family host | `ddl.Host`, `ddl.Allocator`, `index.KeyHost`, `table.TypedResolver` |
| `BindingHost` (`analysis_host.go`) | `analyze.Host` |
| `EngineCatalogRuntime` (`catalog_source_runtime.go`) | `pgcatalog.Runtime` |
| `enginePreparation` (`exec_preparation.go`) | `exec.Preparation`, installed with `exec.InstallPreparation` |
| `sessionHost` (an `Engine` conversion; `session_host.go`) | `session.Host` |

`DDLHost` keeps only the `ddl.Host` methods, what core paths call
(`LookupCollation`, `NamedType`, `OtherDatabaseDependencies`) and the methods
that unrelated family hosts share (`AddDomainBase`, `RemoveObject`,
`RelationDiagnostic`, `ResolveRelation`, `SelectFunction`). A family host
embeds it, keeps its `{E, Op, CatalogStage}` layout and is built by family
code. A statement or stage slot that one family owns is opaque:
`StatementScope.DropsSlot` is a `StatementSlot` (core only clones it for
PL/pgSQL exception checkpoints; `dropScopeOf` recovers the `DropScope`), and
`CatalogStage.ExtensionSlot` holds the `schemacmds` extension host that began
the stage (`extensionNamespaceOf`).

`statementHost.VerifyHeapam` supplies the distinct heap-verification runtime.
It resolves exact regclass authority, borrows and releases only its added
AccessShare contribution, readmits the same descriptor/storage after waiting,
and scans immutable unfiltered captures outside metadata locks. Sequence
targets use sequence state. A final incarnation fence precedes returning the
owned records; no user constraints, codecs or physical block mapping run.

`DropProvider.ColumnDependencies` and `ExpressionDependencies` share one canonical
closure discovery, full RESTRICT preflight, retirement admissions and
stage/preparation effects. Both are unlocked controllers: only their leaf mutation
methods take `Engine.Mu`. Column-root/positive-attribute traversal includes owned
sequence AUTO adjacency and its NORMAL descendants before effects. Whole-provider
roots retain their distinct later owned-expression phase, including genuinely
reached hierarchy owners; their intermediate index layouts remain immutable.
Full captured targets retain admission and observation authority even when local
payload execution is deferred. Captured trigger payload identities join those
targets before effect classification, including automatic-only retirement.
`table.CascadeColumnDrops` consumes captured stable attributes in one metadata
cook and projection per surviving owner; it never re-enters a dependency controller.
The batch cooks the authoritative current staged descriptor, preserving earlier
CHECK/default/policy pruning, but projects from the captured physical predecessor
and allocates its candidate revision from that captured closure descriptor. It
neither increments already revised metadata again nor relaxes
`CatalogStage.AlterRelation`'s +1/identity/storage-root guards.
The original column's shape remains `table.OrdinaryColumnDrop`'s responsibility.
Indexes remain readable through UNIQUE/exclusion pruning, and owned sequences
retire after their column's metadata is cooked, without rediscovery. Scope-free
commit uses its supplied physical projector, not an invented SQL scope.
A successful column closure reports its existing `view.EmitCascade` group once
after admissions/effects, before the next action. Preparation captures and replays
these operation notices once; later action failure does not erase an already
reported group. Only real lock tickets enter closure grant acquisition, including
definition retirement, surviving relations and owned sequences. A low-level
operation scope is not an admitted transaction: its private effects/observations
and ordinary final identity/dependency publication fences remain intact.
The locked ALTER owner captures `ReferencePath` for column diagnostics and
passes it through the ordinary stage/preparation adapters. Moved objects use
the shared visibility-sensitive dependency labels, never a bare-name fallback.

## Lock order

`replacementMu` and `TransactionOwner.Mu` precede `Engine.Mu`, which precedes
`Session.Mu`, `SettingsStore.Mu` and `Engine.sessionMu`; the lock manager's
mutex is innermost and never taken under `Engine.Mu`. Callbacks and external I/O
run outside these locks ([full rules](../lock/README.md#mutex-ownership-and-lock-order)).

## Row mutation

MultiXact records, counters and history belong to the lock manager. CHECK and
branch admission precede the physical event; index and RETURNING evaluation
follow it. Shared mutations journal successor/deletion events while private
roots are locked and flush after the engine/owner unlock. No `Engine.Mu` →
manager-mutex acquisition exists. Finish discards private stages on failure.
Each DML activation captures the current owner-private trigger list, not a
list cached by its prepared handle. VACUUM is
[`tablecmds`](../tablecmds/README.md#vacuum)'s.

AFTER state belongs to `StatementScope`, keyed by monotonic effect-command
identity rather than swapped DML scratch. Writable CTEs share their query frame;
routine SQL completes its own frame synchronously. Accepted ROW WHEN predicates
run unlocked on final images; queued bodies run at command completion after
immediate obligations and before publication, with rows preceding statements.
Statement registration is execution-only and independent of row count. Statement
WHEN qualifies each producer's registration at its exhaustion, before immediate
RI work and row-body draining; firing does not reevaluate it. A repeated
target/event producer replaces prior decisions, while bodies still drain once
at query completion. Where statement effects require consumer boundaries, writable
CTE modifiers activate on demand through their existing typed materialized source.
RETURNING candidates qualify at that source's EOF; non-RETURNING modifiers qualify
on execution exhaustion. Remaining auxiliaries exhaust individually after the
primary, never by blanket lexical deferral. Mutating RI child commands retain the
outer AFTER query frame, matching SPI execution without a separate trigger
boundary. They qualify each producer, including zero matches, under the child
owner; all RI changes precede row/body draining, and later same-target/event
statement sets supersede earlier sets. Routine/helper SQL remains independently
framed, and all effects share the existing outer rollback stage.
Checkpoints detach event/producer suffixes, qualification decisions and completion/
drain cursors while preserving suspended frame identity; effect serials never rewind. Finish releases
all queue captures on success, failure and cancellation. No callback holds engine
or owner mutexes, and trigger-body SQL uses the existing private stage.
Each AFTER invocation retains the current immutable function descriptor validated
under the engine mutex, then executes it unlocked against the current routine
catalog. Queued trigger definitions, physical relation metadata and detached tuple
images remain tied to event admission, independently of executable replacement.
Catalog utilities reached inside an active routine stage into the enclosing
statement's private journal, including implicit commands whose journal was empty
on entry. Only successful outer completion publishes those definitions together
with row effects; standalone utilities retain their ordinary publication boundary.

## Prepared statements

- `Description` is the actual fixed parameter/result and capture carrier used by
  Go handles, SQL definitions, protocol and the binder. Its `Analysis` owns the
  admitted target/source occurrences and complete ordinary dependency manifest.
  `CollectAnalysis` collects closure and purpose; `Valid` checks ordinary
  dependencies, while `FoldsValid` separately checks executable fold lifetime
  without dropping operand dependencies from analysis. `Engine.Mu` and physical
  session admission serialize mutation; there is no additional cache authority.
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
  An installed hstore/lquery/ltree/ltxtquery/ISN owner/ACL-only invalidation in the same environment may
  carry its nominal cast identity into a single candidate analysis. Executable
  analysis is still invalidated; current lookup, authority and coercion run
  before fresh captures publish. A failed candidate leaves the original casts
  retired. No alias repairs obsolete source spelling.
  Direct held hstore/lquery/ltree/ltxtquery/ISN literals use candidate-only datum reownership from the
  exact captured analysis descriptor to the genuine current installed manifest
  member, under the same environment and definition gates. A local pointer
  import preserves immutable payloads and NULL framing without weakening Retag
  or catalog import roots. Identity-only attempts leave original temporals
  intact; successful reanalysis publishes fresh temporals and analysis together.
  On genuine invalidation after hstore/lquery/ltree/ltxtquery retirement, an authentic capture whose
  original type address is absent is omitted only from the attempt candidate.
  Normal binding reads the original SQL literal under the new genuine usage;
  neither a reused label nor a numeric OID authorizes importing retired data.
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
- Immutable typed folds belong to `exec.FoldDefinition`, qualified by
  `PreparedFolds` with the accepted analysis and coherent environment. Their
  occurrence, stored/default/view origin, callable/coercion/provider, ordered
  input Usage and physical datum identity distinguish otherwise equal source
  spellings. Values own immutable payloads; unchanged hits reuse accepted
  backing rather than re-inputting or copying compounds.
- Fold lifetime is distinct from ordinary catalog reanalysis. An incidental
  operand-presentation revision may preserve a compatible fold; actual
  executable dependencies, native/type/provider replacement, effective
  environment changes and DISCARD retire the relevant lifetime. Ordinary
  authority, binding and admission checks still run before reuse. Stable,
  volatile, row-, frame-, stage- and domain-dependent demand is not retained;
  nonzero formal counts use operation-local groups, including constant siblings.
  Dictionary option changes remain revision-checked analysis operands without
  becoming executable fold dependencies: a warmed constant `ts_lexize` keeps its
  accepted typed lexemes while runtime-token calls resolve current state.
- `BeginFoldPublication` creates the actual operation-local `FoldPublication`
  consumed by planning. Final `Accept` validates the exact description, accepted
  postadmission environment, native epoch, dependencies, represented role/RLS
  stamps and cancellation before accepting the exact lower-owner attempt.
  Successful analysis, complete fold planning and statement effects are separate
  publication boundaries: failure publishes no prefix, but later import or
  execution failure does not undo already accepted analysis or planning.
- `PreparedRegistry` owns actual physical-session definitions and bindings.
  Lookup/Install/Remove act on the definitions consumed by execution; replacement
  retires publication rights, not already accepted independent resources.
  `PreparedBinding` retains accepted description/occurrences, transaction/birth
  identity and immutable `exec.AcceptedFolds`. Its `PreparedResource.Root` is the
  real graph consumed by the cursor, with the existing unlocked terminal close
  capability, never an observation copy or a graph in a shared definition.
  Strict scalar read erasure removes invocation identity only from the private
  executable expression; original bound source and analysis manifests retain
  their routine lifetime. RECORD and set-returning file calls keep invocation
  identity. Parameter-imported folds belong to each accepted binding, never the
  definition or another frame; a rejected final publication cannot replace
  accepted backing or generations.
- Parse/Describe do not fold; Bind plans supported SELECT/DML/CALL bodies without
  effects. Command portals retain a close capability but no query producer root.
  Old accepted portals keep their fixed group/resource independently
  of definition close/name replacement and cannot publish into a replacement.
  Missing accepted groups for Bind-planned commands reject rather than silently
  recompute. Utility portals instead import inputs at Bind and use ordinary
  admission/planning at Execute.

Dictionary execution captures and validates the current private descriptor under
`Engine.Mu`, then runs immutable lexization after releasing it. Installed
`unaccent(text)` resolves the dictionary named `unaccent` in the function's current
schema; mutable member names are not a replacement for that lookup. Dictionary
DDL staging belongs to `schemacmds` and publishes through the normal catalog journal.

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
  ([policy](../session/README.md#what-it-owns)). Feature-specific view/materialized OWNER
  is AlterView; collation/conversion OWNER is included. Procedure creation,
  language lifecycle and concurrent index variants keep their independent restrictions.
  Definition mutation admission captures original identities, waits outside
  `Engine.Mu`, then builds at most one final stage from current authority when
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
same Engine.Mu section that constructs its identity-bound stage; any changed
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

`Engine.Settings` holds one [`settings`](../settings/README.md) `Registry` by value,
built before provider registration and publication; providers borrow a pointer
to it, and descriptor validation and grant-name canonicalization use the same
registry. Notice levels, physical-session values, the settings journal and
timeout publication belong to [`session`](../session/README.md).

`current_setting(text[,boolean])` and `set_config(text,text,boolean)` use
canonical typed setting expressions, shared fixed-formal binding and live
`InvokeSetting` session lookup/installation. Ordinary SELECT binding owns output
labels and function-name defaults; presentation does not alter invocation,
authority, NULL handling or setting effects. Each overload retains its own
builtin identity and live EXECUTE admission. `current_setting` is strict and
returns typed TEXT NULL for a NULL name or optional Boolean. The optional true
flag returns NULL only for unknown names; it does not bypass restricted-read
authority.

## System catalog producer contract

Producers, the OID registry and descriptor specs live in
[`pgcatalog`](../pgcatalog/README.md); core keeps orchestration.

- `openCatalogSourceLocked` (`catalog_source_capture.go`) looks up
  `pgcatalog.ProducerFor` and chooses the per-OID catalog/role roots,
  presentation and unlock timing; it is not a second dispatcher. Only `pg_locks`
  and lazy `pg_sequences` bypass producers.
  Information-schema domain and column inquiry sources use the statement's
  paired catalog and authorization roots, including `column_domain_usage`;
  source opening preserves that pair rather than resampling publication.
  Shared-memory view capture additionally checks the current EXECUTE ACL of
  its metadata-only backing routine, after ordinary view SELECT admission and
  before the pure producer. Neither grant is retained in prepared artifacts.
- `catalog_source_cache.go` keeps the latest `pg_type`, `pg_proc` and
  `pg_depend` rows per engine, owned by `Engine.Mu`, keyed by the published
  `*catalog.State`, the published `*access.RoleRecord`, the source relation and
  its ref. Statements reading the published catalog and roles share these rows
  read-only; private transaction catalogs or role records capture fresh. Errors
  are never stored, and a hit still returns the caller's context error. The slot
  also keeps an `exec.CatalogKeyIndex` of its rows; hits and misses attach it as
  `Source.CatalogKeys`, so oid equality filters visit only matching rows.
- `EngineCatalogRuntime` exposes values per call, never Engine, StatementScope,
  AuthorizationView or bindings. Settings rows capture one coherent
  physical-session snapshot after unlocking `Engine.Mu`; `pg_locks_capture.go`
  keeps an unlock/capture/relock epoch fence.
  Routine-default presentation validates the paired capture and exact Procedure,
  then renders checked expressions or declaration-owned finite literals through
  `stored.Deparser` with captured path/output/string settings. Source literals
  retain their type and spelling across validated assignment coercion; no
  expression evaluation, provider binding or replay-renderer change occurs.
- `catalog_sources.go` registers descriptors from `pgcatalog.Specs`, preserving
  registration order, OIDs, columns and row/array links. It copies relation
  templates built once per process and sets this engine's owner, ACL and column
  types; engines share the templates' index maps and attribute slices.
- Provenance is same-pointer authority (`&e.catalog.Owner`), never equal
  names, OIDs or epochs.
- `pg_policy` captures the statement's paired catalog/principal roots, including
  transaction-private policy definitions, without a separate cache or policy
  registry. Its runtime adapter validates authority and constructs the stored
  immutable carrier; metadata does not evaluate conditions. Existing journal,
  savepoint and Reset/Restore publication fences own visibility and retention.
  Actual admitted source dispatch (including empty metadata) is covered by
  `TestCatalogSourceRuntimeCapture/policy_admitted_runtime_authority`, not only
  producer fixtures with injected runtime authority. It also asserts original
  frozen-policy retention and stale-reference rejection.
  `TestPGPolicyDisplayAuthority` exercises real inquiry capture for a noncreator
  actor with and without namespace USAGE, including retained deparse inputs
  after an ACL/session change. Public metadata/deparse does not confer provider
  execution rights or replace the policy's immutable presentation.

## Catalog writes and built-in seed

- `catalog.RevisionFrontier` is pure and saturating and never reserves
  the global allocator; publication commits its local frontier only at
  successful installation under `Engine.Mu`.
- Persistent replacement reserves OIDs with an operation-local base/next cursor
  through the same `catalog.Store.AllocateOIDFromLocked` checks as ordinary
  issuance. All preservation OID users share it. Only final catalog installation
  commits its frontier; no-fit, cancellation and later validation failures leave
  live capacity unchanged. Unlocked repair and final installation fence the
  live OID frontier, including aborted ordinary allocations without a new root.
  Failed candidates never reset another operation's consumed identities.
- Staged type identity changes (`type_generation.go`) authorize generation
  reownership only from the exact published source descriptor to the changed
  catalog-owned scalar, with unchanged extension and companion identity.
  Ltxtquery uses this transient import authority for stored values and expressions;
  ordinary same-OID `Retag`, including typed NULL, remains unauthorized.
  `ValidateCatalogStage` runs this whole-catalog pass only when a staged type
  replaces an existing descriptor with a new SQL type; a stage that only adds
  types (a new relation's row and array types) skips it.
- `CatalogStage` embeds `catalog.Candidate`, which owns the catalog-only
  publication fences, the apply step, the history markers and the creation
  ACLs. Publication's value sub-steps belong to their packages: row shapes,
  candidate relations, birth stamping and replacement capacity to
  [`storage`](../storage/README.md), index captures and candidate fences to
  [`index`](../index/README.md), statement catalog authority and journal shape
  recording to [`session`](../session/README.md). Core keeps the skeleton:
  the `PublishPreparedCatalog` lock and retry loop, `authorizationLocked`, the
  session-mutex revalidation, XID assignment and the install block,
  `replaceCatalog`, `stageCatalogOverlay`, the fence order, DDL
  authorization, lifecycle, rowtype synchronization, the extension
  materialized-view fence and the post-apply validators. Recipe rewrites
  retain the original routine authority, never recovering it from an OID.
  Effect-free dependency staging does not require an admitted transaction:
  definition-retirement locks belong only to an execution scope's real ticket.
  Direct validated publication still enforces captured identity, epoch and
  incoming-dependency fences without manufacturing a transaction for preparation.
- Every engine seeds the same built-in catalog in one bulk load (`NewEngine`'s
  `BeginBulkLoad` … `FinishSeedLocked`) through the
  [`catalog`](../catalog/README.md) seed steps. Core keeps their order, the
  capacity, the process's `catalog.SeedShare` (`builtin_catalog.go`), the
  builtin domain checks, the catalog source descriptors and the database
  descriptor; each engine validates and binds only its option-dependent
  parts. Check `make bench` allocations when adding built-ins.

## Function runtimes

`functions` nodes own binding, immutable recipes and argument demand; the core
runtime bodies (`*_runtime.go`) keep the authority. No prepared node caches an
owner.

Generic file runtimes borrow live expression-principal roles and logical path
settings in Engine -> Settings -> Session lock order. Shared lexical validation
performs no host I/O. Read length checks surround path authorization in released
order; stat/read then reject their physical mechanism. Tmpdir uses the retained,
owner-validated catalog inquiry snapshot for typed tablespace existence before
rejecting physical listing. Nodes retain neither authority nor physical state.

`acl_runtime.go` resolves special inquiry actors under `Engine.Mu` from current
execution authority. Parameter tokens precede named-role lookup and arbitrary
folded parameter names need no provider. Large-object role lookup precedes tokens;
existence and ACLs share the immutable active LO frontier unless ordinary UPDATE
selects the current private catalog. Grant-option-only UPDATE remains active.
Query-source admission captures `Inquiry.CommentCatalog` once, before expression
evaluation; this immutable query owner, never mutable `RoutineAuth`, supplies an
unset LO frontier. The shared `writeAuthorizationLocked` seam preserves it
before every current-authority consumer, including ACL inquiries, namespace and
blocking inquiries, sequences, pgrowlocks, and DML/index/RI operations. Utility
and CHECK paths without an admitted query freeze their private catalog there
before the first such operation. Other authorization refreshes cannot advance
the query-owned catalog. SQL-body activation supplies its explicit fresh LO
frontier and saves/restores both outer slots stackwise, including a previously
unset LO frontier after argument-side creation. Direct LO operations and all
inquiry families use this shared seam; ordinary UPDATE still uses current
authorization, without retaining permission decisions. Compatibility is read
from the operation's effective settings under that store's mutex only after
existence, without changing byte-operation admission.

amcheck's restricted-owner boundary enters that capture seam before replacing
the inquiry presentation, preserving the admitted outer query's LO frontier
even when its first callable uses restricted-owner rather than write authority.
It does not recapture an already active SQL-body frontier or restore an older
frontier when the restricted frame returns.

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

`catalog_inquiry_runtime.go` and `session_inquiry_runtime.go` keep in core
SQL-callable precedence, analyzer/coercion authority, statement snapshots, lock
and authorization recapture, pending-TEMP staging, schema caches and lazy
routine admission, as well as stored literal pairing, logical identity
remapping, prepared invalidation and catalog-replacement fences.

### Other runtimes

- Ordinary blocking reads never nest `Engine.Mu` and manager mutexes.
  Safe-snapshot capture and transaction-inquiry XID/timestamp sampling run under
  `Engine.Mu`; members are packed from the manager's snapshot clone outside it.
- `sequence_runtime.go` runs the catalog epoch fence (with TEMP epoch
  revalidation) before and after RowExclusive `acquireSequence`, then the ACL
  and read-only checks; call semantics belong to [`sequence`](../sequence/README.md).
- `expr.Source.SourceAdmission` and `exec.OpenFunctionSource` own source
  admission; stage rewrites use `expr.Substitute` without re-resolution. Stored
  literal capture validates every admitted `expr.Capturable` operand before
  writing any annotation.
- Stored DEFAULT closure extends through a SQL routine only for a real checked
  call and never executes the callee body. The release identity index is
  diagnostic only (42883 versus 0A000); it never grants execution.
- Aggregate SQL support enters `InvokeAggregateSQLRoutine` with its explicit
  worker context. A cancellable, statement-owned gate in `sql_routine.go`
  protects routine depth, lexical scratch, descriptions and notices while
  independent aggregate scans and builtin state work remain concurrent.
  `statement_scope_state.go` owns the gate and temporary invocation context;
  `sql_routine_scope.go` preserves the existing lexical activation/restoration.
  Nested binding, execution, coercion, result reads and relation waits use that
  context without replacing `Operation.Context`, acquiring another transaction
  admission lease, starting a root command or calling `Finish`. Context scratch
  restores before the gate releases, including cancellation and failure.
- Parallel-operation context covers leader support work as well as workers.
  `parallelExecutionEligibility` reuses `session.ModifyingTag` to reject reached
  modifying commands with `25000` before nested command admission/effects and
  central execution dispatch. This includes modifying CTEs, nested support calls
  and TEMP writes; a falsely SAFE declaration does not grant write permission.
  Ordinary serial support commands retain their existing behavior. Rejection
  keeps the incumbent reached SQL routine statement context.
- Aggregate domain seeds enter `schemacmds.RoutineHost.ValidateAggregateInitial`
  through `BindingHost.DecodeDomainLiteral` and the existing
  `DecodeLockedDomainInput` bridge. CHECK evaluation runs outside `Engine.Mu`
  against captured input/providers; context, caller authority, catalog epoch and
  provider identity are readmitted before publication. Ordinary seed input and
  domain diagnostic provenance retain their existing paths.

## Server protocol and timeouts

The wire adapter ([`cmd/stuntgres`](../../../cmd/stuntgres/README.md)) reuses the
session, transaction, prepared-analysis and typed-frame authorities.

- `session.ProtocolCycle` retains implicit transaction work, not an operation or
  admission lock; each message acquires its own command context. Wire portals
  share names with SQL cursors but own their position and lifetime.
- `ExecuteProtocolSimple` streams plain SELECT from the live producer; other
  row-returning commands fill a row store first, so an inner failure emits no
  partial rows. Stored typed rows are never rewritten or replayed.
- `CopyProtocolHooks` run outside engine/publication locks and admission; wire
  COPY FROM holds a cancellation owner through decoding, released on every exit.
- Session checks observe due transaction/idle budgets under `Session.Mu` before
  publishing a lease, with one timestamp for both slots. A statement timeout
  before `StartStatement` fails its explicit block synchronously through
  `StatementScope.Finish`'s disposal; no timer callback mutates the transaction.

## Testing

Statistics definition/capture/replay invariants use `RUN='^TestStatisticsObject'`.
Publication units use real provider barriers and exact namespace/name resources,
join workers before cleanup, and reject retirement or competing bindings without
adopting successors. Use `RACE=1` for the publication family.

Logical EXPLAIN ownership/presentation and nonexecution boundary units use
`RUN='^TestExplain'`. Their real prepared owners must retain no accepted child
fold group; the installed foreign capability must remain unopened. The render
cancellation context targets the renderer's cooperative context boundary and
must follow that boundary if its owning method is renamed.

Connectby completion, reconstruction, remapping and descriptor units share
`RUN='^TestConnectby'`; provider manifest/owner units use `RUN='^TestTablefunc'`.
`TestConnectbyAdmittedExecutionOwnership` observes the complete recursive
source/RLS capture and local-view release on success, nested conversion failure,
admitted sort cancellation and cancellation after child output accumulation.
It reuses real context polls and the existing stack-aware cancellation seam,
without timing or production hooks. Snapshot ownership units also distinguish
explicit-base, latest and unrelated activation captures.
`TestConnectbyAdmittedDMLCancellation` installs that seam on the admitted
operation and runs the real INSERT owner through both phases, asserting no
published result or target-root change and complete subsequent DML publication.
Accumulation evidence requires a second connectby child-query parse and statement
activation, followed by a poll from the exact traversal function (not its emit
closure). That later query is reachable only after a completed non-root emit;
the ownership probe also requires distinct local views for both activations.
Caller-context Err overrides are not phase probes through the public driver:
session admission deliberately derives its own cancellation context.
Crosstab capture, generation, prepared-definition ownership and nested activation
invariants share `RUN='^TestTablefuncCrosstab'`. Their composed helpers exercise
actual installed declarations, accepted protocol bindings and statement hosts.

Heap-verification capture, admission, incarnation and pure logical-row units
share `RUN='^TestVerifyHeapam'`; use `RACE=1` for capture/admission coverage.
Public scenarios use `RUN='^TestReferenceAmcheckHeap'` in `reference/indexes`.

Trigger payload/frame/runtime/remap/stage units share
`RUN='^TestTriggerCore(Catalog|Frame|Return|Runtime|Persistence|AtomicStage|PartitionPath|WhenProvider)$'`.

AFTER query-owner, detached-image, definition-kind and cancellation invariants
share `RUN='^TestTriggerAfter'`; test-only accessors expose private frame progress.

Event-trigger journal, frame, projection, drop and rewrite units share
`RUN='^TestEventTrigger'`; public direct/prepared behavior belongs in the
dual-mode reference suite.

Routine invocation, CALL projection and retained-fold ownership units use
`RUN='^Test(SQLRoutine|PLpgSQLProcedure)'`, also with `RACE=1`.
Composed effective-user and setting environment ownership uses
`RUN='^TestRoutineEffectiveUser'`, also with `RACE=1`. These units exercise real
entry/unwind, actor nesting, codecs/policy and partial-entry restoration;
public SQL behavior remains in reference scenarios.
The private session cache is observed by same-package handler units over seeded
catalog descriptors and real session admission; composed external units assert
checked bindings and compiler ownership. Neither requires an inspection export.

Core tests are package-level units of these contracts; public SQL behavior is
tested only in `reference/` ([docs/TESTING.md](../../../docs/TESTING.md)). SHOW ALL
description rules are in the [`settings` README](../settings/README.md#testing).
Add `RACE=1` for lifecycle, cancellation and contention selections.
Composed prepared tests save actual immutable `exec.AcceptedFolds` payloads to
assert definition ownership, generations, exact backing, resource graphs and
rejected publication independently of shared public SQL outcomes. No production
inspection export or observer registry is introduced for those assertions.
Pure typed/datum/cancellation cases live with exec; stored retagging/pruning
cases live with stored. Select `^TestPrepared(Rebind|Fold)` and
`^TestStringVariadic(RetainedGeneration|BoundedCancellationAndSizes)$`.
Installed hstore ownership uses `RUN='^TestHstore(Arrays|JSONB?|Comparison)PreparedOwnership$'`.
It exercises real protocol definitions/bindings and publication fences for array
shapes, prefix/comparison wrappers, JSON modes and comparison/hash scalar results,
retaining exact accepted datum and group backing.
Prefix conversions retain the backing-procedure fold while their operator
identity remains in analysis dependencies and the accepted portal graph. Tests
assert that each private wrapper consumes the exact accepted literal backing.
PL cursor compiler/provider/ownership units: `RUN='^TestPLpgSQLCursor'`.
Internal producer lifetime/cancellation units use `RUN='^TestPLpgSQLQueryLoop(Lifetime|Cancellation)$'`.
Static capture demand/cancellation/cleanup units use `RUN='^TestPLpgSQLSelectIntoRuntime$'`.
Static command ownership, validation, remapping, sequential assignment and
producer cleanup use `RUN='^Test(PLpgSQLSQLExecution|SQLCallableProcedureOutputAssociation)'`
with `RACE=1`.
Dynamic command ownership, complete draining and set-sink units use
`RUN='^TestPLpgSQLDynamicSQL'` with `RACE=1`.
Diagnostics live-stack/prepared ownership uses `RUN='^TestPLpgSQLDiagnostics'`
with `RACE=1`.

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
- UPDATE auxiliary-storage measurements preparse their fixed SQL before measuring
  bytes, then use real session acquisition and engine execution. Parser scratch
  pool retention is not an execution-memory invariant.

Core tests are external (`package core_test`) except the accessor files
`export_<area>_test.go` and the pure units that build no engine
(`replacement_admission`, `catalog_fence`, `expected_inputs`,
`fts_query_runtime`). An external test reaches an unexported core member only
through those accessors, filed by the area of the member's declaring file:
`<Prefix><Field>(x)` reads a field, `Set<Prefix><Field>(x, v)` writes it and
`<Prefix><Field>Ref(x)` returns its address. `<Prefix><Method>(x, …)` and
`<Func>(…)` wrap unexported methods and functions,
`New<Prefix>(<Prefix>Fields{…})` builds a struct with unexported fields, and an
exported alias names an unexported type. Prefixes shorten common owners:
`Scope` (`StatementScope`), `Stage` (`CatalogStage`), `CatalogRuntime`,
`Description`, `Cursor`, `Portal` and `Payload`. Tests call exported methods of
unexported types directly. Engine tests import `enginetest` and the root only
for the composition (`engine.New`, `Reset`, `Dump`, `Restore`).

A test beside family code cannot see these accessors. It asserts through the
exported API or SQL, or, as a last resort, through the exported inspection
accessors in production `inspect.go`. An accessor that only core's tests use
belongs in an accessor file, and one no test uses is removed; likewise an
exported name that only core's tests use returns to an unexported name with
an accessor. The inspection accessors are:
- catalog stages and publication: `CatalogDependencyPreflight`,
  `CatalogDiscarded`, `CatalogPublished`, `EngineCaptureCatalogStageLocked`,
  `EnginePreparePrivateCatalog`, `EngineStageCatalogChangesLocked`,
  `NewCatalogRoleReadFence`, `PreparedCatalogPublicationEpoch`,
  `PreparedCatalogPublicationStage`, `SetPreparedCatalogPublicationCtx`,
  `StageDomainAlter`, `StageMaterializedReplacementCurrent`,
  `StageRemoveObject`, `StageTableOrZero`, `StageValidateIndexMaintenance`,
  `StageValidateLikeSources`, `StageValidateRoleCandidate`;
- statement scope, transactions and roles: `CaptureSavepoint`,
  `RestoreSavepoint`, `ScopeAcquireMaterializedRefresh`, `ScopeAcquireRow`,
  `ScopeExecuteSavepoint`, `ScopeSnapshotPhase`, `SetScopeWorked`,
  `EnginePreparedRef`, `EnginePublishDatabaseFrozenXIDLocked`,
  `EngineRefreshOperationRolesLocked`, `EngineRoleRetirementPreflightLocked`;
- binding, query tables and catalog sources:
  `AuthorizationMaterializedPopulation`, `EngineOpenRelationSourceLocked`,
  `EnginePrepareQueryTableLocked`,
  `NewCatalogRuntime`, `ProcedureReturnsSet`;
- materialized-view source fences: `EngineMaterializedSourceFenceCurrent`,
  `MaterializedSourceFences`, `MaterializedSourceFenceRef`,
  `MaterializedSourceFenceRelation`, `MaterializedSourceFenceStorage`,
  `NewMaterializedSourceFence`;
- partition replay: `PartitionCheckReplayAllowed`, `PartitionReplayAllowed`,
  `PartitionUsingIndexReplayAllowed`.

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
make check RUN='^TestSetting'                         # settings registry
make check RUN='^Test(AlterDropColumnRevisionFrontier|CatalogConcurrentReplacement|AccessMethod|Builtin)'  # catalog writes, seed
make check RUN='^Test(CatalogRelations|NamespaceIdentity|ObjectAddress)'  # catalog inquiry and identity
make check RUN='^TestNotification'                     # immutable session/cluster queues and publication
make check RUN='^Test(Advisory|BlockingPids|DeadlockResultScheduling|RelationDeadlock|RelationLockWaits|PGLocks)'  # advisory, waits, deadlocks
make check RUN='^Test(UpdateCore|TransactionIdentity|CommitTime)'  # MultiXact, transaction identity
make check RUN='^(TestAlterDefaultGrantTimeDescriptor|TestNotNullAXGrantTimeDescriptorAndRetargetFence|TestAlterRenameLockGrantTime|TestQueryJoinsSecondLeafGrantRebind)$'  # captured identity after actual AX waits
make check RUN='^(TestAlterSchemaPublicationFences|TestAlterSchemaDependentGrantTimeName|TestCommentLocks)$'  # late destination collisions and queued comment identity
make check RUN='^(TestErrorFieldsExecutePublicationConflict|TestAlterRenamePublication|TestBtreeIndexConcurrentOrdinaryBuilds|TestRoleNoticesConflictSkipRechecksCreateAuthorization|TestSavepointsAuthorityAndCompletion)$'  # shared claim compatibility, diagnostics and completion
make check RUN='^(TestQueryTableCreateSnapshotAdmission|TestSafeSnapshotAbandon)$'  # failed acquisition discards snapshot ownership
make check RUN='^TestSnapshot'                        # binding, retained command/transaction pairs and physical frontier filters
make check RUN='^Test(Serial|SourceProtocol|ViewLiteralProtocol)'  # sequences, sources, stored capture
make check RUN='^Test([A-Za-z]+RoutineOriginalAuthority|SQLRoutine|UnavailableFunctionIdentityBoundary)'  # routines
make check RUN='^Test(Extension|PGDepend|CatalogDrop|DropOwned)'  # extensions, drop fences
make check RUN='^TestDictionary'                      # dictionary providers, publication, remap and retained typed ownership
make check RUN='^TestLqueryProvider'                   # installed scalar authority, staged publication and import
make check RUN='^Test(Protocol|SessionTimeout|StatementTimeoutAdmission)'  # protocol, timeouts
make check RUN='^Test(CatalogSource|InfoUserMappingsProducer|PGSettingsCatalogACL)'  # catalog source capture and producers; one catalog: ^TestPG<Name>
make check RUN='^TestTransactionalSchema(Admission|PrivateProviders|Publication|DropManifest)$'  # utility policy and private catalog stages
```
