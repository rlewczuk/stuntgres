# internal/engine/core

Core is the engine's runtime: `Engine` state and `Engine.Mu`, the catalog store
(`catalog.Store`, see [its README](../catalog/README.md)), statement scopes and
admission, lock waits, `CatalogStage`, publication and replacement, the
transaction lifecycle, sessions and TEMP, the hosts and binder construction,
protocol, prepared SQL, cursors, the bound DML and query path and the
procedural runtime ([`docs/ENGINE-SPLIT.md`](../../../docs/ENGINE-SPLIT.md) §3,
L15). It imports the layered subpackages and never the root or a family; the
family statement executors (the `*cmds` packages) call its exported API.

Backend-memory capture borrows only the admitted operation's physical session,
after view SELECT/backing EXECUTE or ordinary callable routine admission.
Session validation and registry traversal run without `Engine.Mu`; physical
statement admission serializes installed definitions. Detached fixed snapshots
hold own-layout sizes of the actual combined session allocation, prepared registry
and distinct definition objects, never owner pointers or referenced graphs.
The view's `pgcatalog.Input` and the invocation-time `SessionInquiryRuntime`
bridge share typed materialization; no live measurement enters Dump/Restore.
Private bootstrap completes only the view's existing nominal row/array pair
(12144/12143), after source installation and before seed validation. Exact
catalog reciprocity and composite-kind checks precede descriptor copies that
preserve builtin provenance and current-generation members. Reset/Restore and
independent destination engines normally build their own pair; live destination
TEMP consumers instead retain the exact pair and actual member/peer closure.
Candidate-local retention evidence flows into importing reownership, which
validates that closure without reminting or retagging TEMP values. Old eager
values keep immutable descriptors. The callable SRF retains its RECORD shape.
Staged/imported type reownership completes this builtin pair coherently through
the shared typedef member traversal, preserving its effective builtin namespace,
catalog metadata and legitimate relation mapping without user-type serialization.

Detached ordinary catalog publication owns only its staged heap replacements
and removals. At final installation under `Engine.Mu`, it refreshes borrowed
heaps from the current committed map without mutating captured maps or retained
row vectors. Materialized replacement birth normalization detaches only its
version metadata vector during preparation, reusing already-owned publication
vectors and sharing immutable tuple payloads. Savepoint and journal captures
keep their private birth metadata after commit. Physical-session TEMP collation
maintenance uses this same path: retiring one owner's objects must preserve
unrelated committed heap replacements and peer TEMP objects. Fallible preparation
and expression evaluation stay outside the installation lock.
Codec-only heap transports retain the modeled physical VM. Their actual copy
provenance and captured predecessor VM root cross stage/journal/savepoint
boundaries into final publication, so a stale preserving copy cannot erase a
later physical truncation. This is not a blanket VM revision conflict:
metadata-only same-pointer commits remain legal after truncate, fresh destructive
heaps own a new model, and publication retains a private replacement's own VM.
Generated restore's TEMP preservation grafts the current live TEMP heap pointers
under `Engine.Mu`; it does not publish an earlier copied TEMP VM snapshot.
The shared routine-cardinality classifier recognizes the genuine visibility
provider's set declarations before strict scalar NULL folding, including nominal
TID checks. A strict NULL check stays a source invocation, not a scalar NULL row.
Preserving VM observations are fenced at both actual materializing publication
windows, including final readmission after unlocked work. Ordinary private
statement adoption and PREPARE only carry that immutable observation forward;
COMMIT and COMMIT PREPARED check it before physical installation.
Distinguishing native witnesses use
`RUN='^TestPgvisibilityCheck(PreparedPublication|DetachedPublication|GeneratedTempGraft)$'`:
actual prepared journal transfer/materialization, index-merge and real foreign
settlement reentry, and demanded generated repair followed by current live-TEMP
pointer graft. Their mutation occurs at the named ownership boundary, not merely
before COMMIT or on a synthetic TEMP heap.

MERGE latest-row admission preserves transaction-local replacements and the
retained snapshot frontier. A later committed target update or deletion raises
serialization failure through the shared visibility conflict check; ordinary
statement failure and savepoint recovery own the private-write rollback boundary.

Owner disposal retains only genuine created private tuple versions and their
actual predecessor chains in canonical relation storage. Admission/owner locks
precede `Engine.Mu`; retention invokes no callbacks or lock-manager operations.
Failed statement stages, full abort, savepoint/exception rewind, physical session
end and prepared abort all pass through these boundaries. PREPARE transfers the
same immutable roots without aborting them; restored/current versions remain
owned, not duplicated as debris.
Heap replacement captures otherwise-lost versions against their exact predecessor
in `WriteStage.DiscardedHeaps` and then the validated catalog journal. This keeps
surviving storage-generation fences intact across repeated refresh, savepoints
and PREPARE. Final disposal materializes debris only in a still-installed exact
heap; destructive replacement retires inaccessible slots without rebinding by
address. Canonical cohort capture includes deferred batches, and exact repair
headers remain independent of owner rollback. Old roots and payload backing
remain immutable; private-only relation definitions are never installed by
retention.

RI row capture and post-grant refresh borrow canonical `HeapHeaderRoot` under
`Engine.Mu` together with committed/end/index roots. Private shadows retain
genuine exact-version/stamp history, not a RowID-wide replacement. Stable RI
captures borrow the same captured current header authority over retained
version/end vectors; captured old roots and tuple payloads remain immutable.
Restore's FK scan uses the same locked capture boundary.

Tuple admission borrows the actual captured physical version. The immutable
header's inherited repair-anchor pair selects its manager resource through
`exec.TupleResource`; ordinary grants retain their RowID key and allocation path.
Capture and after-wait readmission run under `Engine.Mu`, but manager waits and
grant inspection never do. Replacement/deletion acceptance compares the same
admitted key under Owner → Engine lock order, readmitting changed keys unlocked
before retrying the acceptance window. Queued physical events carry their
actually admitted resource, not a later header-derived key. Old ticket/queue,
relation and advisory ownership survive target-local repair.
Immediate `PhysicalRowLock` publication uses only that existing admitted grant,
without discovering/reacquiring a newer header key. Updating events originate
only at actual replacement/deletion acceptance; a rejected proposal never adds
a successor0 updater to a physical version already updated by another owner.
The same final window also compares the canonical successor with the producer's
expected VersionID. A changed/nonlive replacement returns an indexed, wholly
unaccepted producer recheck, not index staleness; deletion keeps original
captured-source authority separate from its checked current version. The DML
producer owns its precise predicate/RHS/prologue boundary outside these locks.
DELETE latest-row authority remains its exact captured storage identity, not the
source reference's epoch. After that check, tuple admission derives a current
lock reference while retaining the original request; domain, transaction epoch
and storage incarnation checks still apply. UPDATE and MERGE retain their
request-epoch gates.
Ordinary source ordinals survive latest-row resolution as validated position
hints, including the actual position of a genuine successor. DELETE uses that
hint for its final exact physical lookup; single-row replacement windows carry
the same scalar through `WriteBatch`. Every use rechecks VersionID, and missing
or stale positions retain canonical historical/additional fallback. Hints add no
tuple authority, header ownership, side registry or ordinary-path allocation.

Demanded raw xmax capture samples exact canonical headers, releases metadata
locks for the shared manager batch, then readmits the storage/header publication.
Detached XID/MultiXact scalars belong only to immutable source/frame presentation;
they never become full-XID deletion, private-history or publication authority.
Current-row/RETURNING projection follows genuine event flushing. Expression
evaluation remains pure; source/statement cleanup retires its own captures.

Replication-slot lifecycle calls borrow the current effective actor under
Engine → Session lock order, requiring its own superuser/REPLICATION attribute.
After strict operand handling and routine EXECUTE admission, create checks the
shared slot-name rules; logical create also checks temporary/failover and the
current exact plugin allowlist. Both reject unavailable WAL/decoding machinery
with explicit 0A000 and publish no slot or session ownership state. Drop/copy
truthfully report missing slots; advance rejects zero LSN before lookup.
There is no provider, dummy slot store or synthesized replication position.

`SyncReplicationSlots` is a narrow live statement capability, borrowed through
the ordinary exec host after routine EXECUTE admission. It captures the effective
actor under Engine → Session lock order, releases locks, checks its own superuser
or REPLICATION attribute (not membership), and rejects unavailable physical slot
synchronization with 0A000. Held recipes retain no actor, authorization or provider
state. Logical-message publication needs no intrinsic role check or host state;
the typed function rejects unavailable WAL publication after strictness.

Session cancellation and termination borrow `SessionSignalRuntime` through the
executing expression host. Registry lookup spans the cluster under Engine.Mu,
releasing registry mutexes before Session.Mu; registered Ending owners remain
addressable until cleanup unregisters them. Authorization uses the current
effective expression actor, current role descriptors and INHERIT membership,
against immutable CountedAuthenticated target ownership. No victim transaction
admission lock is acquired. Request publication uses the session's existing
command/fatal authority, then releases all locks before waits or notices.
Identifiers never enter host process-signal APIs. Worker-role membership grants
no special authority over authenticated client sessions.
Parse-analysis relation waits attach their source location to an owned `57P01`
diagnostic copy. The canonical physical fatal stays positionless for later API
use; cached-execution and COPY admission keep their existing location fences.
Query cancellation `57014` continues to bypass that relation-position callback.

`external_sql.go` binds the captured genuine foreign-procedure token to its
registered external provider only at the unlocked execution gateway. The
physical session payload lazily attaches `remote.Owner` before first Open.
Callback contexts identify the exact executing operation; no opener enters
planning or persistent catalogs. Existing transaction/cursor owners settle their
actual remote work first; exact-owner terminal cleanup strips opener authority,
then the external resource closes/joins every remaining physical handle.


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
SQL transform replacement retains existing materialized definitions instead of
reconstructing bootstrap choices. Its independently authenticated directions and
immutable SQL declaration provenance survive runtime/type remapping. Transform
definition-object locks and name claims use the ordinary schema publication
fences, with no new singleton converter store or callback execution during DDL.
For a supported single OUT/INOUT host procedure, the actual Program scalar result
is converted as that member and wrapped by the shared `proc.PlanTuple` result
owner into CALL's exact nonnull record; a NULL member is not an absent record.



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
`catalogrt.Runtime.VisitPublicationTables` bridges selected publication relations
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
Origin registrations use explicit immutable `catalog.ReplicationOrigin`
descriptors, including the canonical registration created with each subscription.
The subscription's immutable creation ID is not proof of a currently live origin.
Exact origin-name writers, including empty TEXT names, and class6000 object
AS/AX grants use the shared cluster lock domain through ordinary catalog
admission; the caller retains its local catalog epoch for post-wait validation.
Subscription cascades and standalone retirement acquire the same object grant;
retained grants are queried using that shared identity, not a local alias.
The cluster mutex guards first-free 1..65534 allocation and lazy exclusions,
transferred from stages to origin descriptor journals and retained through
savepoints, internal exception journals and prepared ownership until disposal.
Each subscription reservation carries immutable database scope, not a second
registration authority; reconciliation reads only the admitted owner's journals.
The separate sparse progress map stores unsigned logical positions immediately:
backward and zero assignments survive statement/transaction rollback; zero still
consumes a slot. Explicit SQL drop rejects active owners, then clears progress before
transactional identity removal. Named demands resolve current private catalog
authority; ordinary mutable procedure EXECUTE ACLs, not replication attributes,
admit calls.
Subscription's internal clear instead waits for physical ownership release,
including self-owned origins. A lazy context notification closes under Engine.Mu
on reset, physical end or native invalidation. The DDL stack waits outside Mu and
its replacement reservation, then readmits exact operation/settings/catalog/role
authority before clearing the still-unowned slot. Cancellation and stale
readmission publish no cleanup. Transaction object grants are not session ownership.
The physical payload owns one scalar origin context, indexed by origin ID under
Engine.Mu independently of allocation reservations. Setup reserves a zero slot
and acquires exclusive ownership; explicit advance/drop reject any active owner.
Reset or terminal physical-session cleanup releases only the exact token. Pool
return, DISCARD, ordinary completion and savepoint restoration retain selection
and configured LSN/timestamp. Terminal cleanup advances an assigned abort before
release. Native replacement eagerly invalidates replaced contexts at installation,
while retained peer subscription contexts preserve their exact physical ownership.
Origin-aware completion advances logical progress monotonically before consuming
canonical assigned-XID ownership. Commit history receives explicit PostgreSQL
timestamp microseconds and origin identity; abort/subabort never create history.
Actual child assignment marks distinguish write-bearing savepoint/exception
aborts from read-only children of assigned parents; top-XID inquiry does not mark
children. PREPARE advances the preparing actor without timestamp publication or
ownership transfer; detached completion uses the finishing physical actor.
Every assigned non-origin commit stores its local stop stamp in the same physical
payload, even with history tracking off. Setup preserves that backend-global
timestamp; selected commits reuse it unless it is PostgreSQL zero, when their
local fallback is stored back for subsequent commits. Explicit session/xact reset
clears it. Native replacement clears the stamp with an invalidated selected
context, not an unselected fallback or a retained peer context.
Reset/Restore remint standalone and selected subscription registrations against
retained peers and pending claims. After the last preparation unlock, replacement
merges published/scoped pending-peer and orphan-active peer slots with imports,
including allocated zero, and validates the complete union against destination
active-slot capacity before publishing any catalog, storage or progress state.
The prepared map installs infallibly under the same cluster mutex. Pre-input
standalone descriptor and standalone/selected-database progress generations fence
stale replacement; pending-peer writes use reservation scope and remain allowed.
Active peer contexts retain their scope through ordinary rollback even without
registration/reservation; replacement excludes their IDs and counts their existing
slots. Unowned orphans keep predecessor global/replaced behavior. Drop-clear
never resurrects a slot from saved identity backing. External I/O and lock waits
never hold that mutex. Acquired rows retain their immutable backing.
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
The pre-input replacement predecessor fences Parameter incarnation, descriptors
and canonical bindings, standalone origin registrations, and standalone/selected
subscription progress writes. It does not fence unrelated local catalog edits or
peer subscription progress during reader I/O. Existing principal remap, TEMP,
provider, prepared-generation and allocation fences remain independently required.
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
operation; it admits its own private repair session when demanded. Inactive
maintenance has no simulation origin and supports distinct genuine sessions.
Active import repair validates the original origin and exact session/staging
binding before publication, projecting only its detached evaluation clone into
that unchanged stage epoch. Principal, domain-check, routine and expression
owners share that local view; the separately prepared destination image retains
its destination next epoch. Only internally acquired operations own the temporary
staging flag and its cleanup; supplied operation provenance remains caller-owned.

Statistics runtime (the `pg_restore_*_stats`/`pg_clear_*_stats` imports, the
`pg_stats` and `pg_statistic_ext_data` visitors, statistics expression recipes
and the statistics snapshot codec) is the leaf [`core/stats`](stats/README.md).
Core owns `stats.Host` on `statementHost` and the adapters of the `exec.Host`,
`pgcatalog.Runtime` and `ddl.Host` methods (`statistics_hosts.go`), the
`stats.Authority` that `AuthorizationView` implements, the imported-statistics
reset of storage replacement and TRUNCATE (`imported_statistics_reset.go`), and
the codec constructor `StatisticsSnapshotCodec` with its bootstrap
`stats.SnapshotHost` (`statistics_snapshot_binding.go`).

Statistics namespace movement uses the shared staged namespace resolver with
system destinations enabled, retaining CREATE and temporary/TOAST transition
checks. Its constructor enables that policy only for a genuine isolated import,
so restoring a moved definition does not relax ordinary CREATE admission.

The snapshot binding adapter supplies the codec's authority and cancellation
context to type-annotation, namespace and object-name hooks. Constructor casts
use the existing immutable snapshot resolvers, not live-publication admission:
the destination generation is deliberately private until final installation.
Live operation resolution and final publication retain their original fences.

EXPLAIN presentation (trees, estimates, statistics, pruning, foreign
provider details and TEXT/JSON output) is the leaf
[`core/explain`](explain/README.md). Core owns the statement seam:
Describe and ordinary Execute (`explain.go`), EXPLAIN ANALYZE capture
(`explain_analyze.go`), `explain.Host` on `statementHost` and the
`explain.Authority` that `AuthorizationView` implements.

EXPLAIN ANALYZE admits and executes the child through the canonical
binding, typed-parameter import and rule/CTE/DML dispatcher in the same statement
scope and write stage. It consumes and closes child output before returning
detached QUERY PLAN output; output expressions and their errors still execute.
INSERT VALUES and INSERT SELECT propagate the current invocation context,
including the analyzed command's observer, through source selection and DML.
They do not replace it with the enclosing operation's unobserved context.
The outer Finish remains the sole publication/abort owner, so caller transactions
retain ordinary Commit/Rollback semantics and a failed publication cannot expose
a successful plan. The wrapper fold owner stays inert; actual child preflight
graphs, presentation and runtime observations belong only to the current
activation, never a prepared description or portal.
Foreign presentation attaches only after the executor selects its real scan,
composite or modification plan, using defensive callback copies and detached
properties. ANALYZE never reruns metadata-only provider planning. Composite
measurements use the original replaced node identity and omit pushed local
children; ordinary EXPLAIN retains its metadata-only provider path.
The composed foreign ownership witnesses pair selected opaque tokens, current
typed parameter frames and modification targets with actual Begin/Exec requests
through capture, detached rendering and Finish. Provider property failure or
cancellation prevents startup/publication; a begun source still closes once.
Ordinary foreign query capture forwards the bound occurrence's dense `Uses`
ordinals without changing its manifest. A nil query-use slice denotes presence
only and becomes a nonnil empty scan-use slice; it must not become the foreign
request API's nil/full-tuple demand. `ONLY` uses that same ordinary path.
Mixed inheritance/partition adapters instead consume complete physical child
tuples before applying captured parent layouts. Capture expands their existing
lock manifest into the actual local/foreign prefixes before execution, retaining
physical storage authority and forwarding marks by the genuine foreign source
pointer. Unmarked executable identity reads, including stored-view expressions,
initialize that same manifest in identity-only mode before capture; they acquire
no tuple locks. Each logical row carries one active member and absent siblings through
locking, recheck, virtual/tableoid and cursor consumers. Full-tuple hierarchy
demand stays separate from ordinary/ONLY projection demand; both retain the real
scan owner, callback layout validation and unlocked producer lifecycle.
Foreign partition attachment fences the exact declaration without requiring a
nonexistent local heap; local candidate and validation stores keep their ordinary
storage fences.
Internal UPDATE/DELETE identity scans keep their own selected token and exact
request frame, but their EXPLAIN properties belong to the modification rather
than a separate observed base scan. The executing ownership fixture returns
provider assertion failures as errors and registers an unfinished-scope cleanup
before session shutdown, so a diagnostic cannot strand statement admission.
Observation-only modification request copies are made only after presence checks.

