# internal/engine/control

Native Dump/Restore drivers and catalog-replacement phases. Dump turns a coherent
capture into an ordered SQL script; Restore replays through an ordered hook table.
[`dump`](../dump/README.md) owns the event graph, feature packages own serializers,
and [docs/FIXTURES.md](../../../docs/FIXTURES.md) defines the public contract.

## Imports and state

Only the root engine imports this composition-facing package. It holds no mutex,
engine pointer or state between calls. Runtime ownership stays in core.

## Dump

The root's `Engine.Mu` capture window checks portability and typed ACL values,
fills `Source`, and calls `Collect`. Nothing in Source is read after unlock.
`Dump(ctx, capture, dst)` completes/sorts the owned capture, builds the script and
writes it; callers do not reuse that capture afterwards.

Collectors and emitters are ordered tables. The first matching collector owns a
descriptor; unmatched or unrepresentable state fails before output. Render all
fallible graph, provider, privilege and typed-value work before writer I/O.
Output ordering and canonical SQL bytes are compatibility contracts.

### Ordering and replay rules

Dependencies order declarations, memberships, data and completion events rather
than relying on allocator order. Logical identities and genuine installed-provider
requirements travel in scripts; source handles, callbacks, physical resources and
transaction/session state do not.

Native metadata complements ordinary SQL only for state SQL cannot represent
exactly. Decoders validate complete versioned records and resolve actual
destination identities. Comments, names or equal values alone grant no repair
authority. Historical privilege state is distinct from current ACL state.

## Restore

`Restore[R](ctx, host, hooks, run, src)` reads and decodes input before starting
an isolated private stage. The ordered `Hook[R]` table supplies optional phase
callbacks; entries share state only through the run and execute on the stage,
never the destination. Admission accepts only the generated bounded shapes,
not arbitrary utility SQL under elevated authority.

### Host contract

`Host` owns stage/session startup, per-statement preparation and execution,
finishing, disposal and final replacement. Root `restoreHost` implements it.
Private replay authority is exact to the authenticated import, target and
statement/lifecycle; ordinary ownership, dependency, provider and cancellation
checks remain independent. Failed execution clears transient authority.

Final resolution/repair completes before one checked destination publication.
Input, decoding, replay or late-validation failure leaves the destination intact;
cleanup releases the private session and all stage resources.

## Catalog replacement

Core plans identity/principal remapping and destination TEMP retention under the
replacement/engine lock order. Fallible provider repair runs on an isolated
candidate outside the target mutex, followed by exact predecessor revalidation.
Imported identities are reminted; destination-live TEMP identities/resources and
cluster authority follow their owning retention policies, never source handles.
All affected roots are prepared before the no-failure installation window.

## What stays in the engine

Capture windows, allocator installation, storage and TEMP ownership, the restore
run/stage, lock order and atomic replacement. This package supplies drivers,
ordered phases and pure replacement helpers, not a second publication authority.

## Testing

Package units exercise the driver and hook ordering with a fake Host. Core and
root composition tests cover real capture, emit, remap, TEMP retention and
failure preservation. Public controls live in reference packages; see
[docs/TESTING.md](../../../docs/TESTING.md).
