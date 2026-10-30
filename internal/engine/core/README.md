# internal/engine/core

Core is the engine's runtime: `Engine` state and `Engine.Mu`, the catalog store
(`catalog.Store`, see [its README](../catalog/README.md)), statement scopes and
admission, lock waits, `CatalogStage`, publication and replacement, the
transaction lifecycle, sessions and TEMP, the hosts and binder construction,
protocol, prepared SQL, cursors, the bound DML and query path and the
procedural runtime ([`docs/ENGINE-SPLIT.md`](../../../docs/ENGINE-SPLIT.md) §3,
L15). It imports the layered subpackages and never the root or a family; the
family statement executors (the `*cmds` packages) call its exported API.

Detached ordinary catalog publication owns only its staged heap replacements
and removals. At final installation under `Engine.Mu`, it refreshes borrowed
heaps from the current committed map without mutating captured maps or retained
row vectors. Physical-session TEMP collation maintenance uses this same path:
retiring one owner's objects must preserve unrelated committed heap replacements
and peer TEMP objects. Fallible preparation and expression evaluation stay
outside the installation lock.
MERGE latest-row admission preserves transaction-local replacements and the
retained snapshot frontier. A later committed target update or deletion raises
serialization failure through the shared visibility conflict check; ordinary
statement failure and savepoint recovery own the private-write rollback boundary.

Replication-slot lifecycle calls borrow the current effective actor under
Engine → Session lock order, requiring its own superuser/REPLICATION attribute.
After strict operand handling and routine EXECUTE admission, create checks the
shared slot-name rules; logical create also checks temporary/failover and the
current exact plugin allowlist. Both reject unavailable WAL/decoding machinery
with explicit 0A000 and publish no slot or session ownership state. Drop/copy
truthfully report missing slots; advance rejects zero LSN before lookup.
There is no provider, dummy slot store or synthesized replication position.


Application-provisioned scalar host routines use the same typed frame, invocation
depth, EXECUTE admission, security/settings environment, volatility policy and
statement completion owner as SQL/PL routines. `host_routine.go` projects owned
carriers, releases the replacement reservation and engine mutex for every
application callback, and readmits the current operation/settings/session,
catalog epoch, descriptor capabilities and original invoking actor before
constructing detached SQL output. Callbacks receive no frame or authority object.
`host_registry.go` performs pure prepublication token/descriptor installation;
`host_materialize.go` distinguishes genuine new type generations from language
alias events, so alias creation never reinstalls retired directional procedures.
Routine SQL opt-in, not Program selectors, owns pair selection. Common declaration
associations survive captures/remapping independently of boundary conversion slots.
For a supported single OUT/INOUT host procedure, the actual Program scalar result
is converted as that member and wrapped by the shared `proc.PlanTuple` result
owner into CALL's exact nonnull record; a NULL member is not an absent record.


Routine declaration inquiry composes the statement-owned catalog and presentation
settings with the shared formal projection and metadata-safe default/body
renderers. It never invokes the target or checks its EXECUTE privilege. Actual
Procedure descriptors are authoritative; release identity aliases do not create
catalog objects. Public CREATE OR REPLACE output is independent of native dump
SQL, while both consume the same retained declaration and wrapper metadata.
Language presentation uses the actual descriptor address from that same snapshot;
native internal/C/SQL declarations resolve their fixed language identities.
Renames and aliases never require a display-label match or target compilation.
Body presentation carries the querying statement's captured temporary-owner
authority, never the stored routine's principal. Qualified own-session TEMP
sources use `pg_temp`; foreign or absent temporary authority retains physical
namespace naming through the unchanged stored relation renderer.


`StageForeignConstruction` strictly validates a complete CREATE candidate with
table metadata and no column options, forces ordinary publication into the
statement-private journal, then immediately installs an immutable foreign shell
before any user callback can run. Its sealed `ForeignConstruction` token owns the
ordered stable-attribute schedule and advances from relation shape to table
definition to validated column payloads using fresh latest-private stages.
Each advance rejects mutation of its exact current relation, never overwrites a
callback's change, and leaves unrelated callback effects intact. Nested tokens
retain independent address guards. Only strict validation of the actual completed
descriptor retires its guard; `Finish(nil)` rejects any pending construction and
error/cancellation cleanup releases token authority. Final provider validation
is unchanged and never accepts incomplete foreign descriptors.
Recoverable PL exceptions restore construction authority with their ordinary
catalog/effect checkpoint: suffix tokens become inactive, while preexisting
tokens recover their exact identity, predecessor descriptor and column progress,
even if they completed inside the failed suffix. Successful marks never rewind.

Logical publication DDL uses database-local immutable catalog descriptors,
ordinary definition/name/dependency admission and the existing statement journal.
`BindRetainedPredicate` shares the normal Boolean binder and frozen presentation
owner with policies; publication restrictions do not introduce an evaluator.
Common Execute admission checks declared UPDATE/DELETE capabilities against
current membership and replica identity, including empty targets, MERGE and
ON CONFLICT UPDATE. After locked-relation validation admits compatible metadata,
publication facts use the descriptor from the current captured catalog without
replacing the binding's retained row layout or authority. Actual partition-leaf
capture repeats physical identity admission against the same captured topology.
No predicate filters local SQL.
Publication admission precedes the hierarchy write-capability rejection, including
the early ON CONFLICT UPDATE boundary; unrelated unsupported executors remain
unsupported. ALTER's genuine metadata transfer validates remapped publication
mapping descriptors through their registered providers.
Catalog capture retains the exact catalog/role/source pairing and owned values.
`EngineCatalogRuntime.VisitPublicationTables` bridges selected publication relations
to low catalog-producer facts under that captured root. A private shared projection
supplies both the view and inquiry with the presented relation's direct mapping
and default attributes, independently of DML-effective ancestry coverage.
Callback errors and cancellation discard the source; no second resolver or retained
authority enters pgcatalog. Normal policy tree/display runtime methods own filter
serialization.

Subscription DDL uses ordinary definition/name admission, role dependency locks
and catalog journals. Shared catalog projections prepare replacement roots for
every database under the cluster mutex before the installation fence; no
callbacks, connection attempts or external I/O occur there. Definition ownership
is database-local, visibility is shared, and ENABLE is desired metadata only.
Each subscription owns an immutable allocated replication-origin ID (1..65534);
the main origin name uses its actual subscription OID. The cluster mutex guards
lazy pending exclusions, transferred from stage to statement journal, retained
through savepoints/internal exception statement journals and prepared ownership,
and released at canonical disposal. They are never catalog rows or public locks.
Reset/Restore remint selected subscription addresses and origin registrations
against retained peers and pending allocations. Final preparation rechecks
candidate uniqueness, exact peer predecessors and live exclusions. Failed
replacement changes neither roots nor reservation ownership. Detached catalog
reads keep their original immutable backing.
The source-opening policy captures operation authorization explicitly for
`pg_replication_origin`, pairing its private catalog and role roots with the
engine-backed runtime. CREATE DATABASE's unregistered replacement runtime has
no cluster exclusions; it acquires shared registrations only when attached.

Parameter ACLs use the same immutable shared projection, canonical-name writer
admission and cluster allocation frontier. SET, inquiry and catalog producers
consume coherent retained catalog/role views, not a second permission store.
Name claims retain their original committed binding through waits and expose
the ordinary transaction blocker; conflicting updates/first insertions fail
without retry. A parameter-replacement incarnation follows stages and journals
through private refresh and savepoints, including absent-to-absent Reset/Restore.
Private preparation checks that incarnation before stale descriptor/name
predecessors, so a genuine parameter replacement reports `ids.ErrCatalogReplaced`
rather than an ordinary publication conflict. The extension CREATE authority
fence still precedes those checks and retains its duplicate-name diagnostic;
ordinary competing UPDATE publications remain conflicts.
Replacement remints imported identities privately and prepares all peer roots
before installation; local data, destination database authority and roles remain
independent. Reader/writer I/O and lock waits stay outside the cluster mutex.
The pre-input replacement predecessor fences only Parameter incarnation,
descriptors and canonical name bindings. It does not fence unrelated catalog
edits during reader I/O or override existing principal remap, TEMP compatibility
and provider-validation diagnostics. Prepared-generation and allocation
preconditions still guard the final installation boundary.
Cooperative execution-pause requests are dormant during binding and catalog
capture. Audited detached owners call `StatementScope.ActivateRuntime` after
their final capture unlock, including recipe preflight, index preparation,
trigger conditions, procedure conversions and SQL EXECUTE actuals. This hands
off the replacement reservation for a closed pause even without an application
workload policy. Both capabilities use the supplied current invocation child;
application grant/readmission and final immutable-input/epoch/publication fences
remain authoritative. Binder and new-operation entry remove inherited execution
permission. Neither `Operation.Context` nor the statement host's generic Context
grants permission; children still own cancellation, deadlines, causes and release.
Detached cursor opening and each fresh FETCH/MOVE or protocol Execute pull
admit the current effective child once per invocation, not once per row.
HOLD promotion similarly admits its COMMIT child while demanding remaining
rows; promoted stored-row FETCH has no producer work to pause. These contexts
belong to ephemeral motion/Execute/materialization scopes, never the retained
cursor or a previous operation. Output publication and cleanup retain raw authority.
COPY output similarly enables its effective child once at the detached WriteRows
invocation; the existing row-source chain carries it into protocol query demand.
COPY input privately reuses one raw/active context pair, selecting the actual
session-owned standard child before comparison on each decoder pull. A changed
mask or statement budget refreshes the pair; cached domain evaluators rebind to
that same supplied current child. Finish retires both projections. Raw pending
protocol cancellation still vetoes publication after successful masked decoding.
Remapped replacement repair captures immutable role authority and destination
catalog, storage, allocator and TEMP-session predecessors under `ReplacementMu`
then `Mu`. It releases `Mu` then `ReplacementMu` before private-stage generation,
range, hash or index evaluation, selecting the repair statement's actual current
operation child once after successful capture. Every exit reacquires
`ReplacementMu` then `Mu`; successful preparation must pass exact predecessor,
prepared-transaction, active TEMP-owner and shared-reservation checks before
grafting live TEMP storage. Stage-owned maps are changed only through that
stage's locked storage methods. Final installation retains its independent
cancellation, capacity, storage, allocation and publication fences; stale repair
never retries or adopts a newer destination.
`ReplaceCatalogInOperation` accepts only a live operation of its distinct,
isolated source engine and validates that ownership before touching either
catalog. Ordinary self/foreign replacement uses `ReplaceCatalog` without an
operation; it admits its own private repair session when demanded.