ANALYZE column facts resolve through the statement/transaction Journal overlay
of `storage.Store.RelationMetadata`, the single canonical ANALYZE metadata root
for heap, partition and foreign relations alike. Statistics-only commits validate epoch,
committed descriptor and optional real-heap identity, then merge selected
compatible attributes at the no-failure boundary without row-root replacement
or DML counters. Clone/savepoint/prepared ownership
uses the ordinary Journal lifecycle. Relation scan estimates remain independent
nontransactional maintenance metadata. A completed existing-object local pass
publishes its scan/count/time before inherited work; later inherited failure
preserves that maintenance but rejects both typed deltas and unfinished inherited
population. Ordinary/foreign inherited success does not count a second analyze;
partition success reports one analyze with local0/0. EXPLAIN captures immutable
compatible typed facts by stable attribute and SQL occurrence and compares
detached values without evaluating expressions. Equality/NULL fractions use
actual collector rows: full-native captures remain exact, while mixed inherited
and direct foreign samples retain separate population estimates. One float64
InheritedTuples supplies inherited estimates without snapping fractional values.
Bounded distributions/ranges and independence assumptions remain logical
approximations, not physical plan costs. Compatibility, restore and planning
traverse structural hierarchy metadata without renegotiating foreign capability.
Partition parents never own entries in the physical `Storage.Relations` map.
Their statistics, row estimates and analyze counters live in the same metadata
root as other relations. New private-object metadata remains on its creation
journal until commit; committed-object maintenance remains nontransactional.
Cumulative monitoring keeps the local ANALYZE sample independently of the
inherited population used for optimizer cardinality. A partition parent can
therefore retain an optimizer population of two while reporting zero local
live/dead tuples; inherited collection must not overwrite that local sample.
DDL prunes dropped objects and incompatible column facts. Native Reset/Restore
discards imported/persistent/reminted planner metadata, but preserves compatible
facts and counters for genuinely retained destination TEMP identities through
the existing TEMP-retention authority, never address coincidence.

`pg_stat_activity.go` routes activity to the default target's existing provider,
without moving cumulative facts. Physical attachment owns backend birth and
immutable login identity; real command/finish/settings boundaries publish source,
state, transaction start and application name. Source-less native transaction
controls preserve actual prior SQL rather than synthesizing command text.
`activity_views_capture.go` consumes cluster Engine.Mu and installs one detached,
unmasked transaction Reader cut shared by `pg_stat_activity`, `pg_stat_ssl` and
`pg_stat_gssapi`, independently of cumulative consistency mode. Any first consumer
completes canonical target-database XID/xmin associations before installing the
cut. Clear/end invalidate all three projections; physical retirement changes only
fresh membership. Current inherited observer authority is reapplied per execution.
After releasing Engine.Mu, only activity samples manager waits and overlays live
provider waits, even against cached membership. SSL skips both live wait work
and unauthorized peers, returning actual PID/false/six typed NULLs without
fabricated endpoints. None of these projections takes peer admission or retains
live session pointers.
Login identity is an immutable principal, not a stored usename spelling. The
producer applies the current role-name join on each execution, including cached
cuts after rename/drop; missing same-domain targets leave NAME NULL and preserve
valid observer stats authority without weakening strict role APIs or provenance.
The visibility Entry's monitoring-only ActivityFullXmin observes accepted public
snapshots, restores outer routine horizons and withdraws at command/transaction
retirement; it never participates in snapshot selection or XID allocation.
Routine snapshot restoration publishes monitoring horizons only with an actual
operation, session, engine and transaction reference. Detached scratch-only
activations retain their exact immutable frontiers/journal without entering the
live runtime or manufacturing publication authority.

Cumulative monitoring statistics are distinct from those physical planner facts.
The engine owns one `statistics.Provider`; physical session payloads own pending
writers, and transaction termination clears their readers. Real row mutation
records private attempted counts using operation-effective policy. A dedicated
write ledger carries actual relation nesting frames and first-pre-truncate state.
It belongs to `WriteStage`/`TransactionOwner`, clones at `SavepointMark`, and
transfers to `PreparedTransaction`; collection never creates a catalog journal.
Only completed top-level or aborted-child work becomes writer-reportable.
Successful children merge privately; PREPARE reports nothing unresolved, including
on preparing-session exit. Abort restores first-pre-truncate attempts, contributes
inserted versions to since-vacuum, and never publishes private live rows.
Completed ANALYZE/VACUUM reports instead update the same shared provider
immediately, including inside an open transaction. They neither drain ordinary
pending deltas nor change their throttle/retry or existing reader snapshots.
`stats_indexes.go` captures narrow index and relation execution capabilities with
operation-effective `track_counts`, retaining exact qualified refs and the
physical writer's generation. Sequential starts/fetched tuples, bitmap table
fetches and index search/entry/simple-fetch work are nontransactional read
accounting, never tuple ledger settlement. Top-level clear, actual failed-block
abort and successful
PREPARE record one UTC-microsecond stop marker before owner identity disappears.
A physical payload remembers the stopped TxRef so a later no-op failed-block
ROLLBACK cannot overwrite that terminal clock; savepoints/PL exceptions do not
stamp top-level stops. Prepared completion never transfers/replays read events.
The catalog runtime projects sequential/table-bitmap and index facts through the
same physical-session Reader as tuple/maintenance facts. ALL aggregates attached
index SUM/MAX only after identity predicates admit the parent. The replacement
baseline additionally preserves already-published
attached destination TEMP SQLIndex facts (which have no row store), remapping only
their retained qualified epoch. Imported persistent indexes and stale pending
writers do not inherit that history.

ANALYZE uses the actual local collector population (partition parents report
zero local tuples), subtracts current owner/statement frames and pending dead,
and clamps estimates. Its supported physical dead estimate is sampled under
the captured storage authority; raw invisible versions are not presumed dead
because birth frontiers also exclude live versions. Column-list reports retain
modifications. Existing-object reports and completed function calls survive
unrelated rollback. Journal-proven relation/function/subscription creations discarded by abort
retire their category-qualified keys unless retained by a restored owner or
statement checkpoint. Explicit existing/new object drops retire only on successful
commit, after tuple settlement; direct catalog publication also retires its
validated removals, even without an operation. PREPARE and child release transfer
ownership without retiring pending drops. Rolled-back drops and compatible
non-removal replacements preserve facts. Shared tombstones reject late writer
batches and function completions without resetting unrelated facts or readers.
Physical storage and optimizer metadata remain independent of report policy.
The two `pg_stat_get_xact_blocks_*` OID inquiries instead read only the current
physical writer's pending Relation pair. They neither resolve/open the numeric
target nor construct a cumulative snapshot, sample a clock or publish. Reached
nonempty heap/index invocations record one resident logical request as
fetched+1/hit+1 through the shared source lease. Effective track_counts gates new
events before writer work; prior pending remains visible. Private CREATE, TEMP
and rewritten owner storage remain eligible independently of shared-buffer
occupancy. Captures retain full identity, provider generation and storage
incarnation plus a detachable execution-authority slot, never a finished scope.
Existing-object reads survive statement/savepoint/transaction abort and stay on
the preparing physical backend across PREPARE; only real publication retires
them. No tuple projection or transaction-terminal reset is added.

Sequence encounters use the same Relation category and physical writer, not the
tuple ledger. Under Engine.Mu the common sequence host detects actual nextval
reservations from existing memory generation/tail, and records only successful
reservations/setval. Defaults, identities and triggers share that host. Admitted
sequence scans and authorized demanded pg_sequences last_value reads also record
one encounter, including uncalled NULL; privilege-gated reads and memory-only
calls record none. Event-effective track_counts suppression leaves real cache
reservations and session values unchanged. No clocks or physical IO are involved.
The catalog runtime exposes only the transaction reader's encounter count;
the typed source projects logical reads/hits after snapshot selection. Reads and
clear never publish. Statistics reset retains value tails while fencing pending
counts. Successful native replacement preserves attached destination TEMP
sequence published facts as well as heap facts under the new epoch, but neither
old pending deltas nor imported monitoring history; failed replacement changes
none of them.
Physical TEMP teardown retires accepted removals in that same catalog-installation
window under their current epoch. Retained namespace tuples do not retain removed
relation facts; shared provider tombstones reject pending or later deltas,
including the ending owner's final flush, without borrowing its writer state.
Function collection starts at admitted ordinary, PL-trigger and native-trigger
body boundaries, after effective routine configuration and identity are installed.
Dispatch/enqueue/WHEN filtering do not collect calls. Stock internal routines
remain untracked; procedural bodies follow `pl`/`all`, and applicable C/SQL bodies
follow `all`. Disabled selection samples no clock and allocates no frame.
Completed bodies share the physical writer's saved pending-total and backend
charged-self baselines: same-key recursive overlap is replaced, distinct keys
retain inclusive totals, and each completed invocation retains its call/self.
Error/cancellation unwinds the frame without completing the failed body or
discarding already completed descendants. Function facts use ordinary publication.
Ordinary Finish/message/native-terminal/admitted-error boundaries attempt publication only
at true outside-transaction idle. A generation-qualified one-shot ten-second retry
handles throttled pending; due requests survive busy/transaction blocks until
ready. The existing SQL-lease ready hook uses only nonblocking admission/owner
acquisition and the leaf provider. Unconditional post-resource DrainStatistics
cancels and joins callbacks outside locks, even without a Transaction attachment;
Unregister force-drains after cleanup. Reader clear
never publishes. Provider locking is a leaf beneath existing owner/engine locks.
Session/database events share this writer and may consume its first eligible
report before relation, index, function or sequence work. Internal lifecycle
fixtures prime ordinary eligibility with synctest virtual time before their
measured phase; their exact pending/reset and ten-second retry boundaries remain
unchanged. Completion alone is not authority to bypass the shared throttle.
Replication statistics reset uses current EXECUTE admission and the cluster mutex.
Named subscription OIDs search existing records in the actual owning providers,
retaining each stored domain/epoch key independently of caller-private or committed
catalog visibility. Provisional CREATE and staged DROP records remain eligible
until their actual retirement boundary. NULL-all visits the same provider inventory
using one microsecond timestamp. No projected record is created in another database.
Targeted resets preserve global generations, unrelated categories, pending writer
prefixes, throttles and retained readers, and survive transaction rollback.
Subscription staging initializes its actual zero/NULL-reset shared entry only
after successful creation. Unadopted stage discard and adopted journal retirement
own provisional lifetime; drop waits for successful ordinary/direct/prepared
publication. Native replacement seeds only selected-database restored subscriptions
under fresh identities, leaving peer providers and retained captures unchanged.
Cumulative subscription capture resolves each shared descriptor to its actual
owning provider/domain/epoch under the same cluster fence and passes detached
facts to the pure producer. Current SELECT/getter6231 EXECUTE precedes Reader
configuration. There is no dynamic owner mask or callable getter implementation.
The existing typed Keep for catalog-owned `subname` rejects excluded descriptors
before owning-provider lookup and Reader.Read, and the pure producer applies the
same name restriction before projecting facts. Cache mode therefore retains only
demanded names; a held statement uses its current parameter on each execution.
Getter-produced `subid` does not supply that demand boundary.
For a direct single-source Project→Filter, admission and capture consume the
same occurrence-owned `KeepQuery.RejectsInput` decision. In addition to its
literal/imported-BOOL predicate proof, subscription stats alone accepts one
shallow AND of safe direct builtin NAME-key equality and a literal/current
imported BOOL. Exact source/attribute/frame/type authority is required; casts,
domains, calls, nested logic and other topology keep ordinary demand. Rejected
FALSE/NULL skips getter6231 and Reader configuration without bypassing SELECT;
TRUE rechecks current EXECUTE. Each execution imports current values, never a
retained rejection or permission. Parameter reference witnesses explicitly use
custom planning, not an automatic held-plan choice.
The existing Reader's initial full cut includes all peer Database and Subscription
facts even if the first cumulative access filters a subscription name or reads a
local relation/index/sequence or fixed family. Filtering never narrows that cut.
Peer relation/function/backend facts stay excluded; no second cache exists.
Synchronous catalog producers carry a private lexical Engine.Mu borrow to select
locked preparation without recursive acquisition. Standalone runtimes acquire
the mutex only for a needed initial cut; inspection roundtrips discard the borrow.
None/cache modes never enumerate peers for initial snapshot preparation.
Shared and SLRU resets traverse these same actual cluster target providers.
Target validation precedes mutation. Backend reset classifies the actual PID in
session registries, releases registry ownership before lifecycle locking, then
resets that full live token's retained IO/WAL partitions without borrowing a peer
writer. Successful public-session startup grants positive metrics reporting
membership to current actual partitions. New target publication inherits that
reporting authority, never activity facts or metric history. Public startup
separately registers immutable `CountedAuthenticated` identity and actual birth
facts once in the existing default-target cluster activity map, before any
command. Private and failed initialization never enroll.
The unlocked end-drain verifies actual registry ownership and withdraws the
token's membership and facts from every existing partition, without historical
negative tokens or private-owner pointers. Session.Mu-held Unregister never
takes the cluster mutex, preserving cluster-before-lifecycle lock order and
ordinary final drain. Revoked membership rejects that final drain's stale backend
deltas while unrelated pending work still drains normally.
Provider locks remain leaves under the cluster mutex. Published changes are
immediate and nontransactional; reset never clears the physical reader or moves
its snapshot construction timestamp. Fixed-family events are logical provider
state, not evidence of physical workers, storage IO or WAL generation. The
per-target traversal preserves existing shared-family behavior. Archiver,
bgwriter, checkpointer and WAL retain explicit `DatabaseCluster.ArchiverResetAt`,
`BGWriterResetAt`, `CheckpointerResetAt` and `WALResetAt` authorities under the shared mutex,
initialized from the PostgreSQL-epoch StartedAt
instant. Target publication mirrors current authority only after fallible template
work/admission; accepted local catalog replacement restores it after Provider.Replace.
Neither boundary is a shared reset. Authorized selected-family/NULL-all shared
reset updates its authority and every current provider mirror at one microsecond
UTC instant. Other families and standalone provider semantics are unchanged.
No second provider or Reader is needed without production physical event writers.
The archiver view checks current SELECT and backing routine3195 EXECUTE before
configuring/fetching its session Reader, then projects detached facts outside
Engine.Mu. Describe and denied admission cannot populate the Reader. Fixed/shared
families retain their current provider's coherent values; snapshot additionally
includes accessible peer-database totals and genuine shared subscription keys,
never another runtime's relation/function/backend data.
Backing routine3195 is a zero-provider catalog descriptor with ordinary default
PUBLIC EXECUTE and live GRANT/REVOKE, not a getter implementation. Current native
admission remains independent of retained statistics backing; portable peer-ACL
reference witnesses cross the reader transaction boundary for catalog visibility.
The five-column WAL view follows the same singleton capture path with backing
routine1136, detached SharedWAL ResetAt and typed zero physical estimates.
Keyed WAL operation/timing facts never become physical record/byte counters;
Writer.Shared continues to exclude SharedWAL. Describe and denied EXECUTE cannot
initialize or clear a cumulative cut. Its cluster reset stamp is mirrored only
at genuine startup/reset, accepted database publication and local replacement.
Bgwriter checks current SELECT and each demanded scalar getter's EXECUTE across
the recursive selected source closure during query-source preparation, before any
source or policy materialization can configure the Reader. Preparation and direct
low-level capture share one getter-demand mapping; capture independently rechecks
current authority, never a retained permission token. Existing column demand
includes filters and sorts; empty demand needs only singleton cardinality.
An occurrence-owned direct BOOL FALSE filter proof suppresses Reader access, not
getter ACLs or normal filtering. Ordinary aggregate keys, arguments, FILTER and
ordering consume that rejected input without a read; ordered-set direct arguments,
identity reads, subqueries and unsupported boundaries do not qualify. No expression
evaluation occurs under Engine.Mu to establish that proof. Its
three physical technical estimates are zero absent corresponding facilities/events,
never query-derived; ResetAt is genuine shared metadata. Neither view masks fields
by monitoring-role membership, and the four bgwriter getters remain noncallable.

