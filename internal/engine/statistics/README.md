# Cumulative statistics ownership

`Provider` belongs to one database engine and publishes copied `Fact` values,
keyed by domain/epoch-qualified object identity and category. Its mutex is a leaf:
no engine, settings, session, callback or I/O operation runs beneath it. Reset
atomically advances the reset generation; old pending batches cannot cross it.
This package never mutates physical storage or manufactures disk/WAL operations.

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
New private-object reports are immediately owner-readable. Relation and function
creations discarded by abort retire their exact category-qualified keys; explicit
drops retire on successful commit, including existing objects and direct/prepared
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
periodic worker, polling loop or SQL force-flush/reset builtin.

A transaction owns one `Reader`. `none` reads published facts, `cache` retains
whole accessed objects (including missing entries), and `snapshot` coherently
captures all database facts and a construction timestamp on first cumulative
access. Timestamp inspection never constructs a capture. Clear removes cumulative
and independently cached activity information without changing counters, reset
state, pending work or the publication throttle. Effective setting generations
invalidate even a mode round trip between accesses. Returned facts/maps are owned
copies. Session activity has separate active/idle/disabled command state and does
not change `current_query()` command authority.

## Testing

Deterministic publication, reader, suppression, identity/reset and concurrent
merge tests use `make check RUN='^TestStatsProvider'`; add `RACE=1` for concurrency.
Nesting, truncate, COW and pending-prefix accounting uses
`make check RUN='^TestStatsSnapshotTransactionAccounting$'`, also with `RACE=1`.
Public SQL scenarios live in `reference/session_state`, using
`make check RUN='^TestReferenceStatsSnapshot'` in native and `REFERENCE=1` modes.

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
