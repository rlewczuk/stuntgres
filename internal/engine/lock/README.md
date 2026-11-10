# internal/engine/lock

Heavyweight lock manager below catalog/runtime layers. It depends on identity,
scalar and diagnostic facilities, never core, catalog or sessions.

## What it owns

`Manager` owns registered transaction/session tickets, typed resources/modes,
holder/wait queues, deadlock detection, grant journals/checkpoints, unique-key
claims, tuple/MultiXact state and immutable inspection snapshots.

`StartAcquire` atomically grants or enqueues a request; waiting and terminal
completion belong to that exact request generation. Savepoint checkpoints restore
ticket contributions, not a second engine undo journal. Scoped relation
contributions are independent of transaction-wide holds and release only their
own grant. Domain retirement is terminal lifetime, distinct from epoch fencing.

## Mutex ownership and lock order

`Manager.mu` guards manager maps, queues, journals and counters. Clients use
methods rather than touching state. No method runs a callback under it.
The manager mutex is innermost and never acquired under `Engine.Mu` or a session/
settings/registry mutex. Metadata capture and post-wait readmission are separate
engine windows; manager waits run outside all metadata locks.

Surrounding runtime order takes replacement/transaction-owner locks before
Engine, then Session/settings/local registry locks. Multi-owner capture discovers
pointers under the registry lock, releases it, then locks distinct owners in stable
order before Engine and revalidates the cohort. It never waits for peer admission
or completion while retaining the cohort.

Ticket atomics expose assigned XID/completion state without manager locking;
their engine-side updates still follow the owning publication protocol.

## Cancellation and teardown

Requests become terminal exactly once under `mu`: grant, deadline/cancel, ticket
end or replacement fence. A terminal cannot consume a later request generation's
grant. Teardown releases actual owned contributions and wakes waiters through the
same queues. Cancellation and fatal diagnostics retain their designated owner;
SQLSTATE text is not evidence of a manager event.

## Diagnostics

Inspection is a deliberate owned-value seam for tests/tooling. Snapshots borrow
no mutable manager maps or request state and never acquire engine authority.
Blocking/wait projection consumes actual registrations and queues, not inferred
SQL text or process state.

## What stays in the engine

Catalog-qualified relation/type/object admission, statement scopes, transaction
completion, visibility and `pg_locks` projection. The manager arbitrates resources;
it does not validate catalog authority or execute SQL.

## Testing

Pure fixtures mint tickets/resources from an `ids.Domain` without an engine.
Tests observe the exact queued request and use deterministic deadline/virtual-time
controls rather than sleeps. Join every worker and assert on the test goroutine.
Core tests cover real scope/admission/publication integration; public SQL remains
in reference locking/session packages.
