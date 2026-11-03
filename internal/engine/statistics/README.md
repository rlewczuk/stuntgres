# Cumulative statistics ownership

`Provider` belongs to one database engine and publishes copied `Fact` values,
keyed by domain/epoch-qualified object identity and category. Its mutex is a leaf:
no engine, settings, session, callback or I/O operation runs beneath it. Full
`Reset` advances the reset generation; old pending batches cannot cross it.
Targeted `ResetEntry`/`ResetCategory` and `ResetSubscriptionOID` instead change only
existing replication-slot or subscription counters and per-entry `ResetAt`, never
generation, global reset time, tombstones or pending ownership. Missing keys/OIDs
do not allocate. OID reset scans the existing leaf map for a complete subscription
address/category and updates the original stored domain/epoch key; catalog
visibility is not a reset prerequisite. Creation initializes records immediately,
while staged DROP retains them until commit and creation abort retires them.
There is no parallel OID registry or projection-owned statistics copy. Legacy
categories retain their database reset timestamp; absent per-entry categories have
NULL reset timestamps, including in retained full snapshots. `EnsureSubscription`
is reserved for actual admitted creation, never a reset/read. Category merges add
pending counters without replacing the current shared per-entry reset timestamp.
This package never mutates physical storage or manufactures disk/WAL operations.

`Relation` facts include additive `SequencePageAccesses`, independent of tuple,
maintenance and IO-operation fields. It counts successful generator encounters,
not sequence values: a real nextval cache reservation, successful setval, an
admitted direct sequence scan, or an authorized demanded pg_sequences last_value
read (including an uncalled NULL). Cached nextval, currval/lastval, definition
reads and rejected calls add nothing. The current event's Counts policy gates
collection before writer work; other tracking switches do not. Suppression never
changes generator state or value-cache tails and enabling never replays events.
Existing-object encounters survive later rollback and publish through the same
writer gate/throttle. Relation retirement rejects late batches. Reset fences
pending counts without clearing sequence value caches. Projection after the
Reader gives one logical resident page: N=0 maps to reads/hits (0,0), N>0 to
(1,N-1), regardless of concurrent first-batch ordering. Missing facts are zero.
Native replacement starts persistent/imported histories empty and transplants
only published facts of surviving attached destination TEMP identities into the
new epoch; old pending work is fenced. Dump contains no monitoring history.

Seven fixed `SharedKind` families and eight SLRU entries belong to this same
provider. `ResetShared`/`ResetAllShared`/`ResetSLRU` modify published state only.
IO/WAL keyed facts remain the sole operations/duration authority and use their
family reset times, including absent keys. Archiver reset removes last-operation
presentation; prefetch resets cumulative counters but preserves its three current
gauges. Full-session-token backend partitions contain separate IO/WAL facts;
missing resets never allocate. `BeginBackend` grants positive authority only
from successful actual public-session publication. Both report and flush require
that membership; End deletes membership and facts rather than retaining ended
tokens. Invalid, detached and private identities never receive membership, and
their end paths allocate no history. State is bounded by current public lifetimes,
not connection churn, and cannot retain private session owners.
Full Reset/Replace clear fixed/backend history with the database incarnation,
retain current live registrations and advance the existing pending-generation
fence. New actual target publication inherits only positive registrations under
cluster serialization, using separate provider lock acquisitions, never metrics
or reset history. No monitoring history is serialized or activity manufactured.

Fixed, SLRU and backend event deltas share `Writer` generation, publication gate
and throttle with keyed work; targeted resets never touch any pending partition.
The same Reader captures all modeled partitions coherently on first snapshot
access. Cache mode retains fixed families as whole kinds, including the entire
SLRU array; prefetch always reads current provider state without constructing a
snapshot. Clear releases every cumulative partition and cached activity together.
The core traverses actual per-database providers for cluster-wide reset effects;
there is no second cluster provider or competing IO/WAL counter copy.