`pg_stat_xact_user_functions` uses authenticated current Procedure identities and
the same physical backend's pending Function facts, never the tuple ledger or a
cumulative Reader. Ordinary/configured/trigger body entry retains its existing
tracking selection inside the installed environment. The provider's elapsed
measurements remain unchanged; function views project deterministic completed
invocation cost (one millisecond own work), including nested tracked descendants.
Admission's zero entry remains visible on failure without counting a completion.
Completed events survive savepoint rollback; ordinary publication/throttling and
qualified retirement determine pending lifetime, not transaction-end clearing.
Current relation/column SELECT precedes backing getter3046/3047/3048 EXECUTE under
the invoker. Their VOLATILE outputs survive ordinary projection pruning, so even
identity/count occurrences retain all three initialization checks, including
LIMIT0. The existing occurrence-owned literal FALSE proof eliminates that subtree
and its getters, but not own-view SELECT. A direct single-source Project→Filter
also uses the existing QueryPredicateRejectsAll proof for this execution's imported
Boolean parameter: FALSE/NULL elides that input, TRUE retains all getter checks.
The bound proof is recomputed on every held execution, never cached permission.
Whole-query admission and low-level capture recheck the same current authority.
The existing KeepQuery temporarily carries the already-preflighted root, exact
source occurrences and canonical parameters around the earlier row-security/source
metadata prewalk, then restores the prior scope value on success or error.
Admission order is unchanged; materialization still installs its own capture proof.
None of these paths populates a Reader or masks fields by monitoring-role status.

`pg_stat_user_functions` reads the same qualified Function event provider through
the authenticated cumulative Reader, never the backend's unpublished batch.
Shared membership and published counters share one none/cache/snapshot cut,
including negative cache entries and admission's shared zero before reporting.
Admission never publishes completed calls or timing. Current Procedure names and
identity Keep filtering precede inquiry. Nonowner/schema/routine ACLs do not
redact distribution. Own view/column SELECT precedes invoker EXECUTE on the
retained STABLE calls/total/self expressions (2978/2979/2980).
An execution-owned occurrence decision separates the three-bit getter mask from
provider input capture: ordinary input always needs calls for membership; false
input retains projected getters but discards predicate-only getters. LIMIT0
retains ordinary initialization. Physical BOOL/imported-parameter false or NULL
conjuncts, a transparent identity-derived input, and an unused uncorrelated false
LEFT JOIN side have bounded ownership proofs; unsupported shapes remain ordinary.
Whole-query admission finishes before any Reader cut; low-level capture rechecks
current authority. Prepared roots retain neither permission nor a prior decision.
Elided inputs remain explicit empty anonymous row sources with the admitted
identity and full typed width; nil anonymous storage means a missing physical
source, not an empty materialized result.


`stats_database.go` projects database facts through the same catalog runtime.
Database-category keys use the immutable database/domain identity (epoch zero),
not unsynchronized catalog roots. Every cumulative first-access authority invokes
the same `captureDatabaseStats` complete-domain capture before a Reader method
can mark the cut full: `DatabaseStatistics`; `relationStatistics` (table,
heap/index-block and sequence projections); `IndexStatistics`; and the
archiver/SLRU, subscription-stats and demanded bgwriter source-capture branches.
Bgwriter retains its presence-only and proven-FALSE no-read guards; neither
those paths nor denied getter admission capture peers.
Existing SELECT and backing-routine EXECUTE checks precede capture. Providers
are visited in stable database order under shared membership authority, with
leaf-only provider locks. The current provider's keyed, fixed/shared, SLRU and
backend partitions and each provider's reset/missing defaults keep their existing
owners; peer Subscription facts and the shared database key are copied in their
owning leaf-lock interval. Standalone DatabaseStatistics retains Engine.Mu across
both capture and target/live-registry enumeration; synchronous calls reuse their borrow.
`statsReader` only configures. Timestamp/clear and independent activity capture
do not construct a cumulative snapshot; recovery-prefetch stays a fresh direct
provider read. None/cache behavior and clear-without-publication are unchanged.

`stats_xact_tables.go` supplies the owner-local `XactRelationStatistics` inquiry
under current statement admission and Engine.Mu. It combines the physical
session Writer's copied pending access/tuple facts with the live transaction
Ledger and statement suffix's current I/U/D triples. It never demands a
cumulative cut, applies collection settings at projection, or clears work at a
transaction boundary. Settlement, publication throttle, retirement and provider
replacement retain their existing owners. Captured catalog identities/names
and current view SELECT authority remain independent of statistics history.

`TestStatDatabaseSnapshotFirstAccess` exercises actual local index, archiver,
bgwriter and SLRU `transaction` rows against positive distinct B/C writes,
retained publication cuts, clear and both transaction terminals. Its clear call
uses the existing typed
VOID consumer, which checks the non-NULL singleton, empty output and EOF before
closing; the generic test tuple exporter does not support VOID.
The source combines retained cumulative facts with live registry counts under
`sessionMu`, inspecting only immutable successful public-registration identity;
it never acquires `Session.Mu` from that registry lock. Private restore/maintenance
owners and failed admission are not public sessions.
Native admission attaches an idle transaction owner but does not begin a catalog
transaction, so registration adds no synthetic transaction outcome. PostgreSQL
18.6's `InitPostgres` really starts and commits a startup transaction. Reference
SQL-outcome scenarios isolate that source-grounded difference with a published
marker on the same live writer before measuring SQL; client admission is
asserted separately. Idle physical cleanup itself still adds no outcome.

Top outcomes are recorded once beside actual owner/ref completion or a narrow
accepted SQL-control/admitted-command authority. Idle physical cleanup and
repeated aborted commands cannot invent rollbacks. Idle SQL COMMIT, ROLLBACK and
PREPARE-warning commands do have genuine backend outcomes; a parsed empty simple
query commits. Argument conversion rejected before command admission does not.
PREPARE transfers unresolved work and records no outcome at preparing exit.
Successful actual completion removes the claimed prepared entry and records the
original outcome once, separately from the successful finisher command's commit:
COMMIT PREPARED adds C2/R0; ROLLBACK PREPARED adds C1/R1. A failed finisher adds
only its actual command abort and does not complete the original. This follows
released `FinishPreparedTransaction`'s `AtEOXact_PgStat` callback, not SQLSTATE.
Admitted syntax/analysis failure before visibility ownership records one command abort,
not an error-text event. Ledger settlement contributes database attempts independently of relation
retirement. Public registration, terminal cause and actual monotonic activity
transitions share the session writer. Physical connection retirement, not return
to a `database/sql` pool, is the terminal publication boundary. Genuine hard
deadlocks are drained from the victim's ticket, never inferred from SQLSTATE.
Real executor worker facts merge only on the coordinator after worker joins,
including failed execution; plain EXPLAIN does not collect launches.
Runtime worker and idle-timeout witnesses use actual `AcquireCommand` admission,
which captures worker/cost policy and owns SQL idle completion. Native-control
`Acquire` does neither. The real supported partial-aggregate witness is
`string_agg`, not serial-only CountProvider. Cancellation joins both actual
workers after one physical input predicate per worker and retains those facts.

Selected runtime proof: `make check RUN='^TestStatDatabase(CatalogIdentities|Transactions|TupleFrontiers|SessionCauses|Workers)$'`;
the catalog unit checks installed row/array ownership and full-seed definition
disjointness before database-template replacement. Use `RACE=1` for the
reader/session/worker concurrency selectors. Public SQL and the separate feature
smoke live in untagged `reference/` definitions.

`pg_stat_gssapi` reads this same sole cluster activity map through the real
transaction's raw reader, independently of cumulative snapshot mode or timestamp.
All three monitoring views use `captureActivitiesLocked` to install one complete
cut including canonical assigned XID and accepted xmin enrichment, even when SSL
or GSSAPI is read first. Only activity refreshes post-unlock live wait overlays. At each
GSSAPI execution core captures current authorization under Engine.Mu and
classifies the cached immutable `Activity.Principal` startup identities using
inherited login-role or
`pg_read_all_stats` privileges. Unauthorized peers are absent, not masked tuples;
SET ROLE never relabels the target login. Stats readers can still observe a live
backend whose login role was dropped; recreating its name does not adopt its old
principal. Only detached visible PIDs reach the pure producer after releasing
Engine.Mu. Clear/end refresh membership; Reset/Restore preserve physical lifetime.

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
PL diagnostic signatures belong to the session's existing routine-analysis
cache and the exact immutable Procedure, not persisted program backing or
invocation authority. Validation or first invocation captures presentation once;
ordinary search-path changes and restricted maintenance reuse that signature.
A cold maintenance executor captures its safe-path-qualified spelling and retains
it after unwind; a replacement Procedure recaptures independently per session.
This follows released [PL compilation](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/pl/plpgsql/src/pl_comp.c)
and [function-cache lifetime](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/utils/cache/funccache.c):
the compile callback formats the signature, while cache reuse checks the current
declaration rather than search-path visibility. Invocation-private diagnostic
scratch still restores across nested commands and failures.
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

Simulated sepgsql execute initialization belongs to the actual executor frame,
before argument effects; ordinary SQL/PL/host body dispatch consumes its exact
completed receipt instead of rechecking execute under a changed argument subject.
`SepgsqlObservationMark` snapshots observation length and independent monotonic
initialization/retirement completion watermarks. Failed inner work discards only
newer receipts even when both actions shared a deduplicated observation; counters
never rewind. Command Finish and cursor detachment discard borrowed owners.
Native entered and strict-NULL expanded declaration owners derive separately
from the exact original source receipt and typed phase. DECLARE, FETCH, HOLD
promotion and producer teardown clear the inert borrowed owner beside Host.
Coordinator captures strip live codec/domain/support borrowers, then active
workers and native aggregate leaders consume exact sealed source evidence.
The internal initialization owner fixtures use real analyzed grants, including
deduplicated rollback, constructor phase transfer and selected ordering kernels;
they do not install receipt maps or fabricated policy authority.

Trusted Restore keeps a distinct serial initialization completion record, never
a client MAC receipt. Only a simulation-installed RestoreStage mints its private
engine/domain/epoch/runtime origin; one successful StartRestoreSession binds the
physical owner. Inactive private maintenance mints no origin and remains
multi-session. Repair does not mutate, replace or rebind an existing origin.
The actual command owner retains that exact origin beside its frame identity.
Fresh operation, scope, private read, bootstrap, staging and source/SQL EXECUTE
checks remain required at initialization and entry, including repeated sites.
Import records contain no subject, policy decision, label predecessor or audit
evidence; native body/type-input import boundaries remain unchanged. They cannot
seal even an empty owner into a closed worker proof. Public control consent and
the final replacement catalog/role/policy/cancellation fences are independent.

Generic simulated SECURITY LABEL retains its real resolved object across the
ordinary catalog-lock adapter. TYPE/DOMAIN use existing TypeExclusive; ordinary
TABLESPACE objects alone join the shared RoleDomain AS/AX route. Their original
local domain/epoch is validated after waiting outside Engine.Mu. Local catalog
ACL writer resources remain distinct and unchanged. Current descriptor identity
and ordinary ownership are recaptured before unsupported-provider rejection;
no label candidate is staged for these unsupported classes. Composed coverage is
`TestSepgsqlSimulationGenericLabel(NoPublication|FreshOwnership|LockLifecycle)`
in `schemacmds`, including cross-database tablespace grant/wait projection.

Public simulated `sepgsql_restorecon` checks the current SQL actor's superuser
authority before looking up the immutable virtual profile. It selects every
actual shared database descriptor and only the current root's local objects.
Two filtered passes over that same selection visit databases first, using each
database's own quoted key, then local objects; all permissions and predecessors
belong to one stage and one atomic publication. Denial, cancellation or stale
role/root/label authority publishes none of the requested associations.
Private startup seed labels and both sides of native replacement consent retain
their current-database-only scope and the existing sorted-union ordering.
`TestSepgsqlSimulationRestoreconScopeAndOrder`, `SeedAndControlScope` and
`PublicationFences` cover these actual owners; the dedicated reference
restorecon scenarios cover direct/prepared two-root visibility, peer-local
privacy and privilege/profile diagnostics without claiming active SELinux parity.

Direct TypeIO, encoding and FDW factory fixtures establish a real transaction
through Gate, then capture the current routine authority and identity. A bare
StartStatement has no transaction epoch and cannot authorize MAC observations.
Their actual direct callbacks still do not acquire fictional SQL execute
initialization. Every fixture installs scope cleanup before a callback or
assertion can fail, so diagnostics cannot be hidden by a busy-owner drain.
The FDW no-importer audit witness keeps the same physical session but gives each
IMPORT its own real operation. It observes host audit only after that operation's
deferred `Done` releases the lease and delivers queued records; scope `Finish`
alone does not deliver host callbacks. Setup operations finish before the audit
slice is reset, so their events cannot contaminate the selected factory phase.
Ordinary direct column defaults retain their exact routine source in the
command's retained-expression phase. `AcceptedDefaultInitialization` instead
belongs to the genuine sealed source artifact produced by protocol Bind and
selected by Execute. Constructor witnesses exercise both lanes: direct calls
prove the actual default routine's retained receipt; real Parse/Bind/Execute
proves the sealed artifact, returned rows and all four typed constructor owners
before Finish. Neither lane manufactures accepted provenance for the other.
Actual PL exception checkpoints retain the same typed observation/completion
mark. Their rollback removes only the failed suffix before a handler runs,
including cancellation cleanup, while preserving older reused receipts and
never rewinding completion counters.

Index definition completion preserves its original predicate/key prewalk,
including constant calls before any real build tuple exists. With simulation
active, `DDLHost.PreflightIndexDefinitionLocked` authenticates the catalog
stage's actual statement, suspends the engine mutex for genuine initialization
and provider work, then readmits the same settings/session, stage/base, live
catalog/roles/actor and selected parent before continuing. It never rebinds or
replays the prewalk on a changed predecessor. Generation-definition conditional
simplification uses `DDLHost.SimplifyGenerationDefinitionLocked`: CREATE borrows
its genuine stage/read, not an installed parent; ALTER may legitimately have no
stage. It creates the real command initializer before conditional context copies,
suspends Mu and the replacement reservation for constant callbacks, restores its
borrowed authority and readmits exact operation/settings/catalog/role/actor and
optional stage structural predecessors without retry. It adds no coercion
prewalk or prospective relation admission. Demanded stored-generation runtime
preparation likewise initializes its parent before recipe copies; actual row
evaluation carries each definition's original manifest and exact consumer
(the old installed relation for ALTER) with the same command owner. Its cache
contains expressions only, never receipt or session authority. UPDATE attaches
the exact prepared command header to its captured physical target through the
existing source borrower before dispatch; State never recovers a missing owner
from an ambient Host. Original INTERNAL kernels retain their no-FHET contract:
manifest admission, completed-site consumption and source opening validate at
their actual boundaries, not through newly added per-row EXECUTE checks.
Inactive metadata callers retain their original hostless, zero-owner behavior.
Retained cursor evaluators refresh only their previously sealed live source-header
association; they validate the last local owner/catalog first, never adopt ambient
statement authority. The exact current attachment owner reaches cached projections
and nested CTE states.

Protocol producer construction detaches its former query header before the
first real attachment. Terminal portal/XML motion leaves detach owner/catalog
beside Host. COPY query pulls instead suspend Host and query scratch while the
same lexical COPY command remains admitted; each real pull borrows that SAME
owner without initialization replay. Final producer disposal detaches it.
The multirow encoded-COPY witness checks a real initialized header at the
response, stable owner and receipt watermark through suspended conversion work,
actual Latin1 rows, restored subject and a fully detached terminal header.
The finalized-simple INSERT RETURNING witness instead retains the real eager
store and proves no live SELECT portal was created. At its actual unlocked
metadata callback, the fixture activates the admitted scope's runtime on the
operation context consumed by encoding, not a copied evaluator context; binding
retains an inert request. Its installed hstore output and actual trusted Latin1
converter then pause after entry at WorkEncodingConvert.
Peer relabel or descriptor replacement must reject with the exact catalog
publication conflict before the row callback sees fields; unchanged authority
delivers the exact encoded value and SQL NULL. Both failure legs and the healthy
leg preserve original predecessor/initialization evidence, restore the caller's
subject phase, close the store and join the worker without encoding retries.

Direct index-method factory initialization and entry authenticate the actual
ownerless `pg_am` source through its catalog domain/epoch, whole-object identity,
index-method provider and exact selected handler. They retain the real SQL actor
and handler-owner admission without inventing an access-method owner or requesting
procedure EXECUTE for a direct factory call. Reusing a completed initialization
receipt still validates the current source; retiring the method revokes entry.
Selected direct index-support callbacks likewise identify the actual ownerless
`pg_amproc` member. Admission requires its exact domain/epoch/address, canonical
family/operand/slot lookup, helper identity and both real registered providers,
then checks ordinary authorization-object ownership on the real operator family.
It neither invents a member principal nor grants procedure EXECUTE to a direct
callback. Wrong-class, missing, column, foreign, stale-epoch and wrong-role/helper
sources reject before any receipt is recorded; closed comparisons consume the
same sealed selected source. Other direct callback sources retain ordinary
authorization-object admission.
IndexSupport itself is inert, domainless selection metadata: an equivalent token
resolves against the supplied local catalog and may use that local sealed proof.
Actual captured callback Reference/Source and initializer/read provenance carry
the lifetime boundary. Foreign callback refs, owners or reads cannot borrow a
local certificate merely because display OIDs and structural tokens agree.

