# Logical decoding: native event history and logical slots

## Decision

The native engine owns a logical event journal and SQL replication slots.
`test_decoding` is a stock embedded provider, composed once at startup without
an extension installation or shared-library loader. Explicit startup factories
may intentionally replace a stock name; the retained EngineFactory preserves
that exact composition through Reset/Restore. Plugin allow-list admission still
precedes provider startup. Physical WAL storage/replay, wire replication and
native C ABI loading are separate boundaries, not substitutes for SQL decoding.

The original logical-stream foundation deferred in-progress streaming and
rewrite capture. PG18-EXT-TEST-DECODING explicitly owns those extensions under
the authorization “Extend TEST-DECODING ownership”; ordinary and custom-provider
contracts remain on the same journal, binder, slot and callback paths.

PostgreSQL [18.6 test_decoding.c](https://github.com/postgres/postgres/blob/REL_18_6/contrib/test_decoding/test_decoding.c),
[logicalfuncs.c](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/replication/logical/logicalfuncs.c)
and [reorderbuffer.c](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/replication/logical/reorderbuffer.c)
ground formatting, record limits, fresh contexts, streaming and two-phase output.
The public contract is [DRIVER.md](../DRIVER.md#logical-replication-slots-and-decoding).

## Owners and synchronization

| Owner | Responsibility |
|---|---|
| `logical` | Immutable Change/Relation/Record/Transaction, History/Allocator, output interfaces, per-call decoder/reorder buffers. No locks. |
| `logical/testdecoding` | Fresh stock formatting/options context and per-top-transaction output flags; no catalog lookup or slot state. |
| `core` capture | Real pre-index insertion attempts, accepted row windows, truncate and catalog/invalidation events, actual rewrite destination rows and historic schema/types. |
| `session.TransactionOwner` | Successful-effect projection; real savepoint/exception generations and lazy canonical child XIDs. |
| `visibility.Registry` | The single full/low XID allocator, live-alias checks, child ancestry, subtree abort and parent completion/timestamps. Children introduce no additional MVCC, SSI or lock owner. |
| `core` publishers | Commit, abort, prepare and prepared-finish evidence at actual callback-free transitions. |
| `core` slot runtime | Cluster registry, acquisition, immutable frontier capture, successful confirmation, copy/advance/drop and native replacement. |

Transaction-owner admission and Mu precede Engine.Mu. Engine.Mu (the cluster
mutex) guards event append, status transitions, positions and slot snapshots.
Formatting, output callbacks and waits never run under Engine.Mu. Every decoding
exit releases the held slot and shuts down its fresh plugin. Final get
confirmation uses the operation's Session.Mu publication/cancellation fence.

## Event history and immutable payloads

Each event occupies one fixed allocator span. Real row staging publishes owned
immutable tuple vectors and capture-time relation/type descriptors immediately;
failed uniqueness therefore retains its attempted insertion evidence before
rejection, without publishing a table effect. Catalog overlay installation
records genuine metadata/invalidation evidence, including empty streamed blocks.
Destructive SQL rewrites capture their actual transformed destination vectors,
not a later scan, and tag only those events as rewrites. Metadata-only ALTER and
row-shape-preserving copies do not produce rewrite rows.

Successful statements additionally merge into the existing LogicalChanges
projection used by ordinary commit and prepared entries. Savepoint/exception
rollback clips that projection but records a genuine subtree abort; it cannot
erase the event journal. Released descendants retain canonical ancestry, so
parent rollback includes them. Repeated ROLLBACK TO starts a newly assigned
child generation. Top-only inquiries and read-only marks allocate no child XID.
All ancestor assignments preflight exhaustion/live aliases before publication.

Records and nested payloads never change after publication. Snapshot slices have
clipped capacity; trim creates new backing without modifying a held snapshot.
Terminal projections share immutable payloads rather than re-encoding tuples.
Dropping the last slot releases the database histories and open anchors; held
snapshots remain valid until their consumers release them.

## Fresh decoding and stream scheduling

Every get/peek creates a fresh plugin and fresh reorder state. Reading starts at
restart, queues old events, and stops at the captured frontier. Confirmed flush
gates new stream triggers, not every output row. Thus an active get followed by
commit-only can replay the whole ordinary transaction in the next context;
a new eligible DML trigger can instead stream both old and new queued changes.
Unchanged-status/frontier peeks repeat; changing transaction status can change
what a fresh context decodes from the same retained prefix.

Immediate mode drains complete positive-memory event batches. Buffered mode
triggers at total pending decoded memory **>=** the budget and chooses the
largest eligible top transaction, with first-event position breaking ties.
Actual queued DML/truncate/rewrite evidence makes the top eligible for this
context through drains and child aborts, independently of rewrite visibility.
Catalog/invalidation work alone cannot make a top streamable; it still counts
toward memory and forms empty blocks after genuine streamable work. Origin
filtering occurs before queue eligibility.
The budget is deterministic logical accounting: fixed event header, typed value
slots and canonical payload bytes measured once at capture. JSONB counts its
decoded nodes/keys/scalars once, not its redundant rendered-text cache. It is
neither retained Go heap accounting nor physical WAL/heap/TOAST byte or
segmentation equivalence.
Buffered decoding prunes terminally aborted work. Immediate mode bypasses that
precheck but still honors catalog liveness: completed top aborts cannot open a
fresh catalog context; a child may use the relation already opened by its parent.

Streams contain complete blocks, changes/truncates and true child aborts, with
stream commit/prepare only when that context actually streamed the transaction.
Filtering rewrites/origins precedes the stock plugin's empty-output decisions.
Plugins without StreamingPlugin retain ordinary committed replay. Two-phase
plugins retain separate prepare/finish callbacks; non-two-phase slots defer
prepared contents until COMMIT PREPARED.

Limits are checked after complete input records. Ordinary replay emits a whole
transaction; streaming may stop after one complete block. Get confirms the
processed input position even if no output was produced; peek never confirms.
Callback, formatting, startup and cancellation failures never confirm. Eager
successful materialization can consume even when the caller closes Rows early.

## Retention and native controls

Restart retains every prefix needed by an unfinished, prepared or unconsumed
transaction, even after get confirms beyond its first event. Trim uses the
minimum slot restart, not simply confirmed flush. Copy/advance/drop and multiple
slots share this owner; there is no persistent slot decoder registry.

Creation waits cancellably for XID-holding transactions before selecting its
consistent point; copies use their source positions. Temporary slots belong to
the physical session and disappear on session end or top-level error.
Dump serializes persistent SQL objects/data and recipes, not slots/history/LSNs.
Successful Reset/Restore retires old slots/history atomically while preserving
startup providers. Held-slot replacement fails without changing original
provider, recipes, data or confirmed position.
