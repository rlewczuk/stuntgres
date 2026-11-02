# reference/large_objects

Public dual-mode coverage for descriptor seek/tell and truncation: 32/64-bit
positions, mode-selected EOF, zero-filled sparse gaps and physical endpoints,
exact diagnostics and strict demand, transaction visibility and ACL-at-open,
prepared-definition lifetimes, native controls and cancellation. Existing
large-object byte, descriptor and privilege families remain in
[reference/object_grants](../object_grants/README.md#testing).

## Ownership and orchestration

The package uses unchanged exported [fixture](../fixture/README.md) primitives.
Its local `truncateSession` only orchestrates direct/fresh-prepared steps on
borrowed physical connections, independently captured descriptors, bounded byte
and ordered page assertions, savepoint recovery and held statements. Descriptor
capture uses `RunStep` observations after asserting INT4 metadata and the expected
lowest-free slot. Object setup projects OIDs to BIGINT, avoiding a second general
backend codec adapter. Roles and cluster cleanup are registered only for cases
that need them; every case owns its fixed large-object identities.
Seek/tell reuses this orchestration and its held-statement/descriptor adapters;
cursor-sensitive calls are separate statements, with independent positions and
bounded sparse reads rather than assumed projection order or whole-object reads.

Statements, rows and peers close before returning connections or cleaning roles.
Cleanup rolls back and resets session authority before unlinking owned identities.
Actual held witnesses use `HeldStatement`, not another fresh Prepare. Detached
client results and their metadata remain observable after transaction/control
boundaries. No test retains private engine authority or imports internal packages.

Native Dump/Reset/Restore phases use the raw control pool. A separate Driver's
Restore destination has its own `RecordedNativePool` and recorder, used by every
SQL assertion and cleanup callback. The shared SQL baseline also runs against
PostgreSQL; only native controls and ordinary pre-admission context probes are
backend-specific. PostgreSQL cancellation uses `SessionCancelQuery` to observe
the supplied demanded truncate-plus-sleep statement, cancel and join it, and
recover on the same physical owner. There are no timing sleeps or detached
workers.

## Testing

Use each family selector in both modes:

```sh
make check RUN='^TestReferenceLargeObjectTruncate'
make check RUN='^TestReferenceLargeObjectTruncate' REFERENCE=1
make check RUN='^TestReferenceLargeObjectSeek'
make check RUN='^TestReferenceLargeObjectSeek' REFERENCE=1
```

Follow the shared placement, oracle, lifecycle and diagnostic rules in
[docs/TESTING.md](../../docs/TESTING.md). Expectations specify release-derived
values and metadata independently; backend parity is not an oracle.
