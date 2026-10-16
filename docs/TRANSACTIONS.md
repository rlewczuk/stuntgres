# Native transaction ownership and publication

[README](../README.md) · [Driver](DRIVER.md) · [Settings](SETTINGS.md)

PostgreSQL 18's [transaction isolation](https://www.postgresql.org/docs/18/transaction-iso.html)
and [locking](https://www.postgresql.org/docs/18/explicit-locking.html) manuals
are the SQL authority. This page records native Go contracts, snapshot/resource
ownership and compatibility limits, not transaction-control grammar or a claim
of PostgreSQL physical MVCC/durability.

## Go transaction handles and physical sessions

`DB.BeginTx` and `Conn.BeginTx` use the physical session's canonical transaction
owner. Native supports `sql.LevelReadCommitted`, `sql.LevelReadUncommitted`,
`sql.LevelRepeatableRead` and `sql.LevelSerializable`. Default isolation honors
session defaults; explicit isolation overrides them. Go `ReadOnly: false`
explicitly requests read-write. Deferrability inherits the session default since
`sql.TxOptions` has no such field.

Go handles are incarnation-qualified. A SQL end/new start or chained boundary
makes an older Go Commit/Rollback return `stuntgres: stale transaction reference`
without ending newer work. This intentionally differs from pq's unqualified
end behavior. Pin `sql.Conn` for SQL-owned blocks and end them before returning
the borrower: borrower Close and healthy ResetSession preserve an active block.

`database/sql` owns automatic rollback for its BeginTx context. Driver end
operations use fresh contexts because `database/sql` cancels that internal
context before healthy Commit. Raw driver Rollback remains available after
caller cancellation. Physical close/termination drains operations and discards
only attached private effects, not peer data or detached prepared entries.

## Transaction identity and retention

Public assignment belongs to the existing top-level transaction owner. A genuine
write/row lock/PREPARE or explicit `pg_current_xact_id()` request reserves one
unsigned full64 identity; its low32 mirror supplies XID lock/SSI/timestamp APIs.
Reserved low0/1/2 are skipped on wrap and live-low alias/full64 exhaustion fail
before mutation. Diagnostic IDs, visibility ordering and commit sequence are
separate authorities. Describe/Prepare and if-assigned/age/status/member inquiries
do not allocate. A read-only top transaction can explicitly request a real ID.
Provisional UPDATE/DELETE candidate grants reserve neither an ID nor membership;
accepted physical actions and genuine installed lock-before-qual events do.
Thus a rejected prephysical CHECK leaves an initially unassigned owner unchanged,
without erasing an assignment on rollback-to.

Savepoints retain that full identity and first-demanded age anchor. Existing
native savepoints do not allocate PostgreSQL child XIDs. Rollback restores actual
row contribution provenance while keeping every already-created immutable
MultiXact bag. The manager still arbitrates by ObjectRef/RowID; physical history
additionally distinguishes immutable VersionID predecessors and successors.
Only validated installed lock and modifying-action events publish membership,
outside Engine/owner mutexes and before later RETURNING/index error observations.
Manager batch preflight publishes no partial counter/history prefix.
An UPDATE successor copies the original raw member ID and whole bag, not its
new predecessor action ID, a filtered snapshot or an allocated key-share-only
replacement. Each top transaction has its own bounded 256-entry member-set/ID
cache; member inquiry may learn existing sets without assigning an XID.

Transaction status is retained independently of commit timestamp tracking.
Publication records commit exactly once; ordinary cleanup cannot overwrite it.
Actual ticket retirement controls the publication-to-release in-progress gap.
Prepared completion uses the original detached identity, and failed/denied/busy
completion leaves it live. Disconnect, admitted cancellation and abort record
only genuinely assigned identities. Successful logical replacement fences old
owners and retires status/member/timestamp history without resetting counters;
failed replacement leaves histories intact. Native retention is not physical
CLOG/MultiXact truncation, WAL or VACUUM.

## Private roots, snapshots and eager results

Statements use the existing owner, command identity, catalog overlay, immutable
row/deletion roots and setting journal. Commit atomically installs staged
relations while preserving peer commits; rollback discards private roots.
Committed vectors grow amortized rather than copying a whole relation per insert.
Captured visible prefixes never change. Typed values and descriptor generations
remain owned throughout committed and private version lifetimes.

Data Prepare can establish characteristic timing and a stable snapshot, but
retains neither executed SSI predicates nor a data view in the handle. Prepared
handles retain AST/source and frozen descriptions. Execution imports fresh typed
arguments against a coherent binding. Snapshot visibility is independent of
current-only index roots; native queries consume selected historical/own-write
views, not a promise of optimizer index scans.

Read-committed execution captures fresh data after relation grant. Stable-snapshot
transactions retain their frontier even when rows close or savepoints unwind.
Rows are eager immutable results: public Next performs no query production,
lock wait or predicate recheck. Results survive native commit/rollback/replacement;
`database/sql` can nevertheless close Tx-owned Rows at transaction end, so public
Tx Rows must not be retained past end.

RANGE table ATTACH is a scoped transactional utility. Existing-row validation
captures current committed rows after its ACCESS EXCLUSIVE grants, plus the same
owner's prior writes/deletes. This dedicated visibility view does not advance a
REPEATABLE READ or SERIALIZABLE transaction's ordinary retained snapshot. Catalog
membership, populated index roots and new locks follow the existing overlay,
commit, rollback and savepoint authorities; failed validation publishes none.

## Retained direct-message transactions

A parameterless multi-command direct call retains an implicit segment until
message completion or a reached transaction end. Supported utility candidates,
rows, identity changes and settings compose under the same final publication.
Promotion to an explicit block preserves its reference, birth time, modes,
private roots and held locks rather than publishing the prefix.

A runtime failure discards the retained segment's earlier private changes, not
previously completed segments or independent calls. Already returned eager results
remain independent. Sequence allocation, random draws, advisory counts, cursor
movement and prepared registries keep their own lifetimes; rollback does not
rewind them. Each segment has its own transaction birth, while commands share
message statement time and reread timeout/notice policy from caller context.

Admission remains held between commands. Runtime providers run outside
engine/session/owner publication locks. An uncommitted utility cannot leak a
descriptor or grant peer access. Final publication checks committed predecessors,
storage, namespace/index membership, ACL authority, epoch and completion resources
before installing anything.

Domain CHECK ADD/DROP/VALIDATE participates in this existing implicit route,
without enabling domain utilities after promotion into a genuine explicit block.
Historical validation reads the current private catalog/projected storage plus
prior own inserts, replacements and deletions. Each successful validation carries
immutable relevant committed predecessor graph/storage observations through
catalogChanges cloning, savepoints and promotion. Final owner publication checks
those committed predecessors, not an earlier private projection against the
later private candidate: subsequent own container creation or CHECK removal can
be legal. Peer relevant type-use/storage drift vetoes publication without CHECK
truth replay; unrelated catalog changes remain eligible. Failed/canceled segments
discard private flags, generations, rows and index artifacts together.

Retained role retirement records exact principal obligations through savepoints,
promotion and prepared detach. Final combined catalog/role/reference preflight
rejects a peer's newly committed dependency with the ordinary replacement error,
without orphaning objects. This is optimistic publication, not PostgreSQL role
object-lock parity or new explicit-block role-utility eligibility.

Detached private roles can leave an effective numeric ID temporarily unresolved
outside that prepared owner. Authorization uses only real numeric self/PUBLIC
rights, never invented descriptors, inherited attributes or private-name fallback.
Prepared completion restores committed descriptors or leaves the unresolved ID
until a valid identity reset.

## Admission and transactional utility limits

Read-only modifying-utility rejection precedes lookup and native capability
admission. Most permanent catalog/ownership/schema/view/index/role utilities
remain excluded from genuine writable explicit blocks. Supported private TEMP,
sequence, enum and selected table-definition changes share row publication and
savepoint roots; their existence does not grant every adjacent DDL operation
transactional eligibility.

Query-derived permanent-table utilities remain standalone-only. Their native
explicit exclusion precedes snapshot acquisition, source lookup and waits.
Connection-prepared handles passed through `Tx.StmtContext` can fail during
repreparation before Execute's read-only gate. PostgreSQL's transactional
analysis/safe-snapshot behavior is independently tested, not native parity.

Successful private definitions govern subsequent metadata and writes, with
projected row shapes and provenance restored together on recovery. Historical
constraint scans and index construction use coherent provider/catalog authority;
validating historical rows is distinct from enforcing future private writes.
Most ownership utilities are autocommit-only; direct sequence ownership and
attached-database CREATE ACL changes retain existing transactional behavior.
Bulk catalog/storage/membership cleanup installs one complete candidate.

Full session cleanup cannot run inside a retained or explicit block and performs
no cleanup on rejected admission. Plan/sequence-cache discard is an irreversible
physical-owner effect; TEMP removal instead uses normal private catalog/storage
roots and is recoverable. Pool reuse is never implicit session cleanup.

## Rewrite-rule publication

Native CREATE/REPLACE/ALTER RENAME/DROP RULE follows the standalone view/policy
utility boundary: execution in an explicit transaction is rejected with0A000
before catalog mutation. PostgreSQL's transactional rule DDL is not claimed.

Rewritten DML is one original statement, not a multi-command message. Expansion,
recursion/restriction checks and every action's static preflight/current-owner
authorization precede effects. Original and action mutations share the same
operation, timeout budget, command clock, WriteStage and sole Finish. Top-level
successive actions observe required predecessor staged effects without publishing
them or admitting unrelated peer commits midway.

Modifying CTE auxiliaries retain their original entry snapshot and completed
RETURNING spool identity even when a rule changes the executable target or
mutation kind; no per-auxiliary rule-stage snapshot refresh occurs. Primary
output/count remains primary. A later action, primary constraint, cancellation
or publication failure discards all original/action row effects. Ordinary
transaction/savepoint recovery and non-MVCC sequence behavior remain unchanged.

## Statement failure and savepoint recovery

An admitted SQL error, binding/preparation failure or statement cancellation
leaves explicit work Aborted. Without marks it discards private effects and
releases holds; with marks it restores the failed level and retains premark
outer-ticket resources. Valid ordinary commands then fail `25P02` until eligible
recovery. Pre-canceled admission, closed-handle checks and adapter argument
rejection do not abort prior successful private work.

Savepoints snapshot private writes/deletes/catalog/generator bindings, typed
settings, identity journals and resource-mode contributions. Rollback-to restores
the selected mark and removes later marks; release retains child changes in the
parent. Snapshot/characteristic timing, SSI history and ordinary sequence
allocation remain monotonic. Full ends clear marks; successful catalog replacement
prevents a mark from resurrecting an old epoch.

Cursor motion, failure and closure do not rewind with a savepoint. Later cursor
declarations are removed by rollback-to. Cursor-private successful publication
has one final preflight before irreversible motion/removal/batch delivery; late
cancellation cannot retract it. It does not bypass private-stage merging for
ordinary catalog or positioned row mutation.

Creating holdable-cursor commit exports typed stores before commit actions and
write publication. Promoted stores retain values/position, not transaction
execution authority or live physical lineage; previously promoted stores can
survive later failure. Nonholdable names close at full end, detach or physical
end. See [driver cursor ownership](DRIVER.md#sql-cursors--change-note).

| Go/native boundary | Contract |
| --- | --- |
| Unsupported Go isolation | Ordinary `stuntgres: unsupported transaction isolation level`, before state change |
| Nested raw driver begin | Ordinary `stuntgres: transaction already active` |
| Repeated raw end | Ordinary `stuntgres: transaction is done` |
| Old incarnation handle | Ordinary `stuntgres: stale transaction reference` |
| Public repeated Tx end/use | `sql.ErrTxDone` |
| Go Commit of same-epoch aborted work | Ordinary `pq: could not complete operation in a failed transaction` |
| Old-epoch publication | Ordinary `stuntgres: catalog replaced before publication` |
| Caller cancellation/deadline | Original Go context error identity; automatic rollback can make Tx end return `sql.ErrTxDone` |

## Sequence and runtime-effect ownership

Ordinary generator draws and session last-value memory are immediate non-MVCC
state and survive rollback and later row-expression errors. A private restart
instead stages generator replacement and blocks peer functions, including cached
tail use, until atomic commit/rollback with table roots. Savepoint recovery
restores generator bindings, not copied counters.

Row-owned runtime demand must not replay effects after waits. Update expressions
are evaluated after real row-grant recheck. Earlier row violations retain already
demanded effects but do not consume later producers; whole-statement analysis,
parameter import and conversion preflight remain earlier. Successful writes
still publish as one batch. Read-only persistent generator mutation checks occur
before cache use/mutation; caller-owned TEMP has its existing distinct admission.

Session advisory counts belong to the physical owner, survive rollback and do
not transfer to prepared ownership. Only matching release, release-all or
physical end removes them. Transaction-level advisory providers and lock
introspection remain unsupported.

## Prepared transactions

Preparation transfers complete private roots and original relation/row/unique
tickets to the Engine registry; the origin becomes idle and can start independent
work. Origin/Rows/statement close does not release detached holds. Completion
publishes before waking waiters. Preparation and completion use the canonical
optimistic publisher, including authority/catalog/counter/SSI preflight.

Preparation validates original row/version predecessors and index deltas without
building installation row heads/maps/roots. Completion revalidates predecessors
and materializes installation payload only for actual commit. Validation scratch
is not a separate weaker publisher.

Registry names are exact case-sensitive decoded byte strings, up to 199 bytes,
including empty. Completion authority is the exact preparing principal or a
current superuser, not membership or a recreated same-name principal. Concurrent
claimed completion is busy; denied, canceled or failed completion retains the
entry and holds. Old Go transaction references cannot complete detached work.
Completion changes neither origin nor finishing-session settings.

TEMP use is sticky across savepoint rollback and prohibits preparation. An
unexported healthy holdable cursor also prohibits detach; ordinary Go Rows/Stmt
handles do not. Detached entries have no session timers. A transaction with
retained LISTEN/UNLISTEN actions or NOTIFY events fails PREPARE with `0A000`,
`cannot PREPARE a transaction that has executed LISTEN, UNLISTEN, or NOTIFY`.
The failure ends the incarnation, discards all staged effects, creates no GID,
and releases committed peer notifications at the actual outer idle boundary.
Current subscriptions alone do not prohibit preparation. Duplicate LISTEN and
net-zero action sequences still count; savepoint rollback removes child effects,
RELEASE retains them. UNLISTEN is action-free only on a never-registered physical
session with no pending actions. Committed registration history stays sticky
through UNLISTEN ALL, DISCARD ALL and native Reset/Restore. Notification checks
follow the existing WITH HOLD, GID-size and TEMP checks. Snapshot export remains
unsupported. See [local notifications](DRIVER.md#local-session-notifications).

Completing a detached prepared entry never consults the origin session's later
transaction notification buffers. Only the detached row/catalog roots publish;
later origin subscriptions and events retain their own commit/rollback outcome.

The prepared catalog provider owns coherent registry snapshots. Its XID carrier
uses the same distinct scalar identity and fresh decimal bytes as public XID
values, not OID's int64. Public XID/CID/XID8 input does not fabricate registry IDs
or system columns and does not change the existing preparation authority.
Dropped owners project NULL without changing completion authority. The registry is shared by exact DSN
within one Driver, isolated across Engines/Drivers. Native has no capacity setting,
cluster emulation, replication, WAL or crash persistence.

Reset/Restore/catalog replacement refuses final installation while entries exist,
including overlap with Restore reader I/O. Dump is committed-only and never
exports GIDs or private writes. Complete abandoned entries explicitly before
replacement.

## Serializable snapshot isolation

Native adds nonblocking read/write dependencies to stable snapshot/publication
authority. Executed permanent-table reads record whole-relation predicates before
filtering, including empty inputs. Metadata/system providers and sequences do not
participate. Predicate tracking is separate from blocking resource locks.

Overlapping committed evidence remains until the cohort drains. Coarse relation
predicates can reject workloads finer PostgreSQL predicates allow; continuous
overlap can retain graph memory longer. Unrelated relations/frontier advances do
not create conflicts. A doomed executed producer cannot install even an empty
query-derived target; omitted producers contribute no executed-read evidence.

Unsafe write skew cannot publish both participants. A doomed owner discovers
`40001` at participating read/write/commit, without asynchronously aborting a peer.
Retry the whole transaction, not just the failed statement. Mixed-isolation work
has no cross-level serializability guarantee.

Deferrable read-only acquisition can wait before first data analysis, including
Prepare. Unsafe provisional roots are discarded before data/handle/predicate
exposure; safe snapshots need no SSI predicates. Data Prepare establishes timing
but no executed predicate. Statement timeout covers the wait; lock timeout does
not. Child rollback does not erase predicates, doom, dependencies or attempted-write
history. Actual write attempts cannot later acquire read-only graph exemptions.

Epoch replacement fences old participants and wakes unresolved safe-snapshot
waiters. Failed replacement leaves them unchanged. Prepared validation orders an
immune node without declaring commit or resolving candidate outcomes. Completion
resolves those outcomes before safe cohort reclamation; origin close retains
detached graph evidence. See
[PostgreSQL retry handling](https://www.postgresql.org/docs/18/mvcc-serialization-failure-handling.html).

## Relation and row resource manager

Automatic/explicit relation modes and logical-row modes share canonical exact
transaction tickets. Modes accumulate as bits, not a strongest-mode ranking;
owners do not conflict with themselves. Grants and earlier conflicting waiters
define eligibility, with compatible cohorts and self-reentry preserved.

Semantic data Prepare acquires relation authority: implicit preparation releases
at completion, explicit preparation retains to end/recovery, not statement Close.
Utility preparation is effect-free where documented; execution resolves current
objects and checks authority before and after waits. Bound view descendants use
operation-local owner/original-invoker principals without repeated schema lookup.

Pending wait cancellation removes provisional state and wakes eligible successors
without disturbing holders. Each queued acquisition has its own lock-timeout
budget; immediate grants are untimed. Earlier absolute SQL deadline wins, with
an exact tie favoring lock timeout. Caller context errors remain Go errors.
Resource holds survive Rows/Stmt Close in explicit work. Implicit publication
happens before release; recovery restores exact premark mode contributions.

Deadlock detection distinguishes hard holder dependencies and soft queued-request
dependencies. Finite queue rearrangement can resolve soft cycles without revoking
holds. An unavoidable cycle fails one actual member with `40P01`; victim choice
is unspecified. Cancellation/deadline normalization occurs before graph decisions
and terminalization, so a stale cycle cannot replace the canonical interruption.
Native diagnostics use real native owner/session/epoch/relation/logical-row
identities, never invented backend PIDs or physical tuple addresses.

Row candidates and initial ordering freeze before waits. Latest same-row recovery
checks exact storage/row/version identity; it does not discover new source rows
or join combinations or sort again. Retained output caches own occurrence identity
separately from physical row slots. Qualifying replacements refresh referenced
outputs at their original query level without replaying already demanded source
or default effects. View lineage retains hidden physical contributors without
exposing them as public result columns.

Pending view-local marks settle at their consuming execution frontier, before
upper aggregation/count/publication where required. This eager frontier is not
PostgreSQL optimizer parity: native can retain locks for an executed input even
when an upper constant predicate produces no result. Bounded demand stops
unvisited tails; blocking upper stages can consume a larger marked input. Public
Next never performs these waits.

Stable-snapshot row recovery retains key-change history, including representation
changes and changed-then-restored keys. Immediate uniqueness uses typed key claims
in the same manager; waits recapture index roots and overlay private prefixes.
Payload and exact-datum no-ops do not invent key changes. Virtual catalogs reject
native physical row locking; PostgreSQL catalog locks are independent controls.
Native eager failure returns no partial result, unlike pq streaming failure after
an acquired prefix.

## Whole-table generation replacement

Truncation installs empty private storage incarnations/index baselines for the
selected physical group at one commit frontier. Virtual roots participate in
authority and locks without invented storage. Old vectors, end roots, codecs and
definitions remain retained by their owners. Repeated replacement retires current
private writes while preserving the first committed predecessor fences;
rollback/savepoint recovery restores exact roots and provenance.

Grant-time graph expansion, activity/rights rechecks and final authorization,
source-storage, cancellation and host-graph fences veto the entire group. A
bounded lineage token permits only proven continuity with an earlier committed
truncation, never unrelated rewrites or same-name replacement. Retained-frontier
birth filtering hides later inserts without copying frozen prefixes. Immutable
eager Rows can drain afterward; retained live SQL producers still pin activity.
Generator/cache/session-value state is unchanged by nonrestart truncation.
RESTART IDENTITY completes table closure first, then acquires ACCESS EXCLUSIVE
on every owned sequence and checks sequence ownership (ordinary sequence grants
are insufficient). Sequence-row readers and all four sequence functions,
including cached/remembered calls, wait behind reset until transaction end.

Fresh scalar generators start at each configured START with is_called=false;
their descriptors, prepared dependency stamps and table/index roots share the
same private overlay and final publication boundary. Final fences cover incoming
host ownership changes, exact generator bindings and catalog/control epochs.
Savepoints restore bindings and row roots, never snapshots of mutable sequence
counters. Ordinary nextval/setval before reset remain on the binding used at
invocation; allocations on an unchanged committed binding survive rollback.
Replacement-local allocations disappear when that binding is rolled back.
Earlier own sequence DDL obeys its existing binding lifetime, and repeated resets
retain the first committed fences.

Reset itself discards only the invoking session's unused cache tail, even when
it rolls back without another sequence demand. Generation changes discard stale
tails on later demand; a peer still using the original binding can retain its
tail after rollback. Reset alone does not alter currval/lastval, and local values
observed on a reverted replacement remain valid afterward. Session observations
and cache-tail discard are not transactional undo state. Dump observes committed
bindings, not private resets or physical-session memories. Trigger/FDW and
partition-FK providers remain excluded.

Prepared sequence literals use the existing relation-source stamp, not generator
state or descriptor revision as an invalidation token. A committed rewrite's
descriptor stamp informs peers. A reverted rewrite instead retains an analysis
stamp only in the invoking physical session's existing sequence-memory entry:
the effective source stamp is the maximum of those two stamps. This invalidates
the whole caller literal group even without demand during the reset transaction;
an untouched peer retains its original held binding after rollback. Zero-valid
stamp-only entries do not define currval/lastval. DISCARD SEQUENCES clears values
and cache tails but preserves this analysis-only event; DISCARD ALL already has
its full plan-generation invalidation. Physical end and persistent Reset/Restore
cleanup remove the event; exact caller-owned live TEMP provenance retains it.

## Temporary object lifetime

TEMP descriptors, rows, dependencies and commit actions share transaction and
savepoint roots. Commit actions join ordinary preparation before the final
publication fence, not fallible cleanup after success. Rollback/abort does not
run successful commit actions. Physical end aborts private work and removes only
that owner's complete TEMP dependency closure.

Cleanup projects surviving shapes and rebuilds affected dense-slot indexes while
preserving ready/valid/live state; it does not heal invalid indexes. Final fences
cover catalog, projected stores/vectors/end roots/generations and index identities.
Only physical retirement permits exhausted counters to remain saturated; ordinary
DDL retains exhaustion errors. Pool return is not physical end. Reset/Restore
preserves live TEMP identities while fencing transactions born before replacement.

## Index build, maintenance and concurrent completion

Ordinary builds seed from grant-time committed live rows under the existing
relation manager; prior committed writers are included, rolled-back writes absent.
Authority is rechecked against exact granted identities, never by retrying a
recreated name. Maintenance installs real extracted/sorted/validated roots
atomically, preserving unchanged siblings' newest roots. Ordinary build and
maintenance retain native explicit-block exclusions; read-only rejection precedes
lookup/waits. Published roots are immutable and do not replace snapshot visibility.

Concurrent completion waits for exact captured writer-cohort top-level outcomes.
Savepoint contribution release is not completion and later writers cannot extend
a captured cohort. Visibility XIDs are engine-global first-write/catalog assigned,
distinct from commit sequence and per-session transaction identity. Actual root
capture advertises xmin under the same engine critical section; stable snapshots
remain owned after eager rows close, read-committed Finish releases ownership.
Builders drop their own snapshots before waiting. Only eligible simple builders
receive safe-builder exemption. See
[concurrent design](design/PG18-INDEX-CONCURRENT.md) and
[index maintenance design](design/PG18-INDEX-MAINTENANCE.md).

## Foreign-key and deferred-event publication

Foreign actions settle real row/version transitions in the same statement stage
using an iterative worklist. No hidden transaction, FK queue or SQL replay exists.
Child admission locks actual referenced RowIDs; post-wait recovery checks live keys
and preserves unrelated latest payload. Generated actions use canonical relation,
row and typed key claims with ordinary savepoint/prepared ownership.

Selective deferred exclusion events share existing completion resources; metadata
is not a grant owner or query view. Providers and completion waits run outside
publication/manager metadata locks. Savepoints restore event state and prepared
transfer retains original tickets. A second exclusion pass runs only when foreign
action settlement admitted a new event, not for unrelated work. Counts/results
include initiating rows only and successful proofs are not rechecked under later
ACL/comment metadata. Deferred/temporal/partition foreign keys remain capability
boundaries, not physical PostgreSQL RI-trigger parity.

## Catalog replacement and final publication fences

Reset/Restore are trusted native fixture controls, not wire commands or physical
termination. Exact Driver/DSN sharing controls replacement scope. One complete
catalog epoch replaces committed state without merging old private writes.
Failed/canceled/read-failing Restore before installation leaves committed state
and eligible work unchanged. An arbitrarily blocking Reader must return before
cancellation is observed; target locks are not held over external I/O.

Replacement admission is a logical reservation, not a mutex held across resource
waits. Queued row work relinquishes it and readmits/revalidates exact relation and
epoch after grant. Provider validation runs outside replacement/publication locks.
A changed name-resolution target after grant returns the replacement error before
acting on an unlocked shadow; native does not retry SQL. Successful replacement
retires old holders/waiters without waiting for an exclusive owner to end.

The exact committed database predecessor is captured separately from private
database authority, including storage-only stages and savepoint roots. Owner-only
replacement vetoes Finish/outer commit even without changed role revision/epoch.
Prepared path authority follows canonical database identity, not a fabricated
revision or live fallback.

Old-epoch commits are fenced even when read-only or already aborted; accepted
failed commit disposes that incarnation without installing a chain. Full rollback
remains recovery-eligible. Retained handles/results/session identities/settings
survive according to their owners, but stale private marks cannot publish.

Final cancellation/end observed before installation publishes nothing; completed
installation survives later cancellation. Identity/command/frontier exhaustion
fails before wrap or publication. This is process-local atomic visibility, not
WAL, crash recovery, physical durability or a network PostgreSQL server.

## Transaction timeout lifecycle

Parse admission owns a provisional budget, not a premature transaction reference.
Actual birth adopts it once. Callbacks validate physical token, scope/reference
and generation under the session mutex, then request canonical asynchronous
termination without acquiring transaction/engine/resource-manager locks.

Before transaction FATAL, an unfinished command's already-due statement/queued
lock ERROR wins even if callback delivery is delayed. Exact requests arbitrate
ERROR versus terminal claims; successful grant cannot race a claimed ERROR into
success. Only the manager grants/removes requests. Stale references and completed
commands cannot suppress genuine FATAL.

Full/child ERROR recovery stops the budget; setting restoration does not rearm it.
Fresh birth or explicit assignment can. Detach retires session timing while
prepared roots survive. Fatal cleanup releases attached private resources, never
peer permanent data or detached entries. See [timeouts](SETTINGS.md#timeout-behavior).

## Testing and operational guidance

Native mechanics and public reference witnesses remain separate. Relevant selectors:

```sh
make check RUN='^TestReferenceTransaction[A-Za-z0-9_]*$'
make check RACE=1 RUN='^TestReferenceTransaction[A-Za-z0-9_]*$'
make check REFERENCE=1 RUN='^TestReferenceTransaction[A-Za-z0-9_]*$'
make check RUN='^TestReferenceSerializable'
make check REFERENCE=1 RUN='^TestReferenceSerializable'
make check RUN='^TestReferenceLockModes'
make check REFERENCE=1 RUN='^TestReferenceLockModes'
make check RUN='^TestReference(QueryLock|RowLock|QueryLimit|QueryOuterJoins)'
make check REFERENCE=1 RUN='^TestReference(QueryLock|RowLock|QueryLimit|QueryOuterJoins)'
```

Native queue witnesses use real engine state; a public deadline alone does not
prove enqueue. PostgreSQL resumed-version tests use actual wait-state evidence,
not a fabricated native tuple Context. Consume full pq Rows.Err and close
streaming rows/statements before recovery. Retry serialization failures as whole
transactions; bound acyclic waits with lock timeout or caller deadlines. Native
replacement errors have no PostgreSQL command-equivalence oracle. See
[testing](TESTING.md) and [development](DEVELOPMENT.md).

Partitioned exclusions retain physical-leaf conflict ownership and the existing
selective event/completion authority. Parent-named SET CONSTRAINTS selects the
inherited provider subtree; declaration flags remain unchanged by transient modes.
Selected recursive EXCLUDE ADD/DROP stages the complete provider closure before
publication. A late coverage/build conflict or cancellation publishes no prefix.
Hierarchy INSERT atomicity, savepoint rollback and prepared completion use the same
immutable roots as ordinary exclusions; this does not enable hierarchy UPDATE,
DELETE, row movement, arbitration or MERGE.