Actual native aggregate kernels do not own ordinary scalar callsite receipts.
Their invocation-local, self-associated source is consumed at the selected
postargument entry and, when simulation is active, authenticated with
`Host.ValidateRoutineInitializationOwner(owner, read)`: active scope, genuine
scope-map/CommandIdentity ownership, current authorization under Engine.Mu,
original read owner/domain/epoch and cancellation. This check allocates/seals
nothing and neither mints receipts nor replays EXECUTE. Existing aggregate
membership and AggregateSupportDependency lifetime checks remain in exec; SQL
helper EXECUTE stays aggregate-owner admission while body relation rights stay
with the invoking scope. Direct native support retains execute=false FHET.
A genuine native SQL wrapper used as support (round FINAL or lpad SFUNC) carries
only its immediate authenticated parent through existing nativeBodyLineage,
borrowing the SAME original initializer. Its real child calls still initialize
under the entered subject with DefinitionDependency and exact Body/CallPart
coordinates. Ordinary body parents continue their own receipt/child-owner path;
no aggregate parent receipt/map/owner is fabricated. Closed native non-body
workers authenticate the same sealed owner/read without a live host; conditional
body work retains the existing serial gate and unrelated phases sever lineage.
Inactive genuine SQL Body/DeclarationBody aggregate support retains the same
immediate association and original AggregateSupportDependency lifetime. Its zero
simulation owner remains zero: no MAC owner guard, parent mint, receipt or map is
needed. Ordinary child lifetime/SQL ACL checks still run against the original
aggregate parent source, not a fabricated DefinitionDependency for the wrapper.
No-body/standalone inactive fast paths, closed-body rejection and existing
parallel gates are unchanged. Inactive round/lpad fixtures use the exact public
DDL without COMBINE/PARALLEL additions and assert zero MAC owner/receipt maps.
The native aggregate fixtures capture physical inputs through the same admitted
statement scope's relation/data-view owners, including genuinely empty inputs.
Planner column shape comes from the captured relation descriptor, never a sample
row or a SELECT result that can legitimately omit its source when empty.
Foreign-declaration fixtures distinguish selected-call rejection before native
evaluation from a foreign reference attempted at the genuine selected entry.
Finished-owner witnesses resume retained frames only after normal Finish, while
body continuation probes retain their active scope through observer unwind;
they never finish a statement from inside its own entered-body callback.
The owner fixtures retain actual native sum10/loops2, hstore/arithmetic fanout,
SQL-support sum10/loops1, strict/nonstrict NULL demand, exact-source rejection,
denial/unwind/retry and no terminal marker residue. These are authored contracts,
not evidence of an exercised check or real SELinux enforcement.

Retirement preparation uses the same raw-removal/index-evolution metadata owner
and captures secondary owned/late constraint cohorts before effects, with ordinary
grants and RESTRICT preceding combined MAC while parents/namespaces remain live.
Live source descriptors come from the authenticated full stage read, including
unchanged committed objects. `Candidate.Get` exposes staged deltas only and is
not a source-existence test; committed predecessors remain independently fenced.
Later effects consume exact stage/predecessor/completion-stamped decisions, not a
new allow under the final caller. Explicit ON COMMIT/drained cleanup origins never
fabricate a client SQL subject.
`activeTriggerDepth` belongs to the root invocation owner, separately from
routine safety depth and lexical scratch. Only admitted DML trigger bodies and
individual internal RI callbacks enter it, with deferred unwind. WHEN and queue
qualification read the surrounding level. Ordinary routines and event triggers
borrow it without entering; rollback and native controls never serialize it.
The three builtin DML trigger declarations and authentic installed native
trigger providers share PL installation, stored validation and live invocation
admission. Immediate and queued AFTER dispatch select the admitted native body
before allocating any PL frame.
Each body borrows OLD/current NEW. Immediate bodies borrow stable command UPDATE
attributes; queued events own an immutable target copy shared by the definitions
accepted in one queue call and forward it unchanged to native firing. Earlier
row rewrites never widen that lexical target set.

AFTER accepted images belong to the existing query effect frame, keyed by
relation and actual event. Lazy transition segments collect each accepted tuple
once, independently of ROW WHEN and consumer count. Each demanded OLD/NEW image
is detached once and shared with qualifying scalar queued events; an unused
counterpart is not copied. Capture-only hierarchy ingestion uses the explicitly
named target's layout without manufacturing a physical ROW event. Movement
collects UPDATE OLD after deletion and NEW after insertion, so a destination veto
can leave an OLD-only set. Initial zero-row sets retain the relation schema.
Statement-only physical capture requires that relation/event's actual producer
registration; definitions on unnamed leaves create no hook or unused segment.
ROW exposure freezes a segment; only a later accepted image rotates it and
creates its successor statement group. After RI exhaustion and paired internal
trigger unwind, ROW consumers may expose it; unrelated and statement-only sets
remain pending. Checkpoints detach maps/event references and exposure progress,
with copy-on-append vectors over immutable payloads. Finish and nested command
retirement discard all capture state.
Constraint ROW events may share an ordinary consumer's exposure segment but
cannot own transition aliases. RI exposure and final query draining use the same
constraint timing decision: immediate events invoke with live identity, while
deferred events retain only accepted scalar payloads in the transaction vector,
never the query's transition segment or invocation namespace.

Transition aliases occupy a balanced one-trigger-invocation namespace, shared
by its static/dynamic commands, shadowed by nested triggers and cleared for
separately called functions. CTEs take precedence; qualified names bypass it.
Binding retains only a symbolic scan name/schema. Opening resolves current data
into an ordinary private scan over shared immutable rows, with no catalog
identity, locks, privileges or extra evaluator. Modifying targets cannot fall
back to a hidden physical table. Ordinary CREATE VIEW reanalysis is catalog-only;
other persistent capture rejects invocation relations. Blanket row locking skips
these sources; explicit OF targets reject them without acquiring physical locks.

Native trigger bodies (tcn, refint, autoinc, insert_username, moddatetime,
lo_manage, suppression and the tsvector triggers) are the leaf
[`core/nativetrig`](nativetrig/README.md). Core keeps the invocation
envelope (`invokeNativeTrigger`, `native_trigger.go`) and implements
`nativetrig.Host` on `statementHost` (`nativetrig_statement_host.go`).


Admitted mutation targets retain dynamic source roots when their trigger bodies
or destination-owned DEFAULT/CHECK manifests contain lazy relation inquiries.
Those manifests remain separate from outer expression dependencies. The actual
DML executor admits target-root retention at its snapshot boundary, including
ordinary direct/prepared commands without CTE/rule mutation syntax scratch.
Lazy reads extend the fixed command roots and frontier, never recapture newer
roots to fill a missing target. DML without dynamic demand retains no additional
dynamic-root map.

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
Reached SQL body statements classify genuine operation user cancellation through
`Operation.CommandError` before adding their own statement frame, preserving the
complete nested diagnostic without mutating the shared cancel cause. Aggregate
support retains the same boundary for its statement timer; ordinary caller or
sibling cancellation remains an ordinary context error.
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
isolation phase before analysis and source capture. If a child admits the first
stable owner pair, restoration carries that exact pair and demanded export
backing into previously empty same-incarnation caller scratch. Already-admitted
callers retain their own pair. Successor snapshots remain lazy: snapshot-free
utilities preserve the immediate characteristics window, while read-committed
commands keep their data-admission frontier and lexical restoration.
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


Text-search runtime (conversion, `ts_debug`, the `ts_rewrite`/`ts_stat` nested
SQL, dictionary and thesaurus acquisition, `unaccent` and parser resolution) is
the leaf [`core/tsearch`](tsearch/README.md). Core owns `tsearch.Host` on
`statementHost` (`tsearch_statement_host.go`), the `tsearch.Authority` that
`AuthorizationView` implements, `textSearchHost`'s `exec.TextSearchRuntime`
adapters, the tsvector trigger's use of `tsearch.Capture`, and the session's
`tsearch.DictionaryMemos` in `sessionPayload`.

Generic folds retain immutable explicit vector/query results and their first
execution notices are not replayed. Stable omitted forms and runtime arguments
consume current defaults/mappings without local result caches. Rich lexical metadata
stays internal; only `ts_lexize` projects TEXT[] at its checked SQL boundary.
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

## pg_trgm initialization and thresholds

`pgtrgm.go` owns the admitted physical-session transition shared by the reached
extension script and actual routine fold/residual initialization. Activation
locks Engine, physical SettingsStore, any distinct effective store, then Session;
the settings owner promotes active physical and detached images once. Returned
warnings enter the ordinary operation notice buffer outside settings/session
locks. The script reaches activation after its first setter declaration passes
the duplicate-name check; a duplicate setter, skipped CREATE or analysis alone
never loads the overlay. Getter and later declaration collisions cannot undo
physical loading when the catalog stage rolls back. Threshold
evaluation takes Engine, effective SettingsStore, then Session, validates the
current invocation and reads the typed threshold without implicit loading.
The getter rounds that typed value to the expression-owned REAL descriptor.
The setter resolves the existing definition under the same lock order, releases
publication locks, and captures the effective owner's live `Presentation` after
the demanded REAL child. Its descriptor's float4 output feeds ordinary
`applySetting` with `SettingSet`; parsing, access, cancellation, restricted and
parallel guards, journal publication and invocation refresh remain shared with
SET. The result re-reads the effective typed value, never the returned SHOW text.
Catalog replacement, extension removal, rollback and DISCARD do not unload a
physical session or reset its threshold values.

Routine and persisted setting validation use the same effective definition
lookup as SET/SHOW rather than bypassing loaded definitions through the shared
engine registry. Savepoint and exception rollback prepare InvocationMark images
before final publication locks; final restoration only installs already typed
independent candidates and cannot parse, allocate or re-emit load warnings.

## Contrib runtimes

The amcheck, `verify_heapam`, pageinspect, pgrowlocks, pgstattuple, pg_surgery,
tablefunc (crosstab, connectby, normal_rand) and generic file runtimes are the
leaf [`core/contrib`](contrib/README.md). Core owns `contrib.Host` on
`statementHost` (`contrib_statement_host.go`), the `contrib.Authority` that
`AuthorizationView` implements, `contribHost`'s `exec.ContribRuntime`
adapters and `PageinspectNotice`, and these host pieces:
- `AcquireRelationContribution`: the temporary relation contribution wait,
  dropping the replacement read around a queued wait;
- the pgrowlocks and `verify_heapam` readmission fences
  (`contrib_readmission.go`);
- pgstattuple's heap-statistics owner cohort (`heap_statistics_cohort.go`):
  live owners locked in session-token order, then `Engine.Mu`, with the
  prepared transactions' roots;
- visibility capture (`pgvisibility_capture.go`) shares that genuine owner cohort
  and pairs advertised snapshots with actual CommitSeq frontiers. Caller RC uses
  its statement pair; stable peers use retained pairs, and unknown advertised
  peers prohibit positive universal proof. Catalog heaps use the existing
  immutable catalog/RoleRead producer capture with zero Keep, not SQL or
  fabricated version headers. Exact tuple-maintenance sampling happens only after
  releasing the cohort and Engine mutexes.
  The independent consistency horizon follows running assigned full XIDs, not
  read-only advertised snapshots. Real incarnation VM roots live on storage;
  producer-backed catalog heaps use descriptor-anchored RelationMetadata without
  invented tuples. Final cohort readmission fences physical facts, map
  truncation/removal, descriptor, domain/epoch, storage and headers; a moved
  READ horizon or a peer-installed root only withholds publication.
- freespace capture uses the caller's ordinary retained data view rather than the
  dirty-statistics cohort. Existing catalog producer sizing and genuine index
  Allocation roots are shared through the leaf host, with no new store or global
  relation_size cutover. Owner/Engine fences protect identity capture, never row
  arithmetic, producer iteration or callbacks.
  The entered freespace wrapper's genuine relation-size child borrows that same
  worker with an explicit nullable wrapper-extent purpose. Its checked
  `FreespaceLayout.Blocks * 8192` is only this documented technical page estimate;
  missing numeric OIDs give size NULL and genuine no-storage kinds size zero.
  Neither path counterfeits C2 admission or entry; actual emitted blocks keep
  their separate C2 probes and all existing currentness/cancellation fences.
- `WithMaintenanceOwner` (`maintenance_environment.go`), the serial
  operation-local table-owner frame that amcheck (`CheckBtreeAsOwner`) and
  CLUSTER and FULL index rebuilding share, and `refreshRestrictedPresentation`
  (`amcheck_presentation.go`);
- `GenericFileAuthority`: the routine principal's `pg_read_server_files`
  membership and the path settings, borrowed in Engine -> Settings ->
  Session order.

Persistent exact-version freezing belongs to the relation's immutable header
root, not a transaction's write journal. Cohort capture includes actual aborted
and deferred-discarded versions as well as live and prepared owners; no payload
is recreated from a supplied TID. Old captures retain their original headers.
The repair's scoped relation contribution is independent of ordinary tuple and
relation ticket ownership, so clearing visibility cannot release peer resources.

`WithMaintenanceOwner` supplies this one scoped owner environment: current
routine authority, activated execution, nested restricted frames and safe
`pg_catalog, pg_temp` lookup. Callbacks run without `Engine.Mu`; all operation
presentation/settings state is restored on success, error and cancellation.
Admitted physical heap rewrites prepare fresh catalog locators alongside their
existing publication candidates: journal `StorageChange.Rewritten`, direct
`RowShape.Rewritten`, or the direct materialized refresh replacement. A changed
Go store pointer alone is not a new physical incarnation. Metadata-only column
projections and ordinary descriptor-only edits preserve locators.
The descriptor-owned ON COMMIT DELETE clear is likewise in-place: its empty
heap/index transport preserves locators without claiming physical rewrite or
uninterrupted TRUNCATE lineage.
Actual heap replacements publish locator-only clones of their exact staged
definitions rather than preserve descriptor pointer equality. Retained descriptors,
tuple roots and defining metadata remain unchanged. REINDEX separately stages
revision+1 and an owned ready/valid/live index-definition clone alongside its fresh
locator; key/include/exclusion backing and constraint/predicate providers stay
identical. Native assertions check these two exact transitions separately.
Foreign catalog replacement clears source locator overrides on its private
principal-remapped descriptor projection, before retained TEMP owners are merged;
the source catalog is never mutated. Imported heaps construct zero retained family
and truncate boundary. FULL and both genuine concurrent-MV publication paths
preserve actual predecessor read provenance; pure TRUNCATE assigns its new boundary
only at the successful canonical commit/prepared-completion frontier. A preceding
private destructive rewrite cannot inherit the old family through TRUNCATE.
Stable new commands distinguish same-boundary old-root continuity from crossed
TRUNCATE current-root birth filtering. `captureDataRootsAtLocked` supplies
`SameStableReadLineage` only for new stable-root routing by current name/current
ACL; valid rename/owner changes preserve that read only when family, equal
boundary, actual row shape and View/RowType/TableMethod identities still match.
Current SQL schema/privilege/catalog admission stays outside the predicate.
The separate `SameLineage` callback retains
strict defining authority for `ValidateView`. Bound query-source/publication and
domain/epoch fences remain separate and unchanged.

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
lexical admission and restores the exact outer `MaterializedSlot` and typed
`TruncateScope` manifest pointers once on exit, including the suspended caller's
exact backing. Each nested command starts with no TRUNCATE admission scratch:
prior Relations/Names/storage references cannot leak into its fresh selection,
and sequential commands admit current private storage. The shared
catalog/statistics journal and physical effects remain root-owned. Exception
checkpoints retain their independent rollback snapshot; transaction rotation
discards obsolete utility admission instead of restoring old storage authority.
The exact descriptor/storage guards remain authoritative for genuine replacement.
Volatile nested commands snapshot complete projected private roots instead of
merging their removed entries with older transaction writes. Thus AFTER TRUNCATE
reads the staged empty target even when earlier commands wrote it in the same
transaction; read-only routine policies retain their calling-query frontier.
Activation detaches only deletion roots still borrowed from the owner-mutable
statement stage, including both physical-collision maps. Retained nested data
views therefore keep their exact deletion frontier through later stage writes
and exception rewind; already immutable unrelated roots remain shared.

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
unwinding and later adapter callback delivery remain separate. A frontend
`CopyProtocolHooks.Notices` receives a top-level DO block's final notices as
each RAISE completes, through `protocolSimpleIO`; only the DO's own frame outside
PL subtransactions streams, where no outer executor or notice range is open.
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
Normalized CTE rules retain original lexical target contexts for locks, ACLs and
name validation. Final data discovery excludes an original target only when the
exact CTE rewrite selects a different mutation context; actual rewritten targets
and surviving source occurrences still enter the shared capture frontier.
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
Simple-expression classification uses scalar query structure, not volatility or
fold/runtime dependence. A genuine installed scalar can run nested source SQL
without giving its enclosing RETURN/assignment an extra PL expression frame;
actual subquery, set-source and aggregate/window/order/group slots remain SPI.
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
immutable event vector for index conflicts, FK checks and user constraint triggers.
Index events retain exact admitted row/version identities after a potential
conflict. FK and user payloads retain accepted images and immutable provenance;
user payloads retain only an optional RI principal, never an authorization view,
scope or execution frame. Savepoints retain modes/events and the incumbent dirty
projection together. Append and consumption detach membership, so rollback also
restores eligibility of an older event consumed after the mark.
Batch INSERT, row-at-a-time insertion and replacements all stage the physical
identity reserved by that same projection. Definitions requiring a reservation
must supply it; allocating a second identity would orphan the pending event and
make later deletion or replacement unable to retire its exact incarnation.
Command completion checks due indexes, executes immediate RI actions, checks
action-generated indexes, then enlists/checks due FK work before AFTER bodies.
Nested completion consumes only its accepted command's events.
Completion borrows retained event membership until an event is actually appended
or consumed; unrelated deferred obligations do not detach the vector. Unchanged
parent-key transitions also skip RI row-inventory capture. Actions still run
between the producer and action-generated index checks.
SET validates every name before installing candidate modes, then drains against
the live state. Callback appends and nested SET overrides are never overwritten.
Top-level and nested dispatch share this executor; description is effect-free.
Actual routine activation retains timing even in an implicit CALL/DO transaction,
without an outside-block warning. Ordinary nested return preserves modes/events.
SQL/Go COMMIT, CHAIN, PL completion and PREPARE settle user bodies and their
generated ordinary/index/FK effects to a fixed point, unlocked. Actual staged
rows, deletions and catalog/authority join the existing private segment before
one combined publication. PREPARE retains completed effects, not executable
events. Transferred segments cannot merge or publish twice; a terminal failure
clears the old incarnation and grants without exposing earlier body effects.
A borrowed native terminal scope attaches the real operation lease through
settlement and publication, without starting another statement or timeout.
Its admitted writable stage, current snapshot and before-callback catalog
authority are the same owners reached by nested SQL/DDL; completion restores
the original lease before releasing the borrowed scope.
Finished ordinary scopes may retain their immutable catalog journal for held
observers, but never transient publication authority. A journal already shared
with the transaction owner is not new segment work; staged suffixes and events
still transfer normally.
Procedural COMMIT/ROLLBACK, including CHAIN, drops the old scope index slot after
releasing its scratch, so reattachment uses fresh definition defaults rather
than copying old overrides or events into the new transaction incarnation.
`HasPendingConstraintEvents` reads the effective common vector, unfinished
qualified constraint ROW frames and every active constraint-invocation ancestor
without allocation, locks, liveness probes or consumption.
An exposed ROW frame membership marked consumed is no longer pending; any
transferred vector event or still-active invocation remains independently guarded.
The call-owned LIFO relation stack reuses capacity, clears each popped entry, and
is independent of
transaction/savepoint/PL frame snapshots: rollback cannot hide or resurrect a
still-live invocation. Every event protects only its
physical transition relation (`Parent`), even after trigger drop or row repair,
until completion consumes it. Common ALTER/default/CLUSTER/TRUNCATE admission and
actual catalog retirement closures use this protocol before installation.
Every executing constraint ancestor also owns an active relation descriptor.
`ConstraintPendingPreflight` reports that active use before pending-only events,
using the same diagnostic as retained cursor sources without scanning portals.
Callers retain their own cursor-use policy: `RelationPendingPreflight` adds the
incumbent retained-cursor guard for TRUNCATE/CLUSTER, while metadata ALTER admits
native eager-cursor snapshots. DROP closure CLOSE ownership exempts only cursor
sources, never executing trigger descriptors.
Separately dispatched trigger/constraint rename and trigger drop remain allowed.
DISCARD ALL's retirement stage records its ownership of CLOSE at the successful
frontier. Only cursor pins receive that exemption; pending constraint events
remain fenced, and staging neither closes cursors nor consumes their events.
Stable FK crosschecks use supporting-root pointers captured with the original
owner snapshot. A retained parent match after a current miss is the
isolation-specific serialization error, not permission to publish a dangling
reference. Provider comparisons and transaction waits run outside engine,
owner and session locks and replacement reservations.