Statistics expressions share the single-relation binder setup with indexes but
use a distinct `StatisticsConsumer`: no parameters, subqueries, aggregates,
grouping, windows or SRFs, and no index-specific IMMUTABLE restriction.
The DDL host preflights coercions and retains normal conditional execution
recipes, stable input identities and external dependencies. CREATE does not
evaluate volatile runtime values. Definition-object locks include these real
dependencies; publication rejects retired inputs/providers.
The authenticated catalog runtime projects ndistinct, dependencies, joint MCV
and expression summaries from one immutable extended fact. Missing payloads use
the current descriptor's typed NULL. Catalog SELECT admission precedes capture;
the runtime never collects populations or rebuilds retained statistics on read.
`statistics_snapshot.go` bridges semantic captured statistics to the existing
control-literal renderer and checked typed replay traversal. Destination value
construction uses the ordinary binder/executor under captured catalog authority.
Only this adapter supplies the retained structural-tree capability; ordinary
row replay and SQL input remain unchanged. Joint MCV native images are rebuilt
from typed dictionaries/counts, never copied with source OIDs.
The snapshot binding adapter supplies that captured authority and cancellation
context to type-annotation, namespace and object-name hooks. Constructor casts
use the existing immutable snapshot resolvers, not live-publication admission:
the destination generation is deliberately private until final installation.
Live operation resolution and final publication retain their original fences.
It is not an expression-statistics evaluator or a re-ANALYZE path. Stable
deparsing matches retained recipes independently of source object addresses.

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
Single-source filter access details share execution's detached CPU decision.
`CPU Cost` is separate from inclusive logical tree costs: it compares actual
hash-candidate versus full-traversal work without changing unrelated estimates.
Capture uses maintenance metadata (or default1000), never source counting,
bucket probing or expression evaluation. Explicit/private/RLS/unsupported
admission retains the safe full traversal; named hash indexes are ordered by
object identity. Local-worker costs use the same command-admitted value record.
Cursor declaration additionally captures its startup/total retrieval fraction;
ordinary queries retain fraction1. Existing explicit/private/RLS hash guards
remain unchanged, including for cursors. Implicit WITH HOLD declaration can use
the eligible hash alternative before promotion; promotion retains complete
results and releases the producer normally. Worktable estimates are renderer-local,
scoped by the owning CTEDefinition and its seed rows times the admitted factor.
EXPLAIN's `Pruned Sources` detail uses partition's actual conservative branch
proof over captured metadata, with comparisons unlocked and no source opening.
Method details use admitted `PlannerMethods` and pure shared runtime shape/frame
predicates: hash-assisted versus nested-loop joins and hash versus equality
grouping/deduplication. They do not assert runtime index-root eligibility,
partition interval counts or launched gather/foreign work. Statement scopes
own copied method values; routine scratch restores the enclosing value
independently of presentation refresh, while a separately admitted nested SQL
command captures its current effective settings. Cursor/portal executor owners
retain their original value across FETCH or suspension.
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
Their collected payloads reconstruct from checked immutable construction facts
when destination descriptors change, including nested enum/array identities.
Unchanged usages preserve MCV construction backing. All fallible reconstruction
precedes installation; retained captures keep their original values and backing.
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

Foreign option validation (`fdw_validation.go`) has one statement-owned entry,
`ValidateForeignOptions`, for declarations, phased table construction, ALTER and
IMPORT. It owns the typed argument frame through success, failure or cancellation
and borrows the same SQL/PL executor with the captured procedure, `text[]` and
catalog class `oid`. Its private catalog-validator admission omits only the
explicit-call EXECUTE check; ordinary routine calls retain that check. Both
entries validate the exact descriptor against current authority and share STRICT, caller body
privileges, SECURITY DEFINER/configuration, cancellation and environment unwind.
FDW admission supplies no support owner and does not impersonate an owner.
An empty option list is a non-NULL zero-element array. Non-NULL results are
discarded; a NULL result fails with the invoked procedure OID diagnostic.
Genuine native void validators retain their registered callback path. Validation
runs without `Engine.Mu` or the replacement reservation, before the DDL
publication fences. The common `ValidateForeignOptions` entry preserves an
existing private statement catalog journal or opens it before actual SQL work,
including table ALTER and IMPORT. Metadata and routine writes share root Finish
and cannot commit independently on body, result or publication failure. Empty
jobs and native callbacks do not open a journal through this SQL seam. Ordinary
SQL validators restore as routines, without destination native tokens.

Routine command policy is invocation-local and carries no cached actor.
PL CREATE validation and invocation compilation are distinct: an absent language
validator leaves immutable source/formals uncompiled, with language signature
checks deferred but catalog result safety retained. `CompilePLRoutineLocked`
enforces the genuine PL signature contract before parsing any reached source.
`PreparePLRoutineLocked` checks cancellation and compiles into private
declaration/program backing without publishing or caching it in the catalog.
Ordinary calls and trigger frame creation use current language capabilities,
never mutable names or captured USAGE.
Immediate row triggers validate identity and current capability before WHEN,
but prepare private PL code only after a true condition. WHEN evaluates unlocked;
the firing path reacquires the engine mutex and revalidates current executable
authority before compilation, then unlocks before frame construction/invocation.
Reached SQL and PL commands classify their analyzed executable binding before
opening producers, including writable CTEs and stored-source row marks.
Stable/immutable bodies borrow the calling query's coherent roots and private
write frontier; volatile commands merge reached staged changes and acquire a
fresh read-committed frontier. Transaction-stable isolation retains its roots.
Actual constant evaluation forwards its borrowed planning phase and uses the
active caller's roots before the reached command installs an executor view.
Runtime readonly calls borrow that command's installed view or its merged
private frontier when source-free execution has not installed a view; absence
of a view alone never selects planning visibility.
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
`activeTriggerDepth` belongs to the root invocation owner, separately from
routine safety depth and lexical scratch. Only admitted DML trigger bodies and
individual internal RI callbacks enter it, with deferred unwind. WHEN and queue
qualification read the surrounding level. Ordinary routines and event triggers
borrow it without entering; rollback and native controls never serialize it.
The three builtin DML trigger declarations and authentic installed native
trigger providers share PL installation, stored validation and live invocation
admission. Immediate and queued AFTER dispatch select the admitted native body
before allocating any PL frame.
Each body borrows OLD/current NEW and stable command UPDATE attributes; suppression uses
cancellable typed payload comparison, not SQL equality. Vector bodies capture
one immutable current FTS generation per invocation, resolving explicit names
eagerly and row OIDs at the first non-NULL source. Parser, dictionary and notice
callbacks run unlocked. Separate sources share a caller-owned position counter;
non-rebuilding UPDATEs still validate and parse but retain no output vector.
Argument lookup retains catalog system-attribute existence and type authority
without treating negative attributes as dense slots. BEFORE NEW is a transient
tuple: its tableoid is InvalidOid, not the producer relation's identity. Vector
replacement preserves the destination's owned logical descriptor using the
binary-compatible carrier, without introducing SQL assignment or fresh domain
input checks. Ordinary input and subsequent table constraints retain their owners.
The tcn body reads valid primary-index keys by stable attribute in key order,
uses ordinary type output with exact payload quoting, and appends only validated
events to the existing statement-local notification effects. UPDATE and DELETE
borrow OLD; INSERT borrows NEW. Commit, rollback, deduplication and savepoints
remain notification-owner responsibilities. Ordinary SQL helper calls retain
installed routine authority but never borrow an enclosing trigger activation.
Routine environments, trigger depth and statement rollback retain their existing
owners. C TriggerData/native ABI, heap/TOAST layout, WAL and process loading are
outside this process-local logical execution and control boundary.
Autoinc borrows the incoming tuple for every literal column/sequence pair and
clones output only at its first replacement. Only actual INT4 columns qualify;
NULL/zero demands late textual relation lookup and the ordinary sequence host.
Each allocated int64 narrows to int32; narrowed zero retries exactly once.
Sequence identity is captured before its allocation lock wait, and allocation,
ACL/read-only checks, session memory and rollback behavior remain sequence-owned.
Logical replay stores current helper identity and unchanged argument text,
never early-bound sequence/column identities or activation state.