A physical session owns one `Writer`, serialized by transaction admission. Policy
is captured when events occur. Tuple attempts remain unresolved in a write-owned
`Ledger` until their reporting boundary, separate from the writer's pending batch.
Statement segments merge at their actual nesting level; savepoints clone roots,
and immutable parent frames preserve current and first-pre-truncate counters.
Child abort moves only that child's restored attempts/dead into local pending;
successful child release merges privately, with distinct truncate rules.
Top-level commit reports surviving current work; abort restores first-pre-truncate
work. PREPARE transfers unresolved frames without reporting on preparing exit;
only completion settles them into the finishing writer. Every reported insert,
including aborts, contributes to inserts-since-vacuum. Statistics collection never
creates a catalog journal or changes physical publication.
Maintenance uses immediate atomic `Provider.ReportAnalyze`/`ReportVacuum` updates,
not the ordinary writer batch. Reports preserve pending tuple prefixes and their
throttle/timer; later drains add those deltas after the shared reset. ANALYZE
subtracts active current I/U/D and generation-valid pending dead estimates from
its raw sample, never saved pre-truncate counters or pending committed live.
Column-list ANALYZE retains modifications; full ANALYZE resets them. VACUUM
resets shared inserts-since-vacuum only. Existing-object reports survive abort.
New private-object reports are immediately owner-readable. Relation, function and
subscription creations discarded by abort retire their exact category-qualified
keys; explicit drops retire on successful commit, including existing objects and direct/prepared
publication. Rolled-back drops, live compatible replacements and creations owned
by either restored savepoint/statement journal retain their facts. Completed calls
on existing functions survive unrelated rollback. Settlement precedes retirement.
`Provider.Retire` removes the shared fact and installs a tombstone without requiring
a session; `Writer.Retire` additionally removes only its local pending key.
Tombstones reject late batches, reports and active usage completions, survive
statistics-only reset, and end at catalog replacement. Unrelated facts, pending
ownership, generations, throttles and retained readers are unchanged.
Drop/recreate successors have independent identities.
Physical optimizer/storage bookkeeping remains independently owned.
Function bodies use `BeginFunction`/`EndFunction` under writer ownership. Usage
captures generation-qualified same-key pending total and backend charged-self
baselines. Completion replaces only the overlapping pending inclusive total and
adds each call/self interval; distinct keys retain independent inclusive totals.
Failed bodies do not complete usage; their completed descendants remain pending.
Selection precedes clock/frame work and excludes inlining and stock internal
routines. No active invocation may publish its pending batch.
Ordinary and WAL I/O keep operation counts when their independent timing switches
are off. No clock is sampled by these category methods; callers supply actual
measurements only when enabled.

Bulk population captures policy and attempts at staging. Existing overlay
acceptance transfers private estimates into the statement write ledger; successful direct
utility publication releases committed estimates without changing its physical
timing or late-Finish contract. Concurrent refresh counts come from the existing
matcher, not a second row comparison or the physical heap-replacement counters.
Native replacement seeds only imported live population, not replayed attempts or
maintenance history, at the accepted installation boundary.

Only truly idle outside-transaction boundaries call `Flush`; active, aborted and
retained command/transaction blocks withhold even locally settled child events.
A monotonic clock throttles normal publication to once per second per writer.
The first nonempty batch is immediately eligible; throttled pending arms one
ten-second idle retry without resetting its deadline on later commands.
Expiry marks a request: busy/transactional owners defer it until true outside-TX
ready; a truly idle owner force-flushes without another SQL command. Normal drain
cancels obsolete retries. Session end invalidates and joins callbacks outside
locks, then force-drains after transaction cleanup. Truncate resets shared
live/dead/since, not historical attempts; every reported insert in the coalesced
batch still contributes to since-vacuum. Reads/clear never flush. There is no
periodic worker, polling loop or SQL force-flush helper.

A transaction owns one `Reader`. `none` reads published facts, `cache` retains
whole accessed objects (including missing entries), and `snapshot` coherently
captures all database facts and a construction timestamp on first cumulative
access. Timestamp inspection never constructs a capture. Clear removes cumulative
and independently cached activity information without changing counters, reset
state, pending work or the publication throttle. Effective setting generations
invalidate even a mode round trip between accesses. Returned facts/maps are owned
copies. Session activity has separate active/idle/disabled publication state and
does not change `current_query()` command authority. The Active flag owns actual
command execution even when Disabled hides source/timestamps/ID; genuine live
lock/sleep wait publication is independent of the activity tracking switch.

