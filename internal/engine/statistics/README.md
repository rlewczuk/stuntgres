# Cumulative statistics ownership

`Provider` belongs to one database engine and publishes copied Fact values keyed
by category and domain/epoch-qualified identity. Its mutex is a leaf: no engine,
settings/session lock, callback or I/O runs beneath it. Retirement/reset fence
late pending writes without mutating retained Reader cuts or unrelated keys.

A physical session owns one Writer for its database provider, serialized by
transaction admission. Event-time policy and provider generation qualify pending
facts. Captured multi-provider work never shares a mutable Writer across owners.
Collection-disabled paths allocate no per-row state or clocks.

Tuple attempts remain unresolved in transaction/statement Ledgers until real
child/top completion. Successful children merge privately; savepoint/exception
abort, truncate and prepared ownership preserve their actual causal accounting.
PREPARE transfers unresolved work rather than publishing or replaying it at the
preparing session's exit. Ordinary read/maintenance events retain distinct
nontransactional ownership and do not become tuple ledger effects.

Only genuine idle outside-transaction boundaries flush settled pending batches.
Publication is throttled per writer; reset/generation and late-entry retirement
fences remain independent of retry eligibility. Core owns lifecycle callbacks,
nonblocking admission and cancel/join of deferred flush work outside locks.

A transaction owns one Reader: `none` reads published facts, `cache` retains whole
accessed objects including absence, and `snapshot` takes one coherent captured
domain cut on first cumulative access. Timestamp inspection never creates that
cut; clear discards it without publishing Writer state. Core supplies shared peer
facts through the same capture, never a second per-view collector or cache.

Physical public activity membership and immutable login/birth identity are
separate from cumulative counters. Current observer masking is reapplied by the
runtime after raw capture. Retained activity facts do not grant current authority
or prove that a recycled numeric session/PID is the same physical owner.

Native technical metrics model explicit logical events, not PostgreSQL physical
pages, WAL, workers or OS activity. Runtime collectors own what happened;
producers project detached facts and preserve genuine absence without guessing.

`Statements` is a separate bounded cluster accumulator with a leaf mutex. Active
scalar admissions, planning/execution phases, representative text and reset/
replacement generations have distinct lifetime. Eviction removes history, not
eligibility; terminal ownership releases each admission exactly once. No session,
executor graph, catalog callback or permission is retained by the accumulator.

## Testing

Pure provider/writer/reader/ledger/reset units live here with deterministic
publication and joined concurrency controls. Core tests cover admitted idle,
transaction/prepared lifecycle and actual capture/reporting. Public SQL belongs
to reference packages under [docs/TESTING.md](../../../docs/TESTING.md).