Installed refint procedures join this checked native trigger boundary using
their immutable provider symbols. `check_primary_key` probes ordinary SQL for
the current referencing values; `check_foreign_key` executes restrict, cascade
or setnull against OLD keys, preparing every listed reference before unchanged
UPDATE detection. Any NULL local key short-circuits according to the stock
protocol. Arguments naming local attributes use exact catalog spelling; remote
relation/column arguments remain SQL text, including required identifier quotes.
These are not dependency-bound relation names and renaming a table does not
rewrite them. The generated SQL parser and typed binder share the same closed
positional parameter count and local key type declarations; outer statement
parameters never supply or bound a nested helper's frame.
Generated SQL uses the ordinary nested command owner, invoker
rights, RLS, inheritance and genuine child triggers. Effects share the outer
statement journal and roll back atomically, while already delivered action
notices remain observable. Per-invocation cascade UPDATE literals use current
NEW output values with SQL quoting and explicit NULL, without a plan cache.
Generated-SQL failures retain SPI-style internal positions/query or nested SQL
contexts; helper-owned violations use 09000 rather than declarative-FK 23503.
Native controls replay installed identities and postdata triggers, never frames
or cached invocation values. PostgreSQL's SPI/fmgr C ABI and shared-library
loading are external mechanisms, not exemptions from these logical SQL effects.

The stock moddatetime provider replaces one exact builtin timestamp/timestamptz
NEW field from the transaction start and current session zone, using unsized
precision rather than reapplying the column typmod. Literal arguments name
case-sensitive user columns; domains are not temporal helper destinations.
Ordinary SQL invocation is an unfolded evaluation-time protocol error.
BEFORE/AFTER STATEMENT, including TRUNCATE, and AFTER ROW enter the shared
native boundary and receive the helper's row-before-timing protocol checks.
Arbitrary shared-library loading and general PostgreSQL SPI are not engine paths.

Trigger-bearing admitted mutation targets also retain dynamic source roots at
the calling command's initial data-view capture: lazy trigger-body reads are
not outer expression dependencies. STABLE/IMMUTABLE trigger commands borrow
those fixed roots and frontier without recapturing after firing. Trigger-free
DML does not retain an additional dynamic-root map.

View INSTEAD OF mutations admit an exact logical endpoint with its lifecycle
RowExclusive lock and no storage or leaf replacement grant. The shared immediate
chain keys immutable command captures by relation, event and timing, readmitting
the live executable unlocked for each firing. Logical UPDATE/DELETE freeze the
complete view SELECT and matched source occurrences before any body runs; equal
rows are not physical identities. INSERT uses the ordinary typed input/default
recipes. Only nested body SQL stages physical effects. Non-NULL handling counts
one logical occurrence, I/U replacements feed later NEW and RETURNING, and DELETE
keeps original OLD. NULL stops the chain without undoing earlier body effects.
View statement AFTER completion, nested command frames, savepoints and the sole
publication journal retain the ordinary ownership boundaries.
Logical OLD and FROM/USING metadata join the existing command-source admission
before its RLS fence captures the full physical ref union. Their prepared source
recipes belong only to that activation; materialization consumes each once.
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
Within an actual parallel operation, leave checks both original-operation and
active-invocation cancellation before recapture and after a successful capture.
Authorization can still read coherent facts after cancellation; that is not
permission to replace the closed caller's saved owners. Canceled parallel or
failed recapture restores exact caller codecs, callback/lookup-path backing,
inquiry, identity/authority, row-security policy and presentation cache.
Ordinary serial leave retains live recapture even during cancellation: trigger
entry may have demanded coherent caller RI-actor/domain/ACL/type-name inputs
absent before Execute. A lexical SQL-invocation marker alone does not close that
ordinary contract. Successful serial recapture also follows legitimately
changed settings; no extra pre-trigger capture is added.
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

ALTER CONSTRAINT enforcement admission takes target ACCESS EXCLUSIVE, including
no-ops. Referenced-side trigger creation requires SHARE ROW EXCLUSIVE; removal
requires ACCESS EXCLUSIVE. Same-state requests add no parent grant. ADD-pass
captures precede MISC admission; mixed grants retain the strongest mode and
self-references deduplicate. Existing stored-key toggles preserve provider/storage
fences without becoming fresh REFERENCES declarations. Publication uses the
ordinary statement/transaction journal and cancellation boundary.
Lock modes are bit identities, not numeric strength ranks. This admission folds
its RS/SUE/SRE/AX requirements into one canonical strongest grant, so a
self-referencing VALIDATE retains SUE rather than adding a redundant RS bit,
and mixed enforcement transitions never downgrade AX. Owner proof:
`make check RUN='^(TestAlterValidateConstraintAdmission|TestConstraintEnforcementAdmission)$'`.
Later FK definition-provider readmission reuses an already-held AX grant rather
than appending the ordinary ADD-side SRE bit. It still performs the normal
epoch/catalog/target revalidation and provider-authority checks; ordinary
definitions without AX continue to acquire SRE.
ADD captures include the exact name chosen by the shared table naming authority,
whether explicit or generated. The command-local projection accounts for DROP,
prior index/NN reservations and ordered CHECK/FK name assignment, and is recomputed
after unlocked waits before cooking. Thus generated names receive the same
strongest parent grants and namespace-change fences as explicit declarations.
DROP NOT NULL recapture uses the table owner's shared NN selector rather than a
column-name deletion heuristic. Removing this relation's NN fact does not erase
another relation/domain reservation, and absent facts do not manufacture removals.
Table-method changes share this final private publication and AX ownership.
Command admission captures the current default without acquiring settings locks
inside DDL windows. Definition consumers fence exact original/new method and
handler predecessors, including pinned heap. Prepared analysis observes the
actual method/handler stamps; mutation admission validates its captured physical
relation without manufacturing a catalog journal. Captured row admissions and
coherent publication re-admission retain the existing owner lifecycle. No-op
assignment preserves descriptor/accepted authority; a genuine transition
reanalyzes without mutating old accepted graphs or requiring unrelated fold churn.

## Pending partition detach ownership

`PartitionRelations` retains canonical admitted descriptors. Query lock discovery,
leaf capture, pruning and routing derive one effective pending-edge topology;
catalog membership, bounds and dependency provenance remain canonical until
finalization. `PendingDetachSnapshot` borrows the existing stable owner's
`SequenceCatalog` solely for pending-edge eligibility. Fresh/RC-next commands
exclude pending branches; an older stable snapshot may retain them, never an
edge already removed or an identity recreated in another epoch.
`ONLY` source admission locks the named relation without descendants, so a
parent-only cohort holder does not also prevent pending-child rename/schema
transfer through an unintended child AccessShare grant.

The first concurrent utility publication is irreversible and retires its snapshot
advertisement and SUE relation grants before waiting a frozen parent-holder
cohort. Canonical `WaitTransaction(..., CompletionVirtual)` owns cancellation,
timeout and replacement fencing; no parent AX or second completion registry is
introduced. The second phase reopens exact refs and publishes ordinary teardown.
FINALIZE instead stages transactional teardown after child AX and older-snapshot
waits, preserving COMMIT/ROLLBACK/savepoint semantics.

Reader admission accepts only the exact predecessor plus final revision stamp
issued by the pending/CHECK constructor. Later revisions do not inherit that
permission. Finalization clears this edge's transient proofs; replacement clears
all source proofs without changing persistent pending membership.

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
Admitted event-trigger lifecycle mutations on the canonical catalog-overlay route
retain their original session revision and published role authority through
statement Finish, including the first mutation in a SQL routine with no journal yet.
Transfer preserves any enclosing statement authority; a completed statement does
not carry this execution fence into its outer transaction's Commit.

Standalone CALL follows analyzed command admission, fold publication and the
shared SQL/PL invocation owner, not synthetic SELECT or a second connection.
Describe exposes declaration-owned OUT/INOUT labels with checked invocation
member usages; execution exports one detached tuple through `anonymousResult`,
or no descriptor without outputs. CALL's
completion count is zero regardless of body effects. Quoted SQL-body CALL with
outputs fails before producers; no-output calls use the ordinary command runner.
Unquoted SQL bodies reject CALL during definition validation.