The default database's existing provider owns the sole cluster activity map;
other providers retain their existing cumulative/backend facts only. Successful
physical public attachment registers detached birth/database/login principal identity.
Command, transaction, setting-restoration and drained-retirement owners publish
actual facts, with exact command guards for query IDs and sleep wait callbacks.
Reset/Replace never remint backend birth or erase live activity.
Core enriches one complete unmasked cut with canonical assigned low32 XID and
accepted public full-XID horizons under cluster authority, then installs it in
Reader independently of cumulative modes/stamp. Each execution overlays detached
manager waits and live sleep ownership after releasing cluster synchronization;
current inherited-role masking happens only in the pure catalog producer.
The provider does not cache a role spelling: the producer joins the current role
name by immutable principal identity, with typed NULL for an absent role. A
dropped cached target does not invalidate unrelated rows or legitimate observer
stats privilege; foreign/zero principals remain invalid.
No peer admission, callback, setting read or clock runs under Provider.mu.
Query text is UTF8-safe byte-clipped separately from current_query authority.


`Statements` is a separate cluster-owned bounded statement accumulator. Its leaf
mutex protects owned entries and scalar active admissions, never a session,
executor graph or catalog callback. `Begin` registers admitted and known
prospective terminal keys together. `RecordPlan` and `RecordExecution` consume
explicit successful key, cleaned raw source, timestamp, rows and measured time.
An unanticipated terminal key registers atomically with its first record; earlier
resulting-key-only clears are not retroactive. A destructive admitted-key clear
always fences the original work. `Release` ends admission ownership on every
successful or failed terminal; released tokens cannot publish.

Eviction removes history, not active eligibility: later valid success merges into
the current entry or allocates bounded new history using successful raw source
and time. Existing representatives remain first. `Clear` matches active key slots
even after eviction; minmax fences are key-specific and preserve cumulative
moments. Owned snapshots survive all mutations. Actual all-entry removal resets
global info, including filtered removal and the empty-store minmax edge.
Planning and execution use Welford population moments. Sticky usage starts at
the current median (initially10), decays by0.5 and resets to1 before first-success
increment; completed usage decays by0.99. Deallocation takes the sorted
pre-removal upper median and removes a least-used batch; `Info.Dealloc` counts
batches, not victims. Runtime history and active admissions are not persistent
SQL data.
Zero-sample sticky admissions are not visible rows. Native planning samples
represent real accepted construction events, including repeated prepared builds;
fold hits do not fabricate generic-plan reuse. Exact accumulator arithmetic is
independent of cross-engine clocks and physical planning invocation frequency.

## Testing

Deterministic publication, reader, suppression, identity/reset and concurrent
merge tests use `make check RUN='^TestStatsProvider'`; add `RACE=1` for concurrency.
Sequence encounter merging, reader snapshots and identity/reset fencing use
`make check RUN='^TestStatsSequence'`, also with `RACE=1`.
Nesting, truncate, COW and pending-prefix accounting uses
`make check RUN='^TestStatsSnapshotTransactionAccounting$'`, also with `RACE=1`.
Public SQL scenarios live in `reference/session_state`, using
`make check RUN='^TestReferenceStatsSnapshot'` in native and `REFERENCE=1` modes.
Statement arithmetic, explicit admission release, filtered/minmax clear fencing,
bounded sticky/median eviction and concurrent snapshots use
`make check RUN='^TestPgStatStatements(Accumulator|Clear|Eviction)'`, also with
`RACE=1`.


Core outside-transaction gating, virtual-time one-shot retry and callback lifetime
proof uses `make check RUN='^TestStatsSnapshotIdlePublicationAuthority$'`, also with
`RACE=1`; no public test waits ten seconds for the retry.

Immediate maintenance/reset/peer ordering and atomic capture use
`make check RUN='^TestStatsProviderMaintenanceReporting$'`; runtime sampling,
ordinary retry preservation and creation lifetimes use
`make check RUN='^TestStatsSnapshotMaintenanceReportingAuthority$'` (also `RACE=1`).
Public open-transaction none/snapshot/clear witnesses use
`make check RUN='^TestReferenceStatsSnapshotMaintenanceReporting$'`, and again
with `REFERENCE=1` against pinned PostgreSQL.
