# Logical decoding: native change stream and logical slots

## Decision

The native engine has a real logical change stream and real logical replication
slots (catalog part `PG18-ADMIN-LOGICAL-STREAM`, which owns the logical-slot
contracts of `PG18-ADMIN-LOGICAL-DECODING` and
`PG18-ADMIN-REPLICATION-SLOT-LIFECYCLE`). Output plugins are embedded Go providers supplied
at engine composition; this foundation registers none, and `test_decoding`
belongs to `PG18-EXT-TEST-DECODING`. Streaming of in-progress transactions and
table rewrite changes are not produced: a plugin that needs either fails with
0A000 rather than receiving different output.

The observable SQL contract is in [DRIVER.md](../DRIVER.md#logical-replication-slots-and-decoding).
PostgreSQL 18.6 sources (`REL_18_6`) are the authority for ordering and edge
cases: `logicalfuncs.c` (`pg_logical_slot_get_changes_guts`), `slotfuncs.c`
(create, copy, advance, drop), `slot.c` (`ReplicationSlotCreate`,
`ReplicationSlotAcquire`), `logical.c` (`CheckLogicalDecodingRequirements`,
`CreateInitDecodingContext`, `StartupDecodingContext`,
`LogicalConfirmReceivedLocation`), `decode.c`, `reorderbuffer.c` and
`postgres.c` error recovery (`ReplicationSlotCleanup`).

## Ownership

| Owner | Responsibility |
|---|---|
| `internal/engine/logical` | Change, relation-snapshot and record types; per-database `History`; the position `Allocator`; the plugin interfaces; the pure `Decode` loop. No lock. |
| `core` capture (`logical_capture.go`) | Ordered changes at the four staging windows and TRUNCATE; CREATE TABLE AS rows on the catalog stage. |
| `session.TransactionOwner` | Merged successful statements' changes; savepoint marks keep clipped copies; prepared entries carry them. |
| `core` history (`logical_history.go`) | Commit, prepare and prepared-finish records at the publishers' callback-free installation points. |
| `core` slots (`logical_slots.go`, `replication_slot_runtime.go`, `logical_decoding_runtime.go`) | Cluster-wide registry, holder sessions, creation wait, get/peek/advance/copy/drop, session and error cleanup, catalog replacement. |

`DatabaseCluster.Mu` (every `Engine.Mu`) guards slots, history and positions.
Plugin callbacks and the creation wait run with no engine lock; a held slot is
released under the lock on every exit path. Capture runs only while the cluster
has a slot (WAL is cluster-wide, so a copy made in another database finds that
database's history), and slots exist only with `wal_level=logical`, so the
default configuration pays nothing.

## Stream model

Every record (a captured change, a commit, a prepare, a prepared finish) takes
one fixed span from the cluster allocator, so record boundaries follow from a
position. Changes take their position when captured, in the statement's write
order; failed or rolled-back changes leave gaps, as aborted WAL does. Changes
follow NOTIFY events exactly: they merge into the owner on statement success,
savepoints and PL/pgSQL exception blocks restore clipped copies, and failure or
abort discards them. A commit appends one record holding the transaction's XID,
timestamp, origin and changes; a transaction with catalog writes but no changes
records an empty transaction, while one with neither (an XID only, temporary or
unlogged writes) records nothing. PREPARE records its content on the prepared
entry; COMMIT and ROLLBACK PREPARED reference it. Relation snapshots (schema,
names, columns, types, replica identity key) are taken at capture, which gives
PostgreSQL's historic-catalog results.

`Decode` mirrors the get/peek loop over an immutable history snapshot: it reads
from the restart position to the frontier taken at call start and checks the
limits after each record; a transaction is emitted when its record starts at or
after `confirmed_flush`. Commit-ordered whole-transaction replay gives
PostgreSQL's ordinary output; two-phase slots replay at PREPARE. Get confirms the
end of the last processed record; peek does not.

## Slots and lifecycle

A new slot is published at once as in progress (capture starts and the name is
taken), then starts its plugin, waits for every XID-holding transaction of the
cluster and takes the stream end as its consistent point. A transaction without
an XID at that moment is captured from its first write, so it is decoded whole
when it commits after the consistent point and skipped when it commits before.
A temporary slot stays held by its session; any top-level statement error and
session end drop it.

Every database's history is trimmed to the lowest confirmed position of the
cluster's slots and cleared when there are none. Two-phase slots replay a
transaction whose PREPARE preceded `two_phase_at` whole at COMMIT PREPARED, as
`ReorderBufferFinishPrepared` does. Catalog replacement (Reset, Restore) drops the
database's slots and history, since the replacement's history is unrelated; a
held slot refuses it (55006). Dump carries neither slots nor positions.