SQL-body initialization diagnostics refer to the retained body and the current
declaration's result category. RECORD and composite results retain their
statement/startup context rather than acquiring scalar inlining context.
Parsing and name analysis retain their attempted-inlining phase independently
of later query-shape rejection. Actual terminal-validator failures carry their
definition owner and simple-expression eligibility from the current binding:
only a single scalar projection over a singleton, without disqualifying query
clauses or aggregate/window/set/subquery structure, uses the inlining frame.
This classification neither rebinds nor executes an expression, and does not
apply later volatility/effect restrictions to return-validation diagnostics.
Reached-command reanalysis keeps its actual command ordinal independently of
whether the declaration was originally checked or source-only.

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
Promoted SQL held cursors likewise own typed output without producer authority.
Their exact same-session portal identity and failure state remain checked after
catalog replacement, but only unpromoted producers carry the old epoch fence.
Fresh eligible transactions may move retained output; old live transaction
execution and commit still fail at the ordinary replacement fence.


Text-search conversion captures an immutable catalog generation and configuration
identity under `Engine.Mu`, then releases it before runtime acquisition,
tokenization, resource compilation, lexization or notice callbacks. Capture does
not initialize mapped dictionaries. Omitted configuration resolves the current
`default_text_search_config` name from operation-effective settings at execution,
with Engine-before-settings lock order and semantic missing-schema/configuration
errors. Mapping changes publish through ordinary schema stages and definition locks.
JSON conversion validates its filter first, then resolves an omitted default
identity before traversal, even if no item is selected. Explicit configurations
retain lazy identity demand. Both forms acquire parser/dictionary runtime only
at the first selected item, including an empty string. All selected items share
one immutable catalog generation and acquisition, but reset parser/phrase state
per item. One cumulative position counter includes stopwords; only an item
emitting lexemes adds an artificial gap. No selected item yields a typed empty
vector without dictionary receipts. Traversal, lexical work and final acceptance
remain detached and cancellation-fenced; no partial vector or acquisition escapes
a failure.
Debug borrows the same coherent capture/default resolver and acquisition fence,
but independently lexizes every mapped dictionary against each original token.
Its complete ordered mapping arrays and recognized-empty/unknown distinctions
never inherit conversion's filtering, phrase or token-suppression walk.
Debug rows and acquired dictionary runtimes belong to one invocation; nothing
is published before currentness and cancellation are checked.
Provider failures inside debug acquire the actual SQL-standard function's
statement context through the shared immutable SQL routine diagnostic helper;
caller argument-input failures remain outside that body frame.

`consumeTextSearchSQL` owns the shared rewrite/statistics nested cursor scope:
ordinary settings-aware parse, complete preparation, ACL/planning and read-only
cursor admission use the caller's private catalog/data with no outer parameters.
Stat enables canonical executable-binding `readOnlySPI` classification before
description: derived/stored row marks are rejected, while unused analysis-only
locking CTEs are not executable marks. Non-cursor command-kind precedence remains
separate. Rewrite keeps its existing syntax-gate admission rather than inheriting
stat's stronger SPI demand; lexical restoration includes both demand flags.
The consumer closes rows before lexical restoration; only prepare/open failures
gain SPI diagnostic enrichment. `nestedCursor` is all-row demand, not cached SQL.
Generic folds retain immutable explicit vector/query results and their first
execution notices are not replayed. Stable omitted forms and runtime arguments
consume current defaults/mappings without local result caches. Rich lexical metadata
stays internal; only `ts_lexize` projects TEXT[] at its checked SQL boundary.
Runtime acquisition occurs only when the actual ordered token chain consults a
dictionary; invocation-local reuse avoids repeated acquisition. Empty input,
unused categories and dictionaries after recognizing nonfilters (including
stopwords) create no initialization receipts. Phrase continuation tests mapping
membership without acquiring that category's other dictionaries. Detached demand
and final result acceptance fence the captured configuration, parser and consulted
executable inputs; mismatch returns ordinary `ErrCatalogReplaced`, without retrying
callbacks or notices. Newly initialized receipts remain invocation-private until
the complete result passes currentness and cancellation checks, so rejected
conversions publish neither a vector/query prefix nor a prefix of new receipts.
Thesaurus initialization belongs to the physical session's `sessionPayload`, with
at most one successful immutable runtime per current dictionary definition.
Namespace USAGE authorizes initialization/reinitialization, not each unchanged
invocation: ACL-only revocation leaves that session's runtime usable, while new
sessions independently authorize initialization. Receipts check exact executable
inputs, catalog domain/epoch, effective role/path and logical source identity.
Changed executable inputs require new resolution/authorization and detached
compilation followed by a current-input recheck; failed refresh cannot return
stale state. Errors are never installed or transferred between sessions.
The conversion chain owns head/continuation cursors and private phrase matchers,
saved longest completion, dictionary resume positions and suffix replay. Unmapped
categories are skipped; a mapped incompatible category discards saved completion.
Continuation searches the whole next mapping and bypasses its earlier filters.
Oversized parser tokens emit their notice on first encounter, including phrase
lookahead. A per-walk token-index high-water mark prevents duplicate notices
when a saved completion replays a suffix; skipped tokens demand no dictionary.
Unsuccessful multiword continuation resumes the remaining head chain with the
original token; immediate nonrecognition retains preceding filter transformations.
Accepted all-literal thesaurus lexemes/vectors preserve their original typed
folds across normalizer and own-option revisions; fresh calls and runtime-token
invocations acquire current compiler inputs. Operand reanalysis and accepted
fold ownership remain separate, including when fresh initialization fails.
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
`DDLHost.LookupParserRoutine` likewise copies the callback qualifier into a bounded
semantic name with no expression position before exact routine selection. It
preserves the source AST and caller authority; ordinary function/expression lookup
keeps its original source position.
Parser SRFs resolve current descriptor-backed identities, including the seeded
default after rename/schema moves, and borrow only checked catalog-free callback
capabilities. Parser declarations retain namespace and represented callback
dependencies; configuration consumers retain their actual parser dependency.
Definition mutation, retirement and cancellation use the existing ordered
object/name resources and publication fences, not a separate utility path.

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

`pageinspect_capture.go` checks genuine immutable released routine identity,
current reciprocal membership and superuser authority after STRICT argument
evaluation. It does not require a live original pageinspect extension or infer
capability from a receiving extension. BRIN item decoding opens the exact
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
Routine capability comes from the sealed tablefunc installation's released
declaration, never the current extension member count or receiving extension.
Detached, renamed and foreign-member connectby routines remain genuine after
the original extension is retired. Retained typed carriers resolve current
owner/ACL through the ordinary routine validator, preserving domain/epoch,
EXECUTE admission and caller-owned record-shape fences.
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

Each selection captures operation-effective replication role under `Engine.Mu`
(then the settings mutex), filtering immutable O/D/R/A definitions by event and
TAG. With neither matching callbacks nor enabled end/drop/rewrite inventory,
ordinary Completed utility ownership remains untouched, without command scratch
or a callback journal. Enabled unmatched inventory installs collection scratch
only. If a query-table source function stages actual row writes or deletions,
the destination joins the existing catalog overlay and those effects publish
together at ordinary `Finish`, just as for a writable CTE. Collector scratch
alone does not delay ordinary utility publication or allocate that journal.
Each phase reads the operation-effective typed `event_triggers` Boolean before
capturing callbacks; off suppresses every firing mode. Collection scratch and
the command journal remain independent of that execution switch. Each enabled
phase recaptures current authority and invokes checked functions in name order
unlocked, without rechecking the Boolean between already captured callbacks.
Later phases see callback setting changes and routine-local restoration.
Prepared handles retain neither callback lists, function bodies, a cached
replica bit nor a cached event-trigger Boolean.
Exact command drop capture uses the same effective role/TAG gate; catalog-only
capture remains a conservative session-independent O/R/A superset. Exception
checkpoints restore command-local drop facts alongside catalog, row and effect
roots. Internal replacement effects do not become user-visible drops.

`PublishAlterMetadata` consumes structurally validated metadata before heap work.
Sequence replacements marked for persistence omit eager session-tail discard;
the normal sequence demand path adopts their generation. Stage/journal transfer
and rollback remain identical to other generator replacements.
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
The physical continuation receives one semantic reason mask per stored relation,
independent of native row copying. Its unlocked callback borrows the command's
target OID/reason pair and restores both on every exit. Helper evaluation reads
only that live `table_rewrite` command; prepared nodes and native dumps retain no
invocation state. Nested utilities and PL exception checkpoints preserve the
exact enclosing command and pair without restamping publication proofs.

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