PL lexical expressions use the existing closed-immutable folding policy before
that handoff; PL-local, parameter, row, domain and volatile demand barriers remain.

## Statement statistics

`DatabaseCluster.StatementStatistics` owns one bounded logical accumulator across
databases and physical sessions; independent Drivers never share it. The built-in
module is available at cluster startup, without loading PostgreSQL C libraries.
Database-local installation creates genuine stock 1.12 functions and ordinary
views, not a new collector or another set of session settings.

Canonical command admission captures database, nesting, normalization and
instrumentation policy before the collector leaf. The scanner returns both the
normalized representative and the same cleaned original source span in one pass.
Successful storage preserves an existing first representative; an absent entry
uses the cleaned original source. Planning and execution are separate phases. Actual
preflight timing starts after analysis/import; completed construction is sampled
only after exact-attempt/cancellation/fold publication fences. Native has no
cost-based generic/custom prepared-plan chooser: repeated actual builds are
counted, even under stored `force_generic_plan`; an accepted portal continuation
does not invent another build.
Planning-only CTE/DML Bind retains the accepted measurement without an executor
completion. Its later Execute counts its genuine fresh native construction;
plain SELECT instead retains the already initialized producer across messages.

Execution uses actual command completion or producer work/disposal, not
transport tags or reads of materialized output. A portal retains its measurement
through suspension and exhaustion until normal disposal. Closing an initialized
but unused SELECT records one successful zero-row execution without demanding
its body; closing a planning-only portal records no execution. Partial normal
close does not drain. Finish rejects failed, canceled and late-failing frames, while a
completed nested command remains independent of a later parent failure. Utility
wrappers use `Result.UtilityRows` for actual processed rows, independently of
`Count` and displayed command tags: REFRESH retains zero affected rows while
reporting its population query's count, including unchanged concurrent refresh.
Native and wire COPY share admission and completion in `copyProtocol`; the same
frame's report-once fence and final Finish acceptance prevent duplicate or failed
calls. COPY(query)'s independently analyzed executor has its own nested frame,
using the whole COPY source span. Its existing eager or streaming producer is
observed through actual output and close without changing protocol scratch;
`track=all` can therefore retain both real wrapper and child completions with
distinct identities. `top` and utility suppression still admit only their
configured frames. SQL PREPARE/EXECUTE charge the body rather than wrapper telemetry.
Nested PL commands borrow exact parser-owned expression/query intervals, with
the existing INTO redaction and PERFORM-to-SELECT coordinate shift. They never
borrow the outer invocation text or the entire PL program as representative SQL.
Scalar source spans annotate the existing actual SQL execution path; collecting
them adds no evaluation, executor frame or synthetic completion.
Lexical locals and record fields use their actual local/composite expression
bindings, not fabricated external parameters. Constant placeholder numbering
therefore follows the external parameters genuinely present in that binding.
Named cursor-argument projections reuse the existing declaration-ordered source
builder and exact original-operand byte mappings, including formal AS labels;
normalization never confuses character offsets with UTF8 byte intervals.
Pending normal portal completions follow lexical scratch across routine
transaction rotation until the actual enclosing Finish accepts them.
SQL/PL cursors transfer the same accepted frame into their existing producer.
Live motion counts actual produced tuples, including skipped motion, rather
than FETCH tags. Normal close or HOLD producer retirement ends that frame;
reading a promoted store cannot execute or charge it again. Failed motion,
abort and savepoint disposal reject the unfinished frame, while a child already
normally closed remains successful if later parent work fails.
Transformed utility children and CALL arguments obtain their jumble only from
the existing real binding, never a raw-query substitute. Enabled utility collection
owns one wrapper completion; directly contained analyzed utility children do not
duplicate it. Independently analyzed COPY queries retain their genuine nested
executor. With utility collection off, optimizer children are admitted in their
utility's nested context; retained executor completion uses its actual later
terminal context, not the admission's top-level bit.

Ordinary and nested executor terminals capture effective tracking policy before
their lexical settings environment is restored. Function-local SET restores the
caller; a session SET can escape function configuration. Utilities instead retain
their early hook-enable decision and capture the successful hook-time principal.
Named role/session-authorization and DISCARD ALL owners supply their one genuine
decided target to `AdmitUtilityStatementStatsLocked` before mutation: early hook
timing is not collector admission. Normalization and original/prospective keys
are admitted together there, without repeating authorization decisions.
Transaction-control utility provenance is captured at command-control acceptance,
before command-end role/GUC restoration, not from the later response-time user.
Scope-owned utility-body state distinguishes SQL utility execution from frontend
portal Close and command-end transaction cleanup. Lexical activations save that
state and begin a fresh child context, without counting the parent twice.
Executor terminal policy and successful `TopLevel` use the same current context:
SQL CLOSE remains nested even with utility tracking off; command-end COMMIT/HOLD
disposal can split a nested planning entry from a top-level execution entry.

Each live runtime frame owns one scalar collector admission and releases it on
every terminal, including rejection, unused/planning-only disposal and owner
cleanup. Transfers move that frame rather than minting another admission.
Eviction removes history, not eligibility for a later successful phase; matching
explicit clear/replacement still fences live admitted/prospective keys even when
their entries are absent. Unanticipated terminal keys register atomically with
their successful record; earlier clears of only that unregistered key are not
retroactive, and another prospective key's reset/extrema flags do not transfer.
The original admitted-key destructive fence always remains effective.

Projection recaptures current role authority before the collector snapshot;
own/superuser/inherited `pg_read_all_stats` readers see text and IDs, while foreign
ordinary readers receive redaction and NULL IDs. SQL reset overloads are owned
separately; the real shared filtered/minmax clear seam already exists. Accepted
native replacement clears only the replaced database's history, retaining peer
history and owned snapshots. CREATE DATABASE's unpublished template-copy runtime
has no cluster or registered database identity and clears no shared history.
No runtime frames, history or handles are dumped.

## Logical buffer-cache ownership

`DatabaseCluster.buffercache` owns a lazy sparse read cache shared by that
cluster's databases; independent Drivers never share it. Capacity comes from the
immutable raw `shared_buffers` setting. Slots identify genuine committed catalog
domains, epochs, relation addresses and effective storage locators. TEMP,
virtual/provider relations, foreign sources and storage-less partition roots
cannot acquire occupancy. Actual reached physical leaves, including consumed
hash/bloom indexes, own source read leases; binding and source capture do not heat.
That capture now composes independent pending-block accounting with the optional
cache pin. Statistics can count genuine private/TEMP/rewritten storage without
granting any shared cache eligibility; the cache's committed locator checks,
occupancy and pin lifetime remain unchanged.
Sampled-source initialization retains normal identity and authority checks but
does not activate storage. Only positive sampling that consumes a nonempty child
opens its lease; zero samples leave cold occupancy and warm usage/pins unchanged.
The opened stage retains its read frontier across rewind, while a fresh positive
invocation increments usage without duplicating its capture-owned session pin.

CREATE DATABASE replaces its template into a private runtime with no Cluster
before publishing and attaching the destination. Such metadata publication has
no shared cache to reconcile, and captures acquire no live read lease. Late lease
activation checks owner absence before registration/generation lookup. A provider
observation requires the actual registered cluster runtime and returns an explicit
ownership error otherwise, never successful empty rows or a synthetic private
cache. After attachment the destination starts cold; only its subsequent real
reads heat its database identity. Independent Restore staging caches are not
transplanted.

Each fresh actual source invocation increments usage to five; rewinding the same
stage does not. One main-fork representative block per relation, clean logical
read state and distinct source-owning session pins are technical cache metrics,
not PostgreSQL page occupancy, writeback or backend pins. Lowest vacant slots and
first unpinned circular victims make allocation deterministic; all-pinned reads
still succeed without inventing an occupied slot.

Lock order is statement/session authority, cluster metadata mutex, source-lease
mutex, then cache leaf mutex. Close takes only source/cache leaf ownership, never
engine authority. Sparse publication reconciliation retains metadata-only changes
and retires replaced storage/database identities. Successful Reset/Restore drops
only target live cache state; failed replacement preserves incumbents. Late old
leases cannot alter replacement entries. Dump serializes ordinary installed SQL,
membership, owners, current/initial ACLs and user data, never cache state or tokens.
Provider cursors contain detached scalar snapshots and lazily synthesize unused
slots with all eight unavailable fields NULL; no source/session/catalog root
escapes in a row.

### Table block projection

`catalogRuntime.TableBlockStatistics` reads the qualified relation Fact once
through the existing transaction Reader and returns `(BlocksFetched-BlocksHit,
BlocksHit)`. The ALL/SYS/USER table-I/O sources share current authorization and
effective-settings capture, not prepared evaluated rows. No collector, cache,
pending-write readback, forced flush or source-specific snapshot is added.
The existing actual-work sourceRead lease records one fetched and resident hit
per reached nonempty invocation, including private/TEMP sources independently
of shared-buffer eligibility. Thus N published requests give heap `(0,N)`,
not first-read-cold arithmetic, tuple counts or physical page estimates.
Its index aggregate sums existing per-index read/hit projections once per real
parent relationship. No indexes yields NULL, unused real indexes zero. With no
TOAST relation, both direct toast and tidx counters are NULL; no fake descriptors
are added. Zero defaults apply to valid relation identities without statistics.
Counts gating, timing independence, idle throttle, physical-session drain,
Reader clear/cache/snapshot and retirement remain their existing owners.
Namespace and Keep rejection precede all Reader demand. SYS and USER partition
the same real ALL identities; their own live ACLs remain independent of
underlying schema/table privileges and RLS. Retained eager results own immutable
typed cells across clear, descriptor changes and catalog replacement.
`TestStatioAllTablesReader` composes real source Opens, publication, reader modes,
clear, policy gating and private/TEMP identity lifetimes; it also supplies
F7/H5 to independently prove heap subtraction is not hard-coded zero.

### Index block counters

The selected `pg_statio_all_indexes` source enumerates actual SQLIndex-parent
relationships with parent relkind r/t/m, without underlying-object ACL or
temporary-owner filtering. Its own view ACL remains ordinary SQL authority.
Typed nullable catalog attributes yield actual non-NULL identities and two
zero-default bigint facts; no fake bootstrap/TOAST descriptors are installed.
`pg_statio_user_indexes` uses that same authenticated capture and producer,
excluding parent namespaces pg_catalog/information_schema/prefix pg_toast before
Reader demand (case-sensitive, not OID or ownership). Its own live SELECT ACL
does not redact underlying denied objects. The named SYS sibling remains absent;
none/cache/snapshot authority and other views' absent-activity NULLs are unchanged.

`logicalBufferRead.Open` on a genuine eligible index invocation records one
representative-page outcome: resident-before-touch increments IndexBlocksHit,
otherwise IndexBlocksRead, including a failed cache admission. The leaf cache returns
that outcome atomically with opening/touching; provider collection occurs only
after releasing the cache leaf. Already-open valid entries touch without a
second pin but still record each invocation. A previous nil admission retries
and remains a miss until admitted. Stage rewind does not itself invoke Open.
No new physical I/O, bytes model, cache, event owner or timer is introduced.
The integrated sourceRead lease retains its independent BlocksFetched/BlocksHit
request accounting. Only eligible actual SQLIndex buffer captures carry the live
operation and qualified index reference for these separate cache-outcome facts;
generic requests, including excluded TEMP/private requests, cannot populate them.

The capture-owned bridge retains only qualified index identity and the existing
operation authority needed for live EffectiveSettings (including proconfig and
SET LOCAL), never a bound query/evaluation graph. Source lifetime permits
Close→Open reuse; Close releases pins, while source disposal releases operation
authority. Transaction admission serializes its existing session Writer.
Counts—not IO timing—gate accepted deltas. Reads are nontransactional facts,
not tuple Ledger entries, and use normal pending publication and retirement.
Counts-off accesses still perform the same eligible cache open/touch; a cold
counts-off access followed by a counts-on access therefore records only a hit.
Eligibility is unchanged: hash/bloom candidate capture requires `scope.implicit`
and no private catalog. An explicit transaction's existing heap fallback is not
an index event, even when sequential scans are discouraged. Public policy
witnesses use autocommit accesses; composed LOCAL-policy coverage applies the
ordinary settings authority to an already eligible captured lease.
`catalogRuntime` reads both index blocks and table statistics through the same
Reader, snapshot timestamp, configuration generation and transaction terminals.
Keep rejects identity/name keys before the first fact lookup.

Existing TEMP/private-created/private-rewritten/template-runtime exclusions
remain: represented identities are still visible to their captured catalog,
but excluded accesses add no shared-cache counts. A private rewrite retains
published history; its next eligible committed incarnation is cold. Metadata-only
changes keep history; committed drop and replacement use existing qualified
retirement/generation fences. Held rows remain detached values.

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
Template copies use pointer-qualified `DatabaseTemplateLease` readers under
AdmissionMu. Acquisition holds shared Mu first, counts every reserved/consumed
physical owner and detached prepared transaction, and excludes only the exact
consumed executor Session pointer. Last-reader release closes the source's
notification generation. `AdmitDatabase` waits on that generation or cancellation
with neither lock held and reserves nothing until readmission. Cleanup/release
takes AdmissionMu only. Source replacement read protection is acquired outside
Mu/AdmissionMu and retained through copying/publication; a current-source statement
reuses its existing reservation rather than recursively entering behind a writer.

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

Creation copies the actual registered source's committed persistent catalog,
rows, indexes, sequences and dependencies into fresh local authority, preserving
SQL-local object OIDs and shared principals. Registered template1 supplies default
contents; only an unregistered template1 uses the lazy private UTF8/C builtin
seed. Mutable storage backing is independent, while immutable typed payloads may
share their established portable backing. Object owners/ACLs survive, including
destination-relative `pg_database_owner`; database ACL/configuration does not copy.
Source authorization, exact runtime identity and replacement epoch are checked
again before atomic registration; compatible copies may publish shared roots.
`ReplaceDatabaseTemplate` admits only a fresh private destination and shares the
ordinary replacement helper's graph validation/remap/install. Its initial base
excludes source TEMP objects, preventing the live-destination retention overlay
from adopting source authority. The import retains TEMP catalog metadata, while
row/index/sequence/statistics source capture excludes physical TEMP contents.
Copied namespaces/toast groups receive destination-qualified opaque detached
session tokens; empty private stores, initial generators and method-bound empty
index roots belong to the destination. Same numeric live or previously detached
owners remain distinct on re-clone. Detached metadata is not retained as a live
destination consumer during Reset/Restore; ordinary live TEMP retains its exact
ownership, backing and dependency protections.

