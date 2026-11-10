# reference/triggers

Dual-mode scenarios for executable-trigger catalogs and event-trigger declarations,
catalogs and command callbacks. Ordinary DML-trigger execution belongs to
[operators](../operators/README.md#testing). Shared catalog rules live in
[catalogs](../catalogs/README.md#testing).

## Fixture ownership

All scenarios use `reference/fixture` and public APIs.
`eventTriggerScenario` owns the configured backend loop and registers database-global
cleanup before declarations. Each backend/replay owner retires triggers under a
fresh bounded context before schema teardown, including assertion failures.
Callbacks must not survive into fixture reset.

Audit helpers use fixed phase values, not transactional assumptions about sequences.
Lock observers establish real pre-publication peer admission without sleeps.
Register every old/new global name for cleanup before creating it.

Dropped-record helpers independently specify logical bags. Capture generated
addresses and RI names from public catalogs before deletion; expected kinds,
provenance and multiplicity must not be learned from the helper under test.
Saved historical IDs are audit data, not expected current allocator values.
Native controls and emitted-SQL replay follow the shared fixture ownership rules.
Inspect raw structural carriers independently from semantic deparse and execution.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` delegates to `fixture.Main`.
Catalog helpers remain with `pg_trigger_*_test.go`; shared catalog helpers remain
in `reference/fixture`.

Use `make check RUN='...'`, adding `REFERENCE=1` for the oracle.

- Executable catalog: `RUN='^TestReferencePGTrigger'`
- Event triggers: `RUN='^TestReference(EventTrigger|PGEventTriggerCatalog|ExtensionOwnershipCommandInspection)'`
- DML-trigger lifecycle: `RUN='^TestReferenceTriggerDDL'`