`EventCommand` owns lexical tag/phase, deduplicated immutable pre-deletion
records and a separate append-only base-DDL inventory, never publication
authority. Enabled end/drop/rewrite inventory installs collection independently
of TAG matching. Unmatched inventory needs scratch but no callback journal;
matching callbacks retain the existing atomic Finish owner. Explicit successful
producer annotations transfer with admitted catalog stages, not descriptor diffs.
Repeated base commands on one address remain distinct. Generated utility
commands borrow collection; complete nested commands isolate it and restore the
exact enclosing pointer. Raw namespaces and identities resolve against the
executing private catalog, and deleted known targets are omitted.
Explicit ALTER payload capture uses the owner's immutable pre-step catalog,
roles and path, never the lazily initialized query inquiry. Stage capture keeps
its authorized role snapshot even when a statement inquiry also exists.
`CatalogStage.ReplaceRelationProjection` is the partition-index retirement
adapter: it captures against the actual entering descriptor and admitted
roles/path before checked replacement, appending private records only on
success. Both partition constraint and index commands initialize that path
before unlocked retirement; ordinary descriptor replacement never captures.
Generated-expression replacement reports the old attrdef as an explicit root;
internal default/identity replacements remain suppressed.
`sql_drop` alone exposes dropped records. DDL inspection instead requires the
active complete-query collector, including its empty start/drop/rewrite states.
Exception checkpoints clone mutable append backing and restore installed state,
phase and address index. Conversion polls cancellation before, during and after
materialization, returns no partial source, and never consumes the inventory.
The opaque ninth field owns bounded immutable command facts, not lexical state.
`ProjectedRoots` and `WriteStage.Replacing`
distinguish complete private roots from appended suffixes; `DiscardRemovedWrites`
uses the same conversion as column projection. `ActivateSQLRoutine` owns fresh
lexical admission and restores the exact outer `MaterializedSlot` once on exit.
Volatile nested commands snapshot complete projected private roots instead of
merging their removed entries with older transaction writes. Thus AFTER TRUNCATE
reads the staged empty target even when earlier commands wrote it in the same
transaction; read-only routine policies retain their calling-query frontier.

Reached static PL and SQL commands execute with their description's parameter
count (the accepted binding's count for frozen SQL-standard commands), not the
enclosing routine's formal count. Utilities therefore receive no arguments.
The original invocation frame remains intact for subsequent bound commands.


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
Construction guards participate in this same mark, with a full immutable-backed
token-value snapshot and exact pointer membership rather than a shallow map
copy. Rollback invalidates new pending guards and reactivates saved guards in
place alongside the restored private catalog; it neither commits a phase nor
weakens normal final provider validation.
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
Only relations with incoming/outgoing FK, exclusion or deferrable-unique obligations retain accepted
transitions. Row-at-a-time writers reuse relevance captured by each existing
physical write binding, including routed insertion groups; alternating destinations
do not repeat catalog discovery. Reuse requires the exact reference, parent and
immutable catalog root. Other lanes retain an allocation-free command value.
Unrelated DML does not allocate the effect queue. NOT VALID FKs still enforce new
writes, and index-only transitions retain bounded command/version ownership.
Neither command completion nor protected rollback publishes rows, rotates snapshots,
rewinds identities or owns `Finish`. DO uses that same root publication path.
Nested utility completion flags are lexical scratch, saved and restored separately
from the root's publication outcome. A successful SET helper cannot suppress the
surviving writes of its SELECT or DO caller.
Queues with no referential work advance bookkeeping without adding a cancellation
phase ahead of the root's final publication fence. Procedural diagnostic provenance
uses the actual planner-to-executor handoff, not whether eager execution returned
an error before exposing rows.

Constraint timing has one transaction-owned `index.ConstraintModes`: persistent
defaults are resolved separately from ALL and OID-keyed overrides, with one
immutable event vector for index conflicts and FK trigger checks. Index events
retain exact admitted row/version identities only after a potential conflict;
FK events retain eligible accepted row images, original references, timing and
actor even when currently valid. Their payloads contain no suspended command,
bound job or execution frame. Savepoints retain modes/events and the incumbent
dirty index projection together; append detaches the saved vector once.
Batch INSERT, row-at-a-time insertion and replacements all stage the physical
identity reserved by that same projection. Definitions requiring a reservation
must supply it; allocating a second identity would orphan the pending event and
make later deletion or replacement unable to retire its exact incarnation.
Command completion checks due indexes, executes immediate RI actions, checks
action-generated indexes, then enlists/checks due FK work before AFTER bodies.
Nested completion consumes only its accepted command's events.
SET checks selected pending work before installing candidate modes. Top-level
and nested utility dispatch share the same executor; description is effect-free.
Actual routine activation retains timing even in an implicit CALL/DO transaction,
without an outside-block warning. Ordinary nested return preserves modes/events.
SQL/Go COMMIT, CHAIN, PL completion and PREPARE consume the same obligations
before publication or transfer; failure clears the old incarnation and grants.
Procedural COMMIT/ROLLBACK, including CHAIN, drops the old scope index slot after
releasing its scratch, so reattachment uses fresh definition defaults rather
than copying old overrides or events into the new transaction incarnation.
`HasPendingConstraintEvents` reads the effective scope-or-owner vector without
allocation, locks, liveness probes or consumption. Every queued index/FK event
protects only its transition relation (`Parent`), even after row repair or
supersession, until ordinary completion consumes it. Uncontested index admission
and FK-ineligible transitions do not manufacture events. Common ALTER and recursive
default admission use this read-only query before candidate work; rename/schema
dispatch stays separate.
Stable FK crosschecks use supporting-root pointers captured with the original
owner snapshot. A retained parent match after a current miss is the
isolation-specific serialization error, not permission to publish a dangling
reference. Provider comparisons and transaction waits run outside engine,
owner and session locks and replacement reservations.

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

`AdmitDatabase` resolves a trusted principal and exact selector under the shared
metadata mutex, checks allow/CONNECT/database-limit in order, and reserves one
physical slot before startup callbacks. Its pointer-owned `DatabaseAdmission`
retains the target, principal and coherent role/default inputs; copies, foreign
targets and repeated submissions confer no authority. `StartSession` consumes
that token once. Caller `Release` abandons only an unsubmitted token; after
submission, early failure or terminal Unregister owns release. Private maintenance
and `StartRestoreSession` replay use real cleanup without a public database slot.
Role limits remain a separate cluster-wide final-publication check.

Persisted defaults share the immutable role publication and its existing
transaction journal. Scope keys use principal references and cluster database
identities, never names or local replacement epochs. Settings-only writes retain
graph authority lineage but still participate in complete-record three-way
merge, savepoints and final publication. Scope writers use cluster lock authority.
ALTER DATABASE and ALTER ROLE ALL IN DATABASE share the actual database-address,
zero-role row. Their owner-dependent mutations retain a database predecessor
fence through final commit; scope-lock admission never retains stale authority.
Catalog pairs retain the registry's PostgreSQL display spelling (for example
`TimeZone`); row replacement/reset and startup precedence use folded keys.
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
Encoding 6, provider `c`, C/C locales and `datistemplate=false` are represented
immutable defaults. `datallowconn` and `datconnlimit` project real CREATE policy
(defaults true/−1), retained across replacement. Physical placement and multixact
horizons, ICU locale/rules and collation versions remain typed NULL.
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