The CREATE DATABASE executor and its option rules are in
[`rolecmds`](../rolecmds/README.md#database-creation).

`pg_database` captures every registered target in this cluster, never other DSNs.
Each row has its own stable identity, owner/ACL and runtime freezing horizon.
Encoding 6, provider `c` and C/C locales are immutable semantic defaults.
`datistemplate`, `datallowconn` and `datconnlimit` project the real CREATE policy
(defaults false/true/−1), retained across replacement. Physical placement and
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

`pg_shdepend` retains immutable per-target roots with the current catalog and role
pair at initial statement inquiry capture under the shared mutex. Every source
occurrence reuses that frontier, even if peers publish new roles/objects or register
databases before source opening. Classification runs after unlocking through
canonical access visitors. Current inquiry authority includes private definitions;
other targets contribute only their captured published persistent roots. The
single-database inventory uses inline backing. Class-based sharedness
selects one copy of replicated database/subscription/parameter descriptors with
dbid zero; local addresses retain database OIDs even when object OIDs collide.
Only narrow immutable dependency facts cross into pgcatalog for typed encoding.
The capture neither mutates source catalogs nor retains live runtime callbacks.

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
| `statementHost` (a `StatementScope` conversion; `statement_host*.go`, `*_runtime.go`) | `exec.Host`, `exec.NestedSQL` (`nested_sql.go`), `tsearch.Host` (`tsearch_statement_host.go`), `contrib.Host` (`contrib_statement_host.go`), `stats.Host` (`statistics_hosts.go`), `inquiry.CatalogHost` and `inquiry.SessionHost` (`inquiry_statement_host.go`), `dml.Host`, `dml.FinishHost`, `index.WriteHost`, `partition.RecipeHost`, `fdw.WriteRuntime`, `regobj.NoticeHost`, and the bodies of the `functions` evaluation capabilities (`ACLRuntime`, `AdvisoryRuntime`, `BlockingRuntime`, `SequenceRuntime`, `LargeObjectRuntime`) that `*exec.Context` forwards |
| `contribHost`, `textSearchHost`, `catalogInquiryHost`, `sessionInquiryHost`, `transactionInquiryHost` (`StatementScope` conversions; `host_capabilities.go`) | the `exec.Host` capability groups `exec.ContribRuntime`, `TextSearchRuntime`, `CatalogInquiryRuntime`, `SessionInquiryRuntime` and `TransactionInquiryRuntime`; `statementHost`'s accessors convert to them; `contribHost` and `textSearchHost` enter `core/contrib` and `core/tsearch`, the three inquiry groups `core/inquiry` |
| `DDLHost` (`ddl_host.go`): the core base, embedded by every family host | `ddl.Host`, `ddl.Allocator`, `index.KeyHost`, `table.TypedResolver` |
| `BindingHost` (`analysis_host.go`) | `analyze.Host` |
| `catalogRuntime` (`catalog_runtime_host.go`), which also holds the `catalogrt.Runtime` | `catalogrt.Host` |
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
the stage (its `extensionNamespacesOf` collection retains schema observations).

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

Multi-owner physical capture discovers owner pointers under `sessionMu`, drops
it, and acquires distinct owner mutexes in ascending session-token order
before `Engine.Mu`. The cohort is revalidated before borrowing roots; a changed
cohort is released completely and recaptured. No owner mutex is acquired
under `sessionMu` or `Engine.Mu`, and no peer Admission or completion is awaited.


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
run unlocked once on final images. Query drain preserves alphabetical mixed
ordinary/constraint order: immediate bodies fire there, eligible deferred user
events transfer to the common vector before the frame ends. Rows precede
statements; no deferred body executes at ROW admission.
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
drain cursors while sharing immutable tuple and target payloads and preserving
suspended frame identity; effect serials never rewind. Finish releases
all queue captures on success, failure and cancellation. No callback holds engine
or owner mutexes, and trigger-body SQL uses the existing private stage.
Each AFTER invocation retains the current immutable function descriptor validated
under the engine mutex, then executes it unlocked against the current routine
catalog. Ordinary queued definitions keep their admitted name. Deferred user
invocation resolves the live trigger OID/name first and skips missing identities,
while physical relation metadata, images, targets and accepted WHEN stay tied to
admission. Neither disable nor replication-role changes requalify accepted work.
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
- Statement-statistics planning counts describe actual accepted native builds,
  not PostgreSQL generic/custom cache frequencies. Neither measurements nor
  settings introduce a definition-owned live graph or a second plan cache.
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
- Export-root demand is immutable bound recipe metadata, propagated through
  query/RETURNING/CTE, retained writes, views, policies, triggers and consumed
  routine closures. A demanded RC admission captures complete committed roots,
  public XID value, tuple frontier and catalog in one publication-lock window;
  ordinary RC retains its existing narrow target pins. Evaluation never takes a
  newer root map. Stable isolation shares owner backing. Fastpath and genuinely
  opaque executable boundaries conservatively demand before their active cut.
  Before default preflight, Execute additionally borrows only its actual portal's
  accepted set after transaction/fold-generation checks. Exec's pure selected
  executable projection preserves SQL predecessor demand even when the current
  catalog recipe is quiet; bounded PL demand follows its authenticated current
  provider. This never grants execution, changes backing or retrofits a late cut.
  Lexical routine scratch owns the active pair: readonly children borrow it,
  volatile RC commands capture a fresh one, and unwind restores only the live
  incarnation's pair.
  Direct CTAS is the bounded unbound-utility exception: source grants are acquired
  once before the utility/event pair. Conservative roots are captured after that
  wait and before ddl_command_start or producer Eval, without a second binder.
  Stable analysis retains its existing owner cut. REFRESH instead
  classifies the stored query, nested views and current routine dependencies in
  the capture lock window. WITH NO DATA adds no source demand. Target/source
  grants revalidate defining authority and refuse newly export-bearing closures
  rather than retrofitting roots onto the earlier cut.
  CREATE TABLE PARTITION OF has the same enumerated unbound-expression
  exception; unrelated utility families do not inherit it. Before canonical
  source-principal admission, retained read demand includes possible SELECT/ALL
  USING policy recipes on the consumed source graph. This pure, cycle-bounded
  metadata classification never selects policies or changes runtime RLS bypass,
  role checks, diagnostics, or effects.
  Already-bound ordinary read sources reuse this retained view/policy closure
  before final snapshot capture; analysis-only sources and disabled RLS add none.
  Current routine closure is demand metadata, not permission to replace strict
  retained routine declaration authority or to execute a new body through an
  old callable carrier.
  Matching enabled start/end DDL event closures also contribute before the
  utility pair, using the current persistent event-trigger index, tag/replica
  matcher and event_triggers setting. WITH NO DATA suppresses source execution,
  not event execution. Each actual later phase fences newly export-bearing
  closures when its old pair has no retained export roots.
  ALTER's historical row-validation/rewrite frontier is separate from its
  genuine executor-expression snapshot. Before runtime DEFAULT preflight or
  row CHECK/generation/USING evaluation, the cooked possible closure includes
  retained descendant preparations, domain checks and represented routines.
  Still-unbound descendant executable phases and not-yet-selectable index
  rebuild execution contribute bounded conservative demand without rebinding
  children, moving parent evaluation, or adding an eager index catalog scan.
  The existing late index selector acquires/validates the same active pair
  before opening actual key/predicate recipes. Metadata-only and empty scans
  do not enter merely because ALTER is their statement family.
  Actual selected ALTER index builds enter the existing restricted table-owner
  frame only after selection and snapshot admission. PL diagnostic visibility
  retains the exact immutable Procedure's first-compilation signature in that
  session: a warm signature is reused, while a cold restricted compilation
  retains its qualified spelling after unwind. Returning restores the caller's
  invocation authority and settings without rewriting that diagnostic cache.
- `snapshot_sync.go` owns the lazy cluster token registry under the existing
  shared engine mutex. Records contain committed authority only, never private
  journals, locks or SSI graphs. Source end and import installation linearize
  under that mutex without taking a peer owner mutex. Existing end/fence paths
  retire source tokens; imported immutable references remain owned by importers.
  Explicit whole-block abort drops roots and running advertisements immediately,
  retaining only nonimportable identifier/source metadata until block cleanup.
  Import then reports 55000 with the source backend identity; after explicit
  ROLLBACK/COMMIT cleanup the identifier reports 42704. Implicit failure,
  PREPARE refusal, physical end and native replacement clean up immediately.
  An already admitted old incarnation returns the ordinary native epoch fence,
  not a missing-token SQL diagnostic; only a fresh eligible transaction sees
  42704 after replacement. Cancellation during staged Restore input leaves the
  old epoch and its complete export authority intact.
  Import installs the old data frontier but initializes SSI SnapshotOrder from
  the current tracker. READ ONLY DEFERRABLE rejects instead of acquiring a fresh
  safe candidate. Sticky owner export status participates in PrepareRefusal
  after temporary-object refusal and survives child rollback.
- Incoming FK action demand uses the catalog's persistent derived owner index.
  Core inspects only matching enforced mutating UPDATE/DELETE actions and their
  typed child defaults/generation/CHECK/trigger/routine closures, recursively
  following cascades with exact relationship/action cycle identities. No
  authorization or effect is executed early; NO ACTION/RESTRICT and unrelated
  no-FK/no-row commands acquire no candidate/visited collection or new catalog
  prewalk. Canonical direct/prepared admission passes the actual statement to
  classify DELETE independently of suspended mutation scratch. Each reached
  writable RI command takes its own fresh RC public/frontier/root pair before
  default planning or trigger evaluation; STABLE callees borrow that generated
  command pair. Stable isolation borrows the transaction pair. Balanced unwind
  restores the parent's pair, target roots, view and current advertisement.
  This is genuine nested command acquisition, never a retrofit onto an old cut.
  Mutation demand also visits each already-admitted `InheritedTargets` leaf of
  the matching command occurrence, including modifying CTEs. Descendant-owned
  generation/CHECK recipes, triggers and incoming actions therefore participate
  before capture without a second hierarchy/catalog walk. Known UPDATE columns
  map by name to descendant attributes; unknown sets conservatively include
  UPDATE OF triggers.
- SQL EXECUTE actuals bind/count/type-check before obtaining their own demanded
  pair, and capture before any runtime preflight or evaluation. Their consumed
  routine manifest stays distinct from the prepared body's authority. On unwind,
  current snapshot/target advertisement is restored without revoking actual
  export records, so a later RC body source still chooses its own admission cut.
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
  Accepted-portal relation admission additionally recognizes only an exact
  stored-owner scalar-publication predecessor chain to the current relation.
  Analysis retains the original relation pointers; ordinary `Valid`/`FoldsValid`
  are unchanged. This admits no changed callable/type/provider or fold key and
  never replaces an accepted graph, frame, dependency, group or generation.
  Reusable-definition invalidation remains separate from accepted ownership.
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
  identity, immutable `exec.AcceptedFolds` and a private accepted DEFAULT set.
  Its `PreparedResource.Root` is the real SELECT graph consumed by the cursor,
  with the existing unlocked terminal close capability, never an observation
  copy or a graph in a shared definition.
  Source/expansion stamps belong to each binder's immutable syntax, not a global
  portal sequence: independent bindings may carry equal ordinals. Their actual
  producer, graph and accepted-group holders remain distinct while borrowing the
  definition's same immutable fold backing. Rejected publication and definition
  retirement preserve each accepted binding's exact provenance and typed inputs.
  Retention assertions capture accepted groups and typed entries while the
  definition is live; a retired definition is not a reusable-entry lookup API.
  Producer retirement detaches the live resource graph/group without mutating
  the previously retained graph, accepted-group holders or immutable backing.
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
- Consumed column DEFAULTs transfer only their final owned typed scalar closure
  and original source/attribute/dependency/lexical occurrence evidence at final
  successful Bind admission. `prepared_defaults.go` selects the exact artifact
  after ordinary current binding, replacing only transient local DEFAULT fields.
  Direct INSERT, shipped domain-column UPDATE DEFAULT and normalized rule/CTE
  consumers share source-bound `PrepareDefault` through `analyze.DefaultOrigin`.
  The candidate reads its canonical source itself and records original lexical
  tokens before one incumbent preflight; no caller-supplied output can mint a
  retained recipe. Assignment legality uses each recorded DEFAULT's local origin.
  UPDATE transports that immutable original statement/expansion key through
  `dml.UpdateInput` into its fresh runtime evaluator; a default origin cannot
  infer or overwrite source-command ownership.
  Sealed root/call wrappers and the stateless preparation bridge authenticate
  paired relation/routine lineage and each source-minted closed invocation mode.
  Source mode binds the authentic original code definition and provider authority
  through the one actual preparation, never a language spelling or caller flag.
  `sql_routine.go` validates current authority and independently selects current
  owner environment and executable before strictness, policy, caches or dispatch.
  Standard/quoted SQL execute their authenticated predecessor; only the exact
  current-PL mode selects the freshly validated compatible current body. Original
  source-call/path lineage stays distinct from actual prepared call identity:
  dispatch requires its exact descriptor, metadata and immutable argument/
  assembly/signature backing. No prepared frame/input/Usage is replaced. Hidden
  defaults and current child-body commands retain independent admission manifests.
  Current CHECK/FK/index metadata and every ordinary declaration/fold dependency
  remain current and strict. No command graph, PreparedCTEProgram, Context/Host,
  frame, storage, grant or computed volatile result survives Bind. Close and
  transaction/savepoint/native retirement veto use, independently of reusable
  definition retirement and its publication rights.

Dictionary DDL staging belongs to `schemacmds` and publishes through the normal catalog journal.
Core supplies the shared compiled Snowball language runtimes, including Finnish, Catalan and French, to the catalog's
private seed installer. Allocated helper/template/dictionary/configuration
identities use the same admission, capture and immutable fold owners as simple.
`Engine.TextSearchFiles` is immutable Driver startup authority, not catalog state.
DDL capture retains that same capability before releasing `Engine.Mu` for
compilation. Logical databases, the private template seed, Restore stages and
Reset retain it explicitly; destination Restore never borrows source bytes.
`Engine.WALNames` is immutable arithmetic metadata, initialized from the exact
`wal_segment_size` registry boot value with embedded timeline 1. Binding reads
it by value without acquiring a lock. New logical databases copy the originating
runtime's value before binding; Restore stages copy the destination's value
before replay. Reset and catalog replacement neither serialize nor replace it.

The genuine sealed `unaccent` script initializer uses its current candidate
procedure/default label for direct FHET entry, without SQL EXECUTE admission,
while its original committed descriptor/label predecessors remain absent.
Detached compilation runs outside `Engine.Mu` and retains the exact original
Base, helper/template/dictionary and installation identities, roles and resource
capability. Completion compares candidate label-delta count and exact record
pointers against the captured candidate read, not the fresh persistent-map root
created by each `StageRead`. Retained nil delta keys make this an exact fence;
peer labels, replacement and source retirement are not recaptured as authority.
DDL initialization never warms the physical query dictionary memo. Failed or
discarded candidates restore only their own newer observation/completion suffix,
preserving earlier genuine initialization receipts.

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
- Named CLUSTER is a read-only-eligible transactional maintenance utility.
  Its real catalog preference is published under ordinary heap AX admission,
  without heap replacement, physical index locks or forced all-index rebuilding.
  Unchanged recipes preserve every heap/root identity. `WithMaintenanceOwner`,
  also used by amcheck, surrounds necessary targeted recipe/overlay evaluation
  with the restricted table-owner frame,
  initial `pg_catalog, pg_temp` lookup path and detached command-local settings.
  Input/output and callable presentation follow that frame; every exit restores
  the caller's identity, settings policy and presentation without canceled
  recapture. Later unrelated DML does not inherit maintenance authority.
  Private command regressions compare the complete settings/identity/timer image
  after transaction admission and before `Finish`, then check publication or
  rollback after completion. Ordinary admission and completion legitimately
  advance timer generations and retire setting journals; maintenance restoration
  must not rewind that transaction lifecycle.
  Existing-index reconstruction preserves compatible current routine volatility,
  including STABLE/VOLATILE changes under the same identity. CREATE admission
  remains separate and strict, including LIKE and newly synthesized partition
  indexes; structural publication does not relabel routines or waive current
  stored-program validation.
  Physical construction provenance, independent of reminted logical captures,
  determines necessary affected-index overlay work. Incremental updates and
  transport preserve prior recipe debt; successful full reconstruction starts
  new history. Unchanged roots and heap storage remain untouched. Original
  expression trees and retained roots remain immutable.
  `CompleteClusterTarget` publishes and retires each bare target's ordinary
  transaction/ticket; earlier targets survive a later failure. Named CLUSTER
  retains its ordinary transaction/savepoint lifetime.
  Validated unpopulated materialized views skip before changing descriptors,
  preference, population or storage, and restore the pretarget lock checkpoint
  outside metadata locks. Earlier ordinary/scoped holdings survive.
  Admitted target progress uses one pointer-owned CLUSTER/FULL entry per TxRef.
  Engine.Mu owns membership; entry-local locking owns changing facts, with
  Engine → entry capture order. Builder callbacks never enter the engine.
  Exact-entry cleanup precedes bare-target retirement and cannot erase a newer
  target. Catalog replacement detaches old entries and callbacks permanently.
  CLUSTER counts successful necessary index builds only after baseline/output
  attachment; absent heap work/index OID remain exact zero. FULL retains genuine
  rewrite counts. Detached eager captures and fresh role disclosure share the
  existing twelve-column view, with no unordered-result ordering promise.
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

GIN class/family presentation changes use the same private index baseline and
publication fences. The index owner certifies old/current support authority;
`catalog_overlay_indexes.go` retains a COW binding/root transport instead of
rebuilding keys or replaying earlier own-write expressions. Produced key backing,
construction debt and any prior routine-source receipt survive the transport.
Rollback/savepoints discard the new wrappers without changing committed roots.
Installed scalar extension relocation and membership are normalized through the
same open `CatalogStage`: the index owner certifies complete before/after cohorts
outside `Engine.Mu`, then core allocates/applies dependent key revisions under
the mutex before index evolution/history marking and final provider validation.
No validated stage or committed root is patched afterward. Only genuine
presentation/current-membership projections preserve executable support authority.

- `session.ExplicitUtility` admits the shipped COMMENT kinds (TABLE, VIEW, COLUMN,
  FOREIGN TABLE, SCHEMA, ROLE, SEQUENCE and EXTENSION) in SQL BEGIN and Go BeginTx.
  Describe remains effect-free; Execute preserves read-only and aborted-state
  precedence and refreshes authorization for held statements.

Object comments use `CatalogStage` and the existing private journal; shared role
comments use its immutable role root. Owners see private text, while peers,
including other databases for role comments, see committed text until combined
commit. Rollback and savepoints restore the same roots. Relation COMMENT keeps
its SUE ticket contribution until transaction end or rollback to an earlier
savepoint; RELEASE retains it. Waits validate the captured object identity,
never a same-name replacement.

Trusted Restore admits only local TABLE, VIEW, FOREIGN TABLE, COLUMN, SCHEMA,
SEQUENCE and EXTENSION comment forms for statement-local bootstrap authority.
It retains the replayed principal for namespace checks and never changes session
identity. Shared ROLE comments retain their separate owner path. Admission does
not replace the class-owned target-kind checks, ownership rules or lock lifecycle.

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
The stock pgcrypto enum is a private physical-session attachment, not a global
registry insertion. CREATE member validation and authenticated surviving
installed-call initialization enter `InitializePgcryptoLocked`; current
placeholder setter authority, typed history and owned warnings are published
through the session owner. The live statement host checks password policy using
the effective store and actual Go FIPS mode. Catalog replacement neither imports
physical module state nor resets the destination's effective policy. Persisted
and routine validators use the same active-definition rule as metadata and SET.
Initialization preserves the original node/ref/lifetime manifest, resolves a
compatible captured declaration through ordinary routine validation, and checks
the provider against that canonical current descriptor. Compatible owner/ACL/
remap revisions do not turn a historical recipe pointer into current authority.

`current_setting(text[,boolean])` and `set_config(text,text,boolean)` use
canonical typed setting expressions, shared fixed-formal binding and live
`InvokeSetting` session lookup/installation. Ordinary SELECT binding owns output
labels and function-name defaults; presentation does not alter invocation,
authority, NULL handling or setting effects. Each overload retains its own
builtin identity and live EXECUTE admission. `current_setting` is strict and
returns typed TEXT NULL for a NULL name or optional Boolean. The optional true
flag returns NULL only for unknown names; it does not bypass restricted-read
authority.

Installed ISN weak calls borrow `InitializeIsn`/`InvokeIsnWeak`, never a builtin
name alias. Authenticated execution initializes physical module recognition
before row demand, after strict-NULL elision; Prepare alone does not initialize
it. Getter reads the effective settings image; setter uses ordinary `SettingSet`
and refreshes live ISN input presentation without resetting pooled sessions.

## System catalog producer contract

Producers, the OID registry and descriptor specs live in
[`pgcatalog`](../pgcatalog/README.md); core keeps orchestration.

- `openCatalogSourceLocked` (`catalog_source_capture.go`) looks up
  `pgcatalog.ProducerFor` and chooses the per-OID catalog/role roots,
  presentation and unlock timing; it is not a second dispatcher. `pg_locks`
  owns its detached manager projection and lazy `pg_sequences` its sequence cut.
  Activity and SSL share multi-stage canonical capture and the common producer registry.
  Information-schema domain and column inquiry sources use the statement's
  paired catalog and authorization roots, including `column_domain_usage`,
  `domain_udt_usage` and `information_schema_catalog_name`; source opening
  preserves that pair rather than resampling publication. Domain/base usage
  applies enabled base-owner authority; catalog-name inquiry has no row filter.
  Both consume the inquiry-owned database scalar captured with CommentCatalog,
  not the retained authorization's mutable Session pointer. A legitimate empty
  captured name remains a non-NULL empty identifier, never a live-name fallback.
  Catalog-name inquiry also resolves its source descriptor and column generation
  from that captured root, even after live descriptor replacement. This row-input
  ownership does not replace ordinary current source SELECT admission.
  SQL cursors and protocol portals retain/restore that scalar with their inquiry
  authority and release it on producer teardown; unrelated source policies stay
  unchanged.
  Foreign metadata sources, including `foreign_tables`, carry that same pair
  with the current database name and temporary-owner authority. Shared runtime
  validation checks the exact source/ref and current principal before producing
  rows, even for an empty catalog.
  Restricted statistics-view capture additionally checks the current EXECUTE ACL of
  its metadata-only backing routine, after ordinary view SELECT admission and
  before the pure producer. Neither grant is retained in prepared artifacts.
  Before invoking a catalog producer, the capturing SELECT may prove its exact
  single input undemanded through `catalogrt.KeepQuery.RejectsInput`, using the
  executor's literal/current-parameter WHERE proof. Common current SELECT,
  descriptor/domain/epoch and restricted-view admission still precede the typed
  empty source. No producer, backing getter or provider cut is then opened.
  Physical/stored/foreign/sequence paths are unchanged; unsupported topology and
  locking keep ordinary capture. A held statement recomputes demand from this
  execution's imported parameters rather than retaining an earlier FALSE.
  Ident and simulated HBA diagnostic sources apply the same current view/column
  gate and independent routine 6250/3401 EXECUTE gate. Ident retains its immutable
  startup filesystem capability, with file/include/ARE work outside Engine.Mu.
  HBA captures its Driver-wide holder's immutable fact snapshot only after ACL
  admission, then materializes typed rows unlocked. Set takes no Engine lock;
  source publication never nests Driver/Engine locks. Prepare and metadata do
  not capture facts or access files. Exact-DSN/logical runtimes and template seeds
  share the destination holder, including across Reset and staged Restore, so a
  completed Set during staging stays current. Dump never serializes these facts
  or filesystem authority; ordinary symbolic ACL deviations replay.
- The runtime producers read is [`core/catalogrt`](catalogrt/README.md):
  `catalogrt.Runtime` over `catalogrt.Host`, the shared capture cache
  (`Engine.catalogCaptures`, owned by `Engine.Mu`), keep filters and the
  catalog source descriptors. Core implements the Host in
  `catalog_runtime_host.go` (`CatalogRuntimeFields`, one allocation per
  capture) and keeps the engine-level facts it serves in their own files.
  Settings rows capture one coherent physical-session snapshot after unlocking
  `Engine.Mu`; `pg_locks_capture.go` keeps an unlock/capture/relock epoch
  fence.
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
- Before the existing replacement identity remap, validated compatible active
  native extension/procedure recipes select their live destination addresses.
  Provider/release/slot authority, not replay allocation order, authenticates
  those fixed assignments. The shared alias/collision planner displaces ordinary
  import occupants without changing live TEMP identities; the one graph remap
  updates source declarations, references and membership together. Ordinary views
  remain source-owned definitions, not installed callable roots.
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

`gin_maintenance.go` implements `gin_clean_pending_list` through the ordinary
typed capability. It captures the target, releases Engine.Mu for RowExclusive
relation admission, then reuses the sorted heap-statistics owner cohort
(TransactionOwner.Mu in session order, Engine.Mu last). Effective catalog/store,
owner/epoch, EXECUTE and index-owner authority are readmitted before preparation
and installation. Active, implicit, savepoint and same-database detached prepared
roots participate; foreign active lexical StatementScopes are never mutated.
The index owner authenticates actual current owner bindings and presentation or
routine-source transitions before COW token/output transport outside Engine.Mu.
Accepted older outputs use their actual `IndexAdmission` catalog as key
provenance, not ACL authority. Exact descriptor, physical parent and strict
two-catalog presentation certificates permit COW descriptor transport while
sharing produced keys; detached, foreign or changed executable sources fail.
Semantic `OutputMatches` similarity never supplies production authority: an exact
enrolled descriptor/receipt pointer or its actual matching `IndexAdmission`
source certificate is required, even when a detached copy has current shape.
Exact frontier/root/store/journal/cohort and representation-revision fences
precede one callback-free transfer. Stale publication attempts recapture latest
owner roots; Finish reconciles exact-version stage aliases with monotonic main
placement. No SQL XID, catalog revision or commit frontier is assigned solely
for cleanup. Read-only SELECT is allowed, and successful physical transfer is
not SQL undo state; private membership remains private until ordinary commit.
Committed root publication installs a prepared immutable map image, fenced
against unrelated roots, so cleanup cannot race readers of a previously captured
map or overwrite a peer's root. A private replacement heap uses its own candidate,
never the committed root belonging to another physical store. Concurrent
materialized refresh captures exact predecessor roots and the owner placement
revision; changed sources retry direct publication or reject stale private
preparation before Finish can resurrect deferred placement.
Before an owner's first physical cleanup, publication/Finish skip placement
normalization entirely: no accepted output can yet have moved to main. GIN race
checkpoints likewise require actual GIN candidate/prepared roots. Unrelated
publication retains its original cancellation polls and final locked installation
boundary; cancellation after that boundary cannot retract rows, commit history or
CHAIN installation. The owner admission mutex remains held until Finish, including
control completion, rather than being released early to work around cancellation.
Persistent replacement's retained-TEMP remint branch also certifies GIN
execution across reconstructed builtin descriptor pointers. Only exact live
TEMP descriptor/root/store provenance can enter it; current provider/declaration
and concrete codec/collation checks precede COW key/binding transport. A stale
source or incompatible replacement fails before publication, preserving the
old split rather than retaining obsolete persistent class metadata.
The released recovery diagnostic/precedence helper is retained, but the embedded
runtime supplies false consistently with `pg_is_in_recovery`; there is no native
recovery mode or invented public setup control.


`xml_map_rows_*` borrows ordinary nested SQL execution, relation locks and
SQL/PL/protocol portal identities. Data SPI parses the complete input, then
analyzes and executes commands sequentially; schema SPI opens one prepared
cursor and closes it without FETCH. Combined output performs those stages
separately. Table schema introspection holds a real relation lifetime without
SELECT permission, while data passes normal SELECT/ACL/RLS admission.
SPI admission reports semantic query-creation tags, including normalized SELECT
INTO, rather than the SELECT wire completion tag used for their produced rows.
Schema preparation analyzes canonical query-bearing statements in written order;
raw utilities bypass generic binding. Cursor opening rejects noncursor/multiple
plans without executing utilities or inheriting runtime readonly admission.
Cursor schema inspects an existing descriptor even while its producer is active
or failed; data movement alone enforces runnable state. Named protocol mapping
and wire Execute share typed startup and the same genuine producer position.
SQL FETCH, PL refcursor motion and mapping share one balanced active-motion
guard in the SQL cursor owner, including held motion. Recursive producer demand
is rejected before admission or scratch mutation; descriptor inspection does
not acquire that guard. Preparation activates under the original caller policy
before suppressing readonly scratch, preserving its private write/delete
frontier and restoring the complete lexical owner on success and failure.
Failed SQL/held portals release executor and stored rows while retaining their
immutable tuple descriptors until terminal close or transaction retirement.
Mapping retains no catalog, row, session or output environment in XML values.
Schema/database mapping captures coherent private catalog and role authority
under `Engine.Mu`, then releases it before membership/ACL traversal, name ordering,
relation locks and formatting. Schema USAGE and table-wide SELECT determine
membership; descriptor-only output never demands rows. Generated REGCLASS SELECTs
reuse the same SQL producer, typed consumer and lexical cleanup. A scope-only
effective-user borrow carries the actual expression actor through generated SQL
without changing ordinary view/definer semantics; primary errors and cancellation
restore the original actor and enclosing owners. No membership or authority
survives in prepared recipes or generated values.

`session_activity_runtime.go` reads the current operation's original frontend SQL
and physical session token. `DatabaseCluster` owns immutable microsecond startup
and configuration-load stamps, captured at cluster construction and actual initial
settings-registry load. Logical database adoption shares them; native catalog
replacement never imports staging clocks or changes destination runtime identity.
These runtime reads allocate no per-cell strings, snapshots or catalog captures.

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

Large-object SQL descriptors belong solely to the transaction owner's extension.
Admission serializes table and cursor access; catalog resolution and COW journal
updates then hold `Engine.Mu`, with the existing Engine → Settings → Session
authorization order. Slots reuse the lowest free signed INT4 index. READ-only
entries share immutable opening bytes; WRITE entries resolve current committed
bytes plus the owner's journal when content is needed. Query admission captures a
descriptor-only opening frontier before argument effects. Stable isolation
composes its separately retained committed LO base with query-visible own LO
changes. Nonvolatile SQL bodies inherit that exact immutable caller frontier,
including exclusion of argument writes. VOLATILE commands capture fresh own
changes but retain the stable-isolation committed LO base. SQL-body scratch
restores the exact outer frontier on success, failure and nested unwind,
without rewinding the live journal, retaining views in prepared recipes or
changing convenience-function visibility.
Object ACL admission occurs only at open; subsequent operations retain mode,
not actor or role graphs. Truncation validates readonly mode, descriptor, write
mode and length in that order, journals an immutable replacement/revision and
never changes the int64 cursor. Seek/tell validate the descriptor before any
whence/target checks, with no additional write-mode, readonly or object-ACL
admission. Tell and SET/CUR require no live-content lookup; END uses O(1) frozen
READ size or current private WRITE size (zero after peer unlink). Valid seek
targets include the sparse ceiling and never extend EOF or allocate pages.
Invalid whence/targets preserve the cursor; int32 seek-result overflow is checked
after movement and remains observable after savepoint recovery. Tell overflow
does not move the cursor. Savepoints retain opening ordinals, never
cursor/table copies: rollback restores catalog bytes and closes younger
openings without rewinding cursors or undoing explicit close. Successful local
unlink closes matching handles. Canonical transaction/error disposal releases
the table and stable base, and native replacement uses the ordinary
active-transaction epoch fence. Prepared definitions own no descriptor IDs,
offsets or content snapshots. Dump's generated endpoint opens are fresh replay
handles, not serialized source cursors; admitted statement-local byte-replay
authority bypasses only their object ACL check, not callable authorization,
existence, mode, readonly or catalog/transaction fences.


amcheck's restricted-owner boundary enters that capture seam before replacing
the inquiry presentation, preserving the admitted outer query's LO frontier
even when its first callable uses restricted-owner rather than write authority.
It does not recapture an already active SQL-body frontier or restore an older
frontier when the restricted frame returns.

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

The catalog-object, session and transaction inquiry bodies are
[`core/inquiry`](inquiry/README.md), entered through `catalogInquiryHost`,
`sessionInquiryHost` and `transactionInquiryHost` (`inquiry_statement_host.go`).
Core keeps SQL-callable precedence, analyzer/coercion authority, the
statement's inquiry snapshot and its captures (`captureVisibilitySnapshotLocked`
at the execution frontier), lock and authorization recapture, pending-TEMP
staging, schema caches and lazy routine admission, the size captures over the
cluster's target runtimes (`RelationAllocationLocked`,
`AggregateAllocationsLocked`), as well as stored literal pairing, logical
identity remapping, prepared invalidation and catalog-replacement fences.
`make check RUN='^TestDatabaseFiles(BindingProtocol|RuntimeBoundary)$'` covers
the file inquiries' binding and runtime boundary.

