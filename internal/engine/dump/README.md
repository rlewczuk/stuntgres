# internal/engine/dump

Native Dump records and deterministic event ordering. The engine captures a graph
of declaration, membership, data and completion events; this package sorts it
without owning runtime state or publication.

## What it owns

- `Event{OID, Phase, Step}` and `Graph` represent events and predecessor edges.
  Features add dependencies to the common graph rather than inventing emission
  order from names, map iteration or numeric allocation.
- `Order` topologically sorts the captured graph with deterministic phase/order
  rules and rejects invalid dependencies/cycles.
- Dump records carry SQL and owner identities. Resolver/presenter capabilities
  supply checked names, literals and dependencies without an engine pointer.
- `RestoreScalar` validates only bounded generated scalar forms. Ordinary input
  is not a grant to execute arbitrary SQL or synthesize replay authority.

## Where Dump runs

[`control`](../control/README.md) owns the script driver, ordered sections and
Emit table. Feature packages own serializers using resolver/presenter values.
The root owns the coherent `Engine.Mu` capture window. Emission runs on that
owned capture after unlock, with fallible preflight completed before writer I/O.

## Testing

Pure graph ordering and scalar-admission units live here. Core/root tests cover
real capture and serializer composition; public round trips belong to reference
packages. Canonical bytes and deterministic ordering are compatibility contracts.