`statementHost.SizeInquiry` uses the existing catalog-inquiry capability.
It fences the retained catalog's owner and replacement epoch, captures typed
allocation roots from the current matching private catalog and transaction
overlays under the shared cluster mutex, and counts outside that lock.
Database/tablespace admission captures current invoker authority, resolves
existence before CONNECT/CREATE or inherited statistics privileges, and selects
registered target runtimes rather than treating the attached store as the cluster.
The body borrows `RoutineAuthorizationLocked`, never rule relation-write
authority: effective/restricted invocation owners and legitimate RI precedence
remain the routine admission owner's responsibility.
It retains immutable vectors, not mutable heap headers or prepared numeric/ACL
results. The allocation model belongs to
[`storage`](../storage/README.md#logical-allocation-estimates).

`statementHost.FormatType` consumes the existing immutable visibility catalog
and authorized relation/type-compatible path, including the session's TEMP
namespace. `BaseType` needs only that catalog. Both are captured at the execution
frontier with schema USAGE filtering and normal implicit/explicit pg_catalog
precedence. The TEMP-filtered conversion/collation path keeps independent backing
and is not used for type formatting. Expression snapshot demand and retained
DEFAULT/generation/CHECK/view recipes select the capture; evaluation never rereads
session settings. COPY WHERE forwards its local binder's demand after target and
used-column authorization, before predicate preflight. Retained write demand adds
to that capture without replacing or clearing it. REGTYPE results use ordinary
retained row-output spelling, independently of formatter TEXT.

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

Cluster `AdmissionMu` protects only reservation state and per-target counts.
Admission takes metadata Mu then AdmissionMu; claim/release take AdmissionMu alone
and drop it before startup or cleanup. Terminal Unregister runs with Session.Mu,
removes the exact local session under sessionMu, releases sessionMu, then releases
its reservation under AdmissionMu. It never acquires metadata Mu. No path from
AdmissionMu enters metadata, session, settings or local-registry locks.

Workload-credit policies attach inertly to caller contexts. Known-unlocked
preflight and runtime owners activate only local evaluation/pull contexts, never
the operation, command/source context or `scope.executionContext`. Before each
application admission, the runtime uses the external-work reservation handoff;
row waits may have reacquired that reservation since startup. After a grant,
ordinary live-operation, immutable transaction-reference and catalog-epoch fences
must still accept. Refusals keep their original error identity. Local parallel
workers never mutate the statement's reservation; audited worker source/expression
gates exclude lock/host-mutation paths and the leader joins before continuing.
SQL cursor and protocol demand use the current request's context, and retained
executor evaluations clear their scoped contexts between pulls. Full-text vector
conversion receives the actual evaluator context through the host interface;
only catalog capture is locked, never token dispatch or the admission callback.
Builtin vector triggers activate their own local normalization context after
metadata admission. They use the effective invocation context, not the operation's
inert source context, and retain the cumulative position across separate fields.
Each protocol Execute activates its pull context once and reuses it for that
request's row loop; later Execute requests obtain their own current capability.

## Row mutation

MultiXact records, counters and history belong to the lock manager. CHECK and
branch admission precede the physical event; index and RETURNING evaluation
follow it. Shared mutations journal successor/deletion events while private
roots are locked and flush after the engine/owner unlock. No `Engine.Mu` →
manager-mutex acquisition exists. Finish discards private stages on failure.
Each DML activation captures the current owner-private trigger list, not a
list cached by its prepared handle. Each BEFORE trigger reads operation-effective
replication role separately, before UPDATE-column/WHEN and routine admission:
earlier bodies in the same row chain can change it. AFTER ROW eligibility is
captured before WHEN/tuple ownership; draining never rechecks a queued mode/role.
Disabled statement candidates become skipped registrations, not fired events. VACUUM is
[`tablecmds`](../tablecmds/README.md#vacuum)'s.

Immediate BEFORE STATEMENT events use the admitted logical relation lifecycle
grant (RowExclusive for DML, AccessExclusive for TRUNCATE), including views,
partitions and foreign targets, rather than row replacement authority. Their
lazily allocated relation/event fired set belongs to the same
effect-command frame as AFTER progress and is marked before mode, UPDATE OF or
WHEN filtering. Lexical CTE scratch swaps cannot replace it; reached routine SQL
owns a distinct frame. Checkpoints clone its mutable map, and Finish retires it.
Main modifiers start before demand on auxiliary CTE sources. Statement frames
have absent OLD/NEW images; PL composite results do not alter rows or counts.
The shared live-executable invocation rejects successful non-NULL native tuple
results with 39P01, preserving original helper errors.
TRUNCATE admits its complete manifest, then runs every BEFORE chain unlocked
before capturing any replacement frontier or restarting sequences. It recaptures
and revalidates authority/storage afterward; its manifest, not DML query dedup,
owns selected-relation uniqueness. AFTER remains all-post-effects.

Query trigger state belongs to `StatementScope`, keyed by monotonic effect-command
identity rather than swapped DML scratch. Writable CTEs share their query frame;
routine SQL completes its own frame synchronously. Accepted ROW WHEN predicates
run unlocked on final images; queued bodies run at command completion after
immediate obligations and before publication, with rows preceding statements.
Statement registration is bookkeeping only and independent of row count. Mode
eligibility, then WHEN, qualifies each producer's candidates at its successful
exhaustion, after row production but before immediate RI work and row-body
draining; firing reevaluates neither decision. A repeated
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
  An installed hstore/lquery/ltree/ltxtquery/ISN owner/ACL or membership-only
  invalidation in the same environment may carry its nominal cast identity into
  a single candidate analysis. Executable analysis is still invalidated; current
  lookup, authority and coercion run before fresh captures publish. A failed
  candidate leaves the original casts retired. No alias repairs obsolete source
  spelling.
  Direct held hstore/lquery/ltree/ltxtquery/ISN literals use candidate-only datum
  reownership from the exact captured analysis descriptor to the genuine current
  released type, under the same environment and definition gates. The shared
  scalar admission authenticates the captured/current sealed origin and released
  role, current mapped type/principal and actual input/output declarations,
  independently of mutable e-links and the original header's lifetime. Only
  Owner/ACL/Extension fields are normalized for nominal definition comparison;
  domain/epoch/dependency-purpose and all other definition fences remain.
  `TestPreparedInstalledScalarMembership` and
  `TestReferenceExtensionOwnershipScalarLiteralCaptures` distinguish real held
  captures before detach/foreign ADD/origin retirement from unchanged-pointer
  cases using genuine type-renaming generations. A local pointer
  import preserves immutable payloads and NULL framing without weakening Retag
  or catalog import roots. Identity-only attempts leave original temporals
  intact; successful reanalysis publishes fresh temporals and analysis together.
  On genuine invalidation after hstore/lquery/ltree/ltxtquery retirement, an authentic capture whose
  original type address is absent is omitted only from the attempt candidate.
  Normal binding reads the original SQL literal under the new genuine usage;
  neither a reused label nor a numeric OID authorizes importing retired data.
  Direct ltree/lquery literal retirement also permits changed namespace/path
  resolution, including sticky cast invalidation from a failed attempt, but
  retains exact catalog domain/epoch, session, private journal and
  discard-generation fences, original declaration pointer, capture TypeID and
  dependency provenance. Only an absent original
  address permits this candidate-only omission. The ordinary binder must resolve
  the original SQL and check current namespace/provider authority; this route
  grants neither datum import nor reuse of the old analysis. Failed reanalysis
  leaves accepted literal captures intact and can retry under fresh authority.
  Direct concrete ltree/lquery companions extend this candidate path only with
  separately captured scalar and array declarations/dependencies. Exact source
  association and the current validated installation authorize recursive element
  reminting, including typed NULLs, while sharing compiled backing. Shape-only
  cast eligibility grants no authority; absent-address retirement does not import
  old payloads into a recreated installation.
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
  Successful SQL/protocol publication records the admitted command-start clock
  and origin on this same definition; rebind never replaces provenance.
  `pg_prepared_statements` captures only the issuing token's named definitions,
  after releasing `Engine.Mu` under physical-session admission. Its synchronous
  visitor borrows signatures, not plans; detached rows own their regtype buffers
  and current catalog descriptors. Membership alone determines visibility, so
  native catalog replacement/pool reuse retain rows and physical end removes them.
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
Core supplies shared compiled English, Arabic, Armenian, Basque, Danish, Dutch and Estonian Snowball runtimes to the catalog's
private seed installer. Allocated helper/template/dictionary/configuration
identities use the same admission, capture and immutable fold owners as simple.
`Engine.TextSearchFiles` is immutable Driver startup authority, not catalog state.
DDL capture retains that same capability before releasing `Engine.Mu` for
compilation. Logical databases, the private template seed, Restore stages and
Reset retain it explicitly; destination Restore never borrows source bytes.

## Read-only utility execution

- ALTER SYSTEM describes an empty signature without snapshots or authority.
  Common direct and nested execution checks block context (including actual
  internal exception marks) before routine context, then refuses auto-configuration with
  `0A000`. It is strictly read-only for database admission, not a session setting,
  and never resolves values, takes file locks or performs file I/O.
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
discovery. Checked standard-body/default/aggregate/operator providers and genuine
type I/O/support procedures hold real procedure AS through transaction end;
grant-time identity checks never retarget a dropped provider. Type codec
identities use the same authenticated metadata visitor as dependency projection
and retirement. Type and definition-source reads join ordinary stage/journal
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
  Foreign metadata sources, including `foreign_tables`, carry that same pair
  with the current database name and temporary-owner authority. Shared runtime
  validation checks the exact source/ref and current principal before producing
  rows, even for an empty catalog.
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
  `Source.CatalogKeys`, so oid equality filters visit only matching rows. A
  statement whose filter yields a `pgcatalog.Keep` (`pg_proc.oid`) uses a hit
  as is; on a miss it builds only the kept rows and stores nothing.
- `EngineCatalogRuntime` exposes values per call, never Engine, StatementScope,
  AuthorizationView or bindings. Settings rows capture one coherent
  physical-session snapshot after unlocking `Engine.Mu`; `pg_locks_capture.go`
  keeps an unlock/capture/relock epoch fence.
  Routine-default presentation validates the paired capture and exact Procedure,
  then renders checked expressions or declaration-owned finite literals through
  `stored.Deparser` with captured path/output/string settings. Finite source
  literals include the logical-decoding TEXT[] default, decoded by its captured
  array codec. Source literals retain their type and spelling across validated
  assignment coercion; no expression evaluation, provider binding or
  replay-renderer change occurs.
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
  `pg_policies` uses the same paired inquiry roots plus captured path, output and
  string settings. Its producer and `PolicyDisplay` bridge run after releasing
  `Engine.Mu`; the bridge validates the genuine stored carrier before rendering.
  Ordinary view SELECT admission (PUBLIC by default) protects access, while
  target privileges affect qualification rather than row inclusion. Role arrays
  are sorted distinct NAME[]; absent USING/CHECK remain independent NULLs.
  Each execution recaptures authority; detached rows retain their earlier values.
  `TestCatalogSourcePGPolicies` covers actual dispatch, unlocked output,
  caller qualification and rejected capture without condition execution.

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
- Replacement remaps installed origin, immutable canonical declarations and
  current membership through one descriptor/procedure remap carrier. Captured
  origin cohorts and deleted recipe addresses reserve logical identity capacity
  even without live descriptors, so retained peer TEMP objects cannot collide
  with a detached installation's provenance. Canonical snapshots never grant
  current owner/ACL or descriptor authority and contain no physical handles.
- Ordinary stages, private transaction commit and wholesale replacement all
  validate reciprocal extension membership through the common catalog accessor;
  provider-specific validation cannot silently omit an eligible object class.
- Staged type identity changes (`type_generation.go`) authorize generation
  reownership only from the exact published source descriptor to the changed
  catalog-owned scalar, with unchanged extension and companion identity.
  Lquery, ltree and ltxtquery use this transient import authority for stored
  values and expressions, recursively reminting concrete companion elements;
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
  Detached relation-map candidates own only their new or replacement heaps.
  After all publication fences succeed, the locked install reconciles unchanged
  heaps with the current storage map, just as it preserves unchanged index roots.
  A peer TRUNCATE can replace a heap without changing the catalog root; neither
  that heap nor subsequent committed inserts may be restored from preparation.
  This also applies to physical-session retirement: the transaction resource
  cleans TEMP tables while retaining their namespace pair, then runner
  `EndMaintenance` calls `endTemporaryCollations`. Its namespace/dependency
  candidate uses the ordinary detached publication path, even for an empty
  table-only namespace. Cleanup keeps the same fences and publication semantics.
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

`session_activity_runtime.go` reads the current operation's original frontend SQL
and physical session token. `DatabaseCluster` owns immutable microsecond startup
and configuration-load stamps, captured at cluster construction and actual initial
settings-registry load. Logical database adoption shares them; native catalog
replacement never imports staging clocks or changes destination runtime identity.
These runtime reads allocate no per-cell strings, snapshots or catalog captures.

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
- Aggregate frame-backed SQL/PL/host support enters `InvokeAggregateRoutine` with its explicit
  worker context. A cancellable, statement-owned gate in `sql_routine.go`
  protects routine depth, lexical scratch, descriptions and notices while
  independent aggregate scans and builtin state work remain concurrent.
  `statement_scope_state.go` owns the gate and temporary invocation context;
  `sql_routine_scope.go` preserves the existing lexical activation/restoration.
  Nested binding, execution, coercion, result reads and relation waits use that
  context without replacing `Operation.Context`, acquiring another transaction
  admission lease, starting a root command or calling `Finish`. Context scratch
  restores before the gate releases, including cancellation and failure.
  `parallel_presentation.go` closes outside-gate worker evaluation before any
  support invocation: immutable catalog/role/path and input facts are shared,
  but leader and each worker own separate lazy spelling caches and diagnostic
  queues. All reg*/ACL formatters bind to that owner, never StatementScope.
  Resolver observation sinks are detached and XML/FTS/type-name/ACL-input notices
  flush once after join, including error/cancellation, without cross-worker
  ordering. ACL input retains its immutable roles, exact ACLAuthority and copied
  literal/parameter provenance, but not direct Operation notice access.
  Callback audit: class/role/object resolvers share only captured read authority;
  type/operator/routine/text-search observer pointers are cleared. Type naming
  and enum safety retain immutable catalog/path/provenance closures. Binder-only
  LockedDomainInput and live DomainChecks are removed, and Output.XMLNotice is
  rebound to the captured XML adapter. Locale/encoding/time facts have no live
  operation callbacks; every catalog output formatter uses the private owner.
  Missing captured capabilities stay missing. Nested SQL bodies still use the
  gate-owned live lexical scope, original WriteStage and command journal; actual
  descriptor/EXECUTE checks remain live. Unwind restores the complete lexical
  bundle before releasing context/gate. Parallel bodies cannot change persistent
  settings, so their saved scratch needs no redundant environment recapture;
  ordinary serial setting recapture remains intact. Parallel cancellation or
  failed environment recapture restores caller codecs, callbacks, lookup-path/
  cache ownership and row-security policy with identity/authority. Actual
  parallel membership, not the lexical invocation marker, enables the operation
  and invocation cancellation fences; ordinary canceled trigger leave still
  recaptures its coherently demanded caller inputs.
  A separate synchronous SQL-invocation context prevents a support body's
  nonterminal SELECT from spawning workers that inherit the gate token. It does
  not mark serial support as a parallel operation or forbid legal serial effects.
- Parallel-operation context covers leader support work as well as workers.
  `parallelExecutionEligibility` reuses `session.ModifyingTag` to reject reached
  modifying commands with `25000` before nested command admission/effects and
  central execution dispatch. This includes modifying CTEs, nested support calls
  and TEMP writes; a falsely SAFE declaration does not grant write permission.
  Ordinary serial support commands retain their existing behavior. Rejection
  keeps the incumbent reached SQL routine statement context.
- Aggregate seeds first capture demanded type diagnostics/ACL authority in
  `schemacmds.RoutineHost.ValidateAggregateInitial`. Ordinary seeds retain
  `InputWithEnvironment`; domain seeds enter `BindingHost.DecodeDomainLiteral`
  and the existing `DecodeLockedDomainInput` bridge. CHECK evaluation runs
  outside `Engine.Mu` against captured input/providers; context, caller authority,
  catalog epoch and provider identity are readmitted before publication.

## Server protocol and timeouts

The wire adapter ([`cmd/stuntgres`](../../../cmd/stuntgres/README.md)) reuses the
session, transaction, prepared-analysis and typed-frame authorities.

- `session.ProtocolCycle` retains implicit transaction work, not an operation or
  admission lock; each message acquires its own command context. Wire portals
  share names with SQL cursors but own their position and lifetime.
- `ExecuteProtocolSimple` streams plain SELECT from the live producer; other
  row-returning commands fill a row store first, so an inner failure emits no
  partial rows. Stored typed rows are never rewritten or replayed.
  Retained rows are encoded and delivered before `Finish`, so output conversion
  failure rolls back commands such as INSERT RETURNING rather than committing
  before the wire error. The wire captures the failing active pair before
  unwind and reports restored settings afterward.
- `client_encoding.go` resolves both actual default conversions using the
  ordinary conversion search path, lowest-OID tie-break and immutable handler
  signature validation. Explicit SET/startup candidates publish cache authority
  only after ordinary setting/lifecycle admission; restoration performs no
  lookup. Persisted and routine declaration candidates use the same bidirectional
  resolver without installing live/reset/cache state; removing either configuration
  value needs no pair.
  `copy_encoding.go` reuses admitted outgoing authority for COPY TO selecting
  the active encoding, including explicit ENCODING. Other output encodings and
  every COPY FROM resolve their directional file capability afresh.
- `CopyProtocolHooks` run outside engine/publication locks and admission; wire
  COPY FROM holds a cancellation owner through decoding, released on every exit.
  `ProtocolNotices` uses the same active owner reservation before Finish:
  fallible conversion and unlocked socket delivery precede publication, followed
  by exact owner/lifecycle/catalog readmission. Idle control warnings borrow the
  idle owner without starting a transaction.
- `copy_progress.go` registers a real admitted COPY only after relation/query
  preflight and reservation, before external stream hooks. The cluster owns one
  entry per backend under the shared `Engine.Mu`; database reset/replacement does
  not clear another database's active work. `StatementScope.Finish` alone owns
  exact-entry cleanup after row-close, queued AFTER work and terminal foreign
  modification callbacks, before transaction publication. Its defer covers early
  exits, cancellation and panic; stale cleanup cannot remove a newer entry.
  Map cleanup alone is not failed-command disposal. If a public COPY's terminal
  `EndForeignInsert` callback panics inside Finish, its request boundary preserves
  the original panic and resumes the same scope's failed Finish before rethrow.
  The modification marks itself ended before provider entry; admission programs
  and queued AFTER authority were cleared by the first unwind. Failure completion
  skips command effects/publication and uses existing foreign rollback, owner,
  view/stage/lock, reservation and admission disposal, without callback replay or
  a second teardown authority. `Operation.Done` then releases its lease normally.
  Open transactions do not retain rows. Initial relation waits publish no entry.
  Command-owned atomic counters permit updates from already-locked DML and
  unlocked I/O without recursively acquiring `Engine.Mu`. Capture under that
  mutex loads detached facts and fresh effective-observer role closure against
  the worker's immutable authenticated principal; no entry/counter pointer escapes.
  Reader bytes are raw pre-conversion `n>0`, including n+EOF/error; writer bytes
  are accepted encoded payload `n>0`, including partial failure, never envelopes.
  Successful whole-row writes own output tuple completion, not headers/trailers.
  Plain/no-index validated batch adoption and actual trigger producer admission
  count individually; unique/routed COPY waits for genuine existing buffer flush
  admissions. Later application/publication never counts again. Soft conversion
  skips precede reject-limit checking; WHERE false/NULL alone is exclusion.
  Foreign mutation still waits for EOF/FinishInput; accepted returned results
  count during real provider work without changing trigger-derived batch policy.
  Counts are monotonic completed work, not committed rows or a forced final result.
- Session checks observe due transaction/idle budgets under `Session.Mu` before
  publishing a lease, with one timestamp for both slots. A statement timeout
  before `StartStatement` fails its explicit block synchronously through
  `StatementScope.Finish`'s disposal; no timer callback mutates the transaction.

Installed and fixed native DML trigger bodies use one balanced entry for
routine depth, trigger depth, effective-user/configuration and command policy.
The insert_username body borrows producer NEW, resolves its literal target
against the admitted relation on each firing, requires exact declared TEXT and
changes only that cell. Current-user capture occurs inside the routine
environment; no PL frame, target-slot cache or retained borrowed row is created.

## Testing

Publication descriptor/filter/membership/capture ownership units use
`make check RUN='^TestPublication'`; public SQL scenarios remain in `reference/`.

Aggregate SQL invocation ownership uses
`make check RUN='^(TestAggregateParallelInvocationOwnership|TestAggregateCombineSQLGate)$'`;
add `RACE=1` for the same real-worker regression. Its context observer parks
inside the actual SQL sleep provider before the large nested physical scan,
avoiding repeated stack inspection in that scan's per-row cancellation checks.
The complete provider/support/partition stack witness still precedes sibling
environment/presentation access. Failure cleanup cancels and releases the
observer before joining with an independent bounded five-second interval, even
when the execution deadline has already expired.
The second actual worker also initializes a real declared/bound ACL-state
aggregate outside the gate, using six paired literal43/parameter2 seed decodes.
It checks exact authority/PUBLIC/default grantor/read mask, an empty live
Operation notice list before join, all30 XML/FTS/type/ACL diagnostics afterward,
and no second-drain delivery across success, SQL error and cancellation.
The definer cancellation matrix distinguishes a canceled original request from
a canceled worker invocation whose original operation remains live; both assert
exact fallback ownership, while only the former requires fresh-operation
recovery on the same session.
Retain `TestRoutineTriggerAttributeEnvironment` and
`TestRoutineEventTriggerAttributeEnvironment` alongside that matrix: their
ordinary canceled entry/exit contract requires live coherent caller captures,
not restoration of a sparse pre-Execute Input.

Replication-origin allocation, stage/journal/exception ownership, immutable
capture and replacement units use `make check RUN='^(TestReplicationOrigin|TestPGReplicationOrigin)'`;
add `RACE=1` for the same ownership witnesses. Public scenarios remain in
`reference/catalogs`, selected by `^TestReferenceReplicationOrigin`.

Shared parameter authority backing, admission, publication and replacement
units use `make check RUN='^TestParameterACLShared'`; add `RACE=1` for the same
deterministic ownership/concurrency witnesses. SQL acceptance remains in the
owning dual-mode reference packages.
Prepared metadata identity, immutable buffers and token lifetime units use
`RUN='^TestPGPreparedStatements'`; public SQL and wire scenarios use
`RUN='^TestReferencePGPreparedStatements'`.


Text-search parser descriptor, staging, native capability and dependency owner
units share `RUN='^TestTSParser'`.
Genuine parser membership, supplied-view/callback authority, COW snapshots,
lower relocation atomicity, shared callback/configuration remap and strict
ordinary v3 replay use
`make check RUN='^TestExtensionOwnershipIdentity$$/^parser_(membership_authority|remap)$$'`.

Runtime work-policy handoff and terminal-owner mechanics use
`RUN='^TestWorkAdmission'`; real SQL waits and operation lifecycle use reference.

Physical database reservation, provenance and terminal cleanup units share
`RUN='^TestDatabaseAdmission'` (also with `RACE=1` for concurrency witnesses).
Held cursor store/producer ownership and replacement units share
`RUN='^TestSQLCursorHold'`; public SQL/control observations stay in reference.

Session activity command/portal authority units use `RUN='^TestSessionActivity'`.
Live trigger/RI invocation ownership and unwind units use `RUN='^TestTriggerDepth'`.
Immediate trigger ownership units use `RUN='^TestTrigger(Core|Instead|BeforeStatement)'`.
Builtin admission, borrowed payloads, streaming positions and unwind units share
`RUN='^TestTriggerBuiltins'`; public SQL/control witnesses live in `reference/operators`.
Native tcn payload quoting and shared-entry unwind units use `RUN='^TestTcn'`;
provider-authority units share that family in `schemacmds`, and public SQL,
notification ordering and controls belong to `reference/sessions`.
Installed autoinc tuple ownership, sequence effects and frame-unwind units use
`RUN='^TestAutoincTriggerFrames$'`.
Installed trigger authority, borrowed-row and replay units use
`RUN='^TestInsertUsername'`; public SQL/control cases share
`RUN='^TestReferenceInsertUsername'` in reference/extensions.
Logical admission witnesses use distinct stored leaves: return-only invocation
retains view RX without leaf replacement/row-write grants, while nested DML
observes its own storage and grants inside the physical validation phase.

Scalar hierarchy and positional-match accepted definition/fold backing and
rejected-publication fences use `RUN='^TestLtree(Hierarchy|Lquery)Prepared'`.
Path accepted-definition ownership uses `RUN='^TestLtreePathPrepared'`, retaining
every scalar-LCA occurrence and exact installed fold backing through rejection.
Mixed scalar label-query definition/fold ownership uses
`RUN='^TestLtreeLtxtqueryPrepared'`.

Trigger-mode candidate, staged cancellation, prepared publication, clone/FK and
replay ownership units share `RUN='^TestTriggerMode'`, also with `RACE=1`.
They inspect installed immutable definitions, real journals and accepted folds;
SQL values, diagnostics and native-control effects remain in `reference/operators`.
Retained enforcement CHECK preparation and manifest ownership use
`RUN='^TestConstraintEnforcementCheckProgram$'`.

Other planner-option admission, actual collection/publication, branch selection,
cursor-source retention and scoped recursive estimates use `RUN='^TestPlannerOther'`.
Composed witnesses inspect the real admitted owners; public SQL bags and
diagnostics remain in `reference/session_state`.

Type inquiry retained-definition, stored and concurrent ownership units use
`RUN='^TestTypeDescribe(PreparedOwnership|StoredOwnership|ConcurrentReuse)$'`,
also with `RACE=1`. The composed cases inspect real accepted bindings, private
producer frames and immutable fold generations rather than SQL equality.

XML retained-description and accepted-binding generation/backing proofs use
`RUN='^TestXMLSQLPrepared(Portal)?Ownership$'`, also with `RACE=1`.

Type-path backing and COPY predicate/retained-recipe demand units use
`RUN='^TestTypeDescribe(CapturedTypePath|CopySnapshotDemand|RetainedSnapshotDemand)$'`.
These inspect the real authorized capture and preflight boundary; public SQL
TEMP qualification and COPY rows/errors remain in the reference definitions.

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
All four genuine connectby overloads' membership/origin separation, retained
typed carriers, current owner/ACL and shared sealed remap are covered by
`make check RUN='^TestExtensionOwnershipIdentity$$/^connectby_'`.
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

Logical allocation binding, provider reconstruction, private-root and replacement
fences share `RUN='^TestRelationSize'`; public SQL belongs to
`reference/catalog_privileges` under `RUN='^TestReferenceRelationSize'`.

Trigger payload/frame/runtime/remap/stage units share
`RUN='^TestTriggerCore(Catalog|Frame|Return|Runtime|Persistence|AtomicStage|PartitionPath|WhenProvider)$'`.

AFTER query-owner, detached-image, definition-kind and cancellation invariants
share `RUN='^TestTriggerAfter'`; test-only accessors expose private frame progress.

Event-trigger journal, frame, projection, drop and rewrite units share
`RUN='^TestEventTrigger'`; public direct/prepared behavior belongs in the
dual-mode reference suite.

Routine invocation, CALL projection and retained-fold ownership units use
`RUN='^Test(SQLRoutine|PLpgSQLProcedure|FDWSQLValidator)'`, also with `RACE=1`.
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

JSON conversion ownership and cancellation use `RUN='^TestFTSJSON'`, also with
`RACE=1`; these inspect accepted backing and private acquisition publication.

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
make check RUN='^TestDatabaseSettings'                 # database defaults publication, cancellation and startup ownership
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
make check RUN='^Test(Dictionary|Thesaurus)'           # dictionary demand, providers, publication, remap and retained typed ownership
make check RUN='^TestTemplate'                        # exact callbacks, capability, manifest, closure, publication and remap
make check RUN='^TestSnowball'                        # language seed authority, publication fences and accepted backing
make check RUN='^TestIspell'                          # dictionary resource propagation, fences and retained multi-lexeme owners
make check RUN='^TestLqueryProvider'                   # installed scalar authority, staged publication and import
make check RUN='^Test(Protocol|SessionTimeout|StatementTimeoutAdmission)'  # protocol, timeouts
make check RUN='^TestExecutionPause'                  # detached operation cancellation, release and binding isolation; also RACE=1
make check RUN='^Test(CatalogSource|InfoUserMappingsProducer|PGSettingsCatalogACL)'  # catalog source capture and producers; one catalog: ^TestPG<Name>
make check RUN='^TestTransactionalSchema(Admission|PrivateProviders|Publication|DropManifest)$'  # utility policy and private catalog stages
```