### Other runtimes

- Ordinary blocking reads never nest `Engine.Mu` and manager mutexes.
  Safe-snapshot capture runs under `Engine.Mu`.
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
  `InputWithEnvironment`; domain and ISN-containing seeds enter
  `BindingHost.DecodeDomainLiteral` and the existing `DecodeLockedDomainInput`
  bridge. CHECK evaluation and authenticated ISN initialization run outside
  `Engine.Mu` against captured input/providers; context, caller authority,
  catalog epoch and type/codec provider identity are readmitted before publication.

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
  before the wire error. Finalized RETURNING, modifying-CTE and utility stored-row
  output uses the same post-encoding/pre-sink `validateCodecOutput` frontier as
  portal, FunctionCall and COPY output. It rejects peer-stale codec observations
  without exposing fields or holding a lock over the external callback. The wire
  captures the failing active pair before unwind and reports restored settings
  afterward.
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

## Testing

Simulated HBA holder ownership in `enginetests/pgcatalog` uses
`make check RUN='^TestPGHBAFileRulesHolder'`: destination staging, replacement,
Reset and current/future template/logical targets retain one live holder while
old immutable snapshots survive. Public SQL outcomes remain in reference tests.

WAL-name startup propagation, destination rebinding and accepted-fold ownership
use `RUN='^TestWALNamesMetadata$'`.

Fixed-family view capture, cumulative Reader backing and cluster publication/
replacement ownership use `make check RUN='^TestPGStat(Archiver|BGWriter|Checkpointer)'`;
add `RACE=1` for the same families' synchronized ownership witnesses.

Cumulative table source identity, selection-before-demand, attached-index
aggregation and actual transaction Reader cuts use
`make check RUN='^TestPGStat(All|Sys|User)Tables'`; add `RACE=1` for synchronized
ownership coverage. SYS and USER share composed bodies, with ordinary USER
namespaces and descriptor-only reserved-prefix boundaries; public SQL stays
in the statistics reference package.

Transaction-local statistics ownership and source capture use
`make check RUN='^TestPGStatXactAllTables'`; add `RACE=1` for owner-local
admission and generation-fence witnesses.

Sampling lifetime units use `RUN='^TestSystemRows'` over actual child failure and
cancellation, statement pins, independent prepared protocol resources and retained
immutable graph/fold/schema backing through Close and Reset.

Genuine pgcrypto activation, dormant/closed evaluation boundaries and retained
fold ownership components use `RUN='^TestPgcryptoPolicy'`.

Trusted local comment replay admission and bootstrap/namespace authority use
`RUN='^TestTrustedRestoreComment'`; public comment and native-control outcomes
remain in the owning reference scenarios.

Genuine tuple-discard, deferred heap-incarnation and composed repair/owner
transitions use `RUN='^TestPgsurgeryFreezeRetention$'`, inspecting actual roots,
headers and immutable payload backing. Public SQL repair witnesses remain in
the dual-mode reference family.
Sibling deletion checkpoint ownership uses
`RUN='^TestPgsurgeryFreezeDeletionOwnership$'`, retaining genuine physical
targets and immutable stage roots through protected rollback.

Engine tests whose code is mostly in a lower package or a `*cmds` family live
in test-only `core/enginetests/<area>` packages (`functions`, `types`,
`extension`, `textsearch`, `pgcatalog`, `infoschema`, `binder`, `executor`,
`writes`, `locking`, `sessions`, `tables`, `indexes`, `views`, `schema`,
`roles`, `stored`, `dump`, `routines`); `make check RUN=…` selects across
packages, so the selectors below cover them too.

