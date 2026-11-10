# reference/large_objects

Dual-mode scenarios for large-object descriptor positioning and truncation.
Other byte, descriptor and privilege families belong to
[object_grants](../object_grants/README.md#testing).

## Ownership and orchestration

The package uses exported [fixture](../fixture/README.md) primitives.
Its local `truncateSession` shares direct/fresh-prepared orchestration, held
statements, independent descriptor capture and bounded byte/page assertions.
Capture descriptors through `RunStep` after checking INT4 metadata; project
object OIDs to BIGINT rather than adding a second codec adapter.

Cursor-sensitive calls are separate statements: projection order is not a
positioning guarantee. Sparse checks use bounded reads, not whole-object reads.

Statements, rows and peers close before returning connections or cleaning roles.
Cleanup rolls back and resets session authority before unlinking owned identities.
Actual held witnesses use `HeldStatement`, not another fresh Prepare. Detached
client results and their metadata remain observable after transaction/control
boundaries. No test retains private engine authority or imports internal packages.

Native Dump/Reset/Restore phases use the raw control pool. A separate Driver's
Restore destination has its own `RecordedNativePool` and recorder, used by every
SQL assertion and cleanup callback. The shared SQL baseline also runs against
PostgreSQL; only native controls and ordinary pre-admission context probes are
backend-specific. PostgreSQL cancellation uses `SessionCancelQuery` for actual
admission and a bounded cancel/join on the same physical owner.

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