Snowball provider authority, prepared ownership and publication units in
`enginetests/functions` use the shared `enginetest` prepared-fold and catalog-stage
helpers; retain accepted generations, backing and operand provenance separately
from public SQL outcomes in `reference/`.

Transition capture, checkpoint, exposure, invocation, prepared-shape and
trigger-free producer ownership units use `RUN='^TestTriggerTransition'`.

Installed statistics provider, expression and capture ownership units use
`RUN='^TestPgstat(index|hashindex)'`.

Bloom installed identity, immutable root publication, visible capture, collision
consumption, cancellation and control backing units use `RUN='^TestBloom'`.
They retain genuine production roots and current command admission, never fake
signatures or injected candidates; SQL result witnesses remain in reference.

Low-level relation-admission owner tests must pass the statement through `Gate`
before acquiring its relation: `StartStatement` alone does not attach the
transaction incarnation or lock ticket. Wait witnesses inspect the real grant
and queued owner, and fail immediately if the worker completes before admission.
Short lock-timeout diagnostics instead await the bounded terminal result: a
transient pending request is not a synchronization barrier. Cancel, release the
blocker and join workers on assertion failure as well as success.

Transaction-control publication units use
`make check RUN='^TestTransactionControlsPublicationBoundary$'`, adding `RACE=1`
for the same real-owner barriers. Cooperative `Context.Err` arming must account
for enabled utility normalization before CHAIN reservation and stop at the final
row-publication operation fence, not at reservation. Retain the mixed spare/growth
roots, snapshots and complete owner/error assertions. Every admitted scope defers
`Finish` before operation release; barrier cleanup cancels, releases and joins
workers before either owner is disposed, including assertion failures.

SMALLINT staged-cancellation units use
`make check RUN='^TestSmallintStagedCancellation$'`, adding `RACE=1` for the same
owner boundaries. Capture the real binding and typed rows before arming only
`ValidateInsertRow` demand: physical entry and the no-generation check poll twice
per valid row, before batch-stage transfer. Integrated `scope.Execute` retains
collection and normalization; assert all three typed unpublished stage rows,
then arm only Finish's operation-validation and final publication gates.
Parser, setup and unwind checks are not row-constraint phases.

Retained heap version, logical index publication, capture and expression ownership
units use `RUN='^TestPgstattupleHeap'`; public outcomes remain in
`reference/extensions`.
Visibility owner, snapshot, lock-admission and declaration units use
`RUN='^TestPgvisibility'`, with `RACE=1` for concurrent capture lifetimes.
Installed record-source fixtures use `BindFunctionSource` with the ordinary
`FROM` clause and query namespace, preserving scalar/set admission checks.

`TestPgFreespaceWrapperParameterDemand` separates command-policy transport from
retained-declaration admission. It executes the genuine renamed wrapper with an
imported NULL parameter, then borrows its still-live command authority through
the existing source-capture seam before `Finish`; eager finalized output has
already detached that authority. Separate private retained-call cases initialize
their original binder manifest/catalog and current principal admission under
Owner → Engine locks, following `TestPgFreespaceAdmittedIdentity`. A `SELECT 1`
snapshot supplies command policy only: patching scope dependencies after source
capture neither creates a frozen recipe nor admits an old descriptor as fresh
SQL. Keep the ordinary fresh-binding revision conflict intact. Source/cursor
cleanup and scope/operation release precede reporting any assertion failure.
The no-View command source keeps its own initializer/header catalog; the private
retained-call evaluator explicitly supplies its separately admitted original
recipe catalog, rather than relying on lexical catalog precedence.
Use `make check RUN='^TestPgFreespaceWrapperParameterDemand$'`, adding `RACE=1`
for the same ownership cases; public NULL planning/ACL outcomes remain in
`TestReferencePgFreespacePermissions` in both modes.



TEMP tablespace preparation, admission and composed-creation units use
`RUN='^TestTempTablespaces'`; their SQL/diagnostic witnesses live in `reference/`.

XML mapping accepted graph/fold/definition and caller lifetime units use
`RUN='^TestXMLMap(Rows|Scope)'`; public SQL/protocol outcomes belong to
`reference/strings` (rows/cursors) and `reference/xml_mapping` (scopes).

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
`reference/catalogs`, selected by `^TestReferenceReplicationOrigin` and
`^TestReferencePGReplicationOriginStatus`. Status capture pairs the current
authorized roots and detached progress map in one cluster-mutex window, then
projects outside the lock without a statistics Reader. Backing6014 EXECUTE is
rechecked independently of the common view/column SELECT manifest. The current
whole-query admission frontier also checks6014 before function-scan initialization,
including LIMIT0; proven false-WHERE input elimination is a separate decision.

Shared parameter authority backing, admission, publication and replacement
units use `make check RUN='^TestParameterACLShared'`; add `RACE=1` for the same
deterministic ownership/concurrency witnesses. SQL acceptance remains in the
owning dual-mode reference packages.
Prepared metadata identity, immutable buffers and token lifetime units use
`RUN='^TestPGPreparedStatements'`; public SQL and wire scenarios use
`RUN='^TestReferencePGPreparedStatements'`.

XML2 prepared ownership uses the reusable `RUN='^TestXML2'` family, also with
`RACE=1`. It retains real definitions, exact callable/type generations, ordered
fold inputs, accepted datum backing and private portal graphs through rejected
publication, definition retirement and producer cleanup. Public scalar outcomes
and live authority remain in `reference/extension_xml2`.

SSLInfo definition/accepted-graph ownership uses `RUN='^TestSSLInfoExpressionProtocol$'`.
It retains actual protocol definitions, installed dependencies and volatile
wrappers through repeated bindings and rejected publication, without internal
public-SQL outcome tests.


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
Cumulative reader/lifecycle/source/physical ownership units use `RUN='^TestStatsSnapshot'`.
Logical-slot source/orphan-provider and cumulative-reader ownership uses
`RUN='^TestPGStatReplicationSlots'`; public SQL stays in the reference family.
SSL descriptor/projection and shared-cut ownership units use `RUN='^TestPGStatSSL'`;
add `RACE=1` for synchronized capture, current authority and physical retirement.
The shared-capture consistency/order/end matrix compares the target database's
visibility entry, canonical XID allocation and public snapshot frontier before
monitoring, after SSL capture and after activity projection. Observer-database
transaction bookkeeping is deliberately outside that nonmutation assertion.
Sequence value-tail/reset, TEMP replacement and producer key-filter ownership
units use `RUN='^TestStatsSequence'`, also with `RACE=1`.
TEMP replacement fixtures identify sequences in the attached owner's actual
namespace, not through a bootstrap session's `pg_temp` alias; their assertions
retain published facts, epoch fencing, warm value tails and physical-end retirement.
Index source/path/stop/replacement ownership units use `RUN='^TestPGStatAllIndexes'`.
Their composed row helper preserves unsigned OID and TIMESTAMPTZ values through
the typed sqltype accessors; the generic primitive test exporter is not a codec
for either type.
The catalog fixture includes a real partition-leaf index. Partitioned-parent
exclusion is composed through the existing immutable catalog stage against a
real partitioned parent, without adding unsupported parent-index DDL or an AM.
`RUN='^TestPGStatAllIndexesInterruptedAccess$'` keeps reached search/entry/fetch
prefixes across deterministic hash/bloom enumeration errors and cancellation
after one of two heap candidates. Each uses the actual implicit statement
`Finish(error)` boundary and checks its later terminal stop instant.
The cancellation fixture asserts retained output before the failing demand,
which owns stage closure, and never calls `Count` on a released stage.
Replication reset expression, owner-routing, retained-reader and actual subscription
lifetime units use `RUN='^TestStatsResetReplication'`, also with `RACE=1`.
Shared subscription projection, exact owning-key participation, initial peer
snapshot capture and current backing authority use
`RUN='^TestPGStatSubscriptionStats(Provider|Snapshots|RoutineAuthority|RuntimeBorrow)$'`,
also with `RACE=1`. Subscription-first captures consume the twelve-field typed
cursor directly, preserving OID/reset types and all nine distinct provider fields
before inspecting the Reader's initial shared cut and retained backing.
Shared/backend reset routing, provider composition, immutable expression and
physical reader lifetime use `RUN='^TestStatsResetShared'`, also with `RACE=1`.
Database/object reset composition and retained graph/writer/reader authority use
`RUN='^TestStatsResetDatabase'`, also with `RACE=1`. The typed statement bridge
checks current routine authority under Engine.Mu before provider leaf locks.
Composed inquiries consume TIMESTAMPTZ/VOID through typed cursors. Initialized
portals retain their ordinary admission lifetime; a new execution of the held
prepared definition checks current EXECUTE. Genuine delivery failure detaches
the live producer without mutating separately retained graph/fold backing.
Catalog classification routes shared relations across actual target partitions;
dboid-zero reset metadata belongs to DefaultTarget, never the caller database.
SQL publication does not touch transaction journals, physical data, pending
writers, active functions, reader caches or lifecycle generation.
Forced custom/generic parameter witnesses retain one described definition through
grant/revoke and observe genuine PL calls, including strict-NULL/denial no-effects.
The database-wide maintenance witness starts from independently positive real
vacuum/analyze/tuple history and retains actual optimizer roots, column objects
and backing while clearing cumulative facts. Physical maintenance metadata stays
unchanged; later ANALYZE samples it into fresh estimates and starts new history.
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
Installed ISN accepted definition, operand backing and rejected fold publication
use `RUN='^TestIsnExpression'`; stored transport authority shares that family.

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
`RUN='^TestXML(SQLPrepared(Portal)?Ownership|Produce(PreparedFoldOwnership|UnknownParameterAdmission))$'`, also with `RACE=1`.

Type-path backing and COPY predicate/retained-recipe demand units use
`RUN='^TestTypeDescribe(CapturedTypePath|CopySnapshotDemand|RetainedSnapshotDemand)$'`.
These inspect the real authorized capture and preflight boundary; public SQL
TEMP qualification and COPY rows/errors remain in the reference definitions.
Nullable registry lookup prepared-definition ownership and retained INT4
destination capture use
`RUN='^TestRegLookupRelType(PreparedOwnership|StoredInputCapture)$'`.
These inspect actual definition/provider generations, accepted private graphs
and immutable fold backing, plus current catalog/path/principal input without
rebinding catalog-owned defaults. Public lifecycle and replay remain in
`reference/type_resolution`.

Statistics definition/capture/replay invariants use `RUN='^TestStatisticsObject'`.
Control ownership witnesses admit ordinary transfers across intervening directives,
validate remapped principals/definitions and retain destination roots on missing
recipients or cancellation; no statistics paired-owner capability is involved.
Metadata COW, genuine collected-root compatibility and original-source ordinary
ownership admission use `RUN='^TestStatisticsAlter'`.
Collectors consume the captured catalog's canonical type descriptors. No-op stages
must contain no replacement while their overlay retains the exact base descriptor.
Composed ownership witnesses enter the actual statement Gate to begin and register
the implicit transaction/ticket before lock admission, rejecting early worker
completion while observing the exact queue.
Publication units use real provider barriers and exact namespace/name resources,
join workers before cleanup, and reject retirement or competing bindings without
adopting successors. Use `RACE=1` for the publication family.

Same-package integer/NULL FDW fixtures use private `fdwIntegerImport` and
`fdwIntegerExport` in `fdw_query_prepared_test.go`: import `int64`/nil with the
declared integer type and export `int64`/nil. Metadata-only nil codecs and
intentional failing callbacks remain local. FDW units use `RUN='^TestFDW'`;
public SQL witnesses remain in `reference/tables`.

Foreign selected-plan backing and canonical/journal sampled-statistics ownership
use `RUN='^Test(FDWExplain|AnalyzeForeign)'`. Public dual-mode scenarios remain
in `reference/tables`; prepared definitions retain no provider plans or sampler
closures.
Detached foreign publication units gate actual typed comparison entry with the
existing installed-comparison context seam. Peer work must complete during the
gate. Local-pass rejection preserves complete canonical headers/count/time/
population/backing; inherited rejection retains only authorized completed-local
maintenance, never partial typed or inherited success. Mixed allocation, actual
sample/backing ownership, foreign-root passes and Journal/savepoint/prepared
completion use `RUN='^TestAnalyzeInheritedForeign'`. Native replacement checks
the newly admitted state as well as immutable old records, not intentionally
retired persistent statistics. Prepared definitions retain no sampler closures.

EXPLAIN ownership/presentation, nonexecution and actual-execution boundaries use
`RUN='^TestExplain'`. Prepared wrapper owners retain no accepted child fold group.
Ordinary EXPLAIN keeps installed foreign capabilities unopened; ANALYZE witnesses
exercise real child execution, demand, worker aggregation and publication veto.
The render cancellation context targets the renderer's cooperative boundary and
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
Buffer-cache identity, source attribution, sparse eviction, session ownership
and successful/failed replacement fences share `RUN='^TestPgBuffercache'`.
Composed units retain actual protocol portals and admitted source captures;
pure tiny-capacity units exercise pinned victims without public inspection hooks.
Public dual-mode SQL belongs to `reference/extensions`.
Pending-block source reachability, live typed recipes, identity fences and
virtual-time publication ownership share `RUN='^TestStatsXactBlocks'`, also with
`RACE=1`. Public direct/prepared behavior belongs to `reference/session_state`.

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
Deferred event ownership, terminal atomicity, reentrant drain and cancellation
units share `RUN='^TestTriggerDeferred'`; private assertions distinguish immutable
payload backing, restored consumed eligibility and publication ownership.

Event-trigger journal, frame, projection, drop and rewrite units share
`RUN='^TestEventTrigger'`; public direct/prepared behavior belongs in the
dual-mode reference suite.

Routine invocation, CALL projection and retained-fold ownership units use
`RUN='^Test(SQLRoutine|PLpgSQLProcedure|FDWSQLValidator)'`, also with `RACE=1`.
Composed effective-user and setting environment ownership uses
`RUN='^TestRoutineEffectiveUser'`, also with `RACE=1`. These units exercise real
entry/unwind, actor nesting, codecs/policy and partial-entry restoration;
public SQL behavior remains in reference scenarios.
`RUN='^TestRoutineStoredReplacementPublication$'` covers authenticated scalar
publication, immutable source/backing and rejected predecessor, lifetime,
dependency, type/collation and provider authority without partial installation.
It uses populated expression/predicate roots, exact source/root/storage-window
receipts, private replacement chains/rollback and genuine later rebuild intent.
Pending INSERT witnesses independently distinguish old produced keys and false
predicate membership from successor evaluations, preserve producer backing
through repeated private replacements/commit/rollback, reject forged output
lineage, and require current values and membership on a genuine later rebuild.
`RUN='^TestRoutineStoredReplacementOutputShape$'` checks exact declaration-owned
OUT tuple/member generations across compatible replacement, reminted stored
field carriers, immutable predecessors and rejected incompatible publication.
`RUN='^(TestRoutineIndexPhysicalRecipeProvenance|TestRoutineIndexUnchangedSlotConstructionTransport|TestRoutineScalarRelationPublicationReceipts|TestPreparedFoldProtocolRoutineReplacement)$'`
checks physical last-build history across logical publication/merge/transport
and selective maintenance, exact relation predecessor chains and rejected copied
or abandoned receipts, and independently retained nonempty portal POINT artifacts
and imported frames across authenticated relation transitions.
Accepted command DEFAULT ownership and admission units use
`RUN='^(TestPreparedAcceptedDefault.*|TestSnapshotSyncAcceptedDefault.*)$'`; they
inspect the actual private accepted owner separately from public wire lifetime
scenarios. The core protocol fixture compares selected DEFAULT exports with the
actual acquired snapshot/root backing, while the views fixture probes pure
retention before strict invocation, publication, current-authority and terminal
fence checks. Demand must not mutate original source paths, prepared argument/
assembly/signature backing or generations, or supply invocation authority.
`TestPreparedAcceptedDefaultSourceDerivation` distinguishes genuine nested,
sibling, hidden-default and pruned CASE recipes from substituted roots, changed
arguments and wrong original mapper paths, including nil child slots.
`TestPreparedAcceptedDefaultInvocation` drives the actual active delegation and
body dispatch while rejecting copied descriptors and argument/assembly backing;
`TestPreparedAcceptedDefaultHiddenIsolation` keeps hidden-default admission and
readmission independent from the enclosing occurrence. The unchanged-slot index
transport cases pair a clean construction with preexisting recipe debt across
the same unrelated tail-column metadata remint, preserving physical payloads
and historical provenance.
Snapshot registry lifecycle units use
`RUN='^TestSnapshotSync(AbortImportRace|EndImportRace|NativeEpoch.*Race|RestoreInFlightCancellation)$'`,
also with `RACE=1`. They compose real independent session operations with
canonical failure/end and native replacement, checking exact retained committed
backing, atomic tombstones, visibility withdrawal and old-incarnation fences.
Ordered controls bracket repeated concurrent starts; a gated external Restore
reader distinguishes in-flight cancellation from an entry-only context check.
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

Backend-memory private owner/layout, typed capture, real callable source and
cancellation units use `RUN='^TestBackendMemory'`. Public SQL, ACL and native
control scenarios live in `reference/catalogs` under `TestReferenceBackendMemory`.

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
  `NewCatalogRuntime`, `CatalogRuntimeFieldsOf`, `ProcedureReturnsSet`;
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
make check RUN='^TestPGStatGSSAPI'                      # real attachment, raw transaction capture, current authority and detached producer; also RACE=1
make check RUN='^TestTransactionalSchema(Admission|PrivateProviders|Publication|DropManifest)$'  # utility policy and private catalog stages
```
