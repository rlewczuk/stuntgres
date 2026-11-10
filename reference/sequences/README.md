# reference/sequences

Dual-mode scenarios for sequences, defaults, serial/identity/generated columns
and temporary tables. Virtual generated-column COPY consumers live in
[dml](../dml/README.md#testing) to reuse streaming transports; hierarchy row-lock
rechecks belong to [locking_transactions](../locking_transactions/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.

- Helpers: `addGeneratedRoutes` (`alter_add_column_generated_helpers_test.go`),
  `generatedStoredScenario` (`generated_stored_test.go`),
  `newSequencePreparedSession` (`sequence_prepared_helpers_test.go`),
  `sequenceRestartValue`/`sequenceRestartState` (`sequence_restart_helpers_test.go`),
  `serialIdentityScenario` (`serial_identity_inquiry_test.go`),
  `identityReferenceCase` (`identity_test.go`) and `defaultConsumerReference*`
  (`default_consumer_test.go`). TEMP sequence and temporary-table scenarios use
  `fixture.TempReferenceSession`.
- Physical sequence measurements use independent backend expectations, not parity.
- Lock-queue witnesses use the captured relation OID and actual blocker PID, not
  hard-coded names or timing.
- pq returns raw REGCLASS and VOID cells as bytes (native: strings); a VOID result
  is non-NULL and empty.
- Never name a file `*_windows_test.go`: Go builds it only on Windows.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Sequences (DDL, functions, REGCLASS, ownership, prepared, TEMP namespaces): `RUN='^TestReference(Smoke)?Sequence'`
- Serial columns and serial/identity inquiry: `RUN='^TestReference(Smoke)?Serial'`
- Identity columns: `RUN='^TestReference(Smoke)?Identity'`
- Column defaults: `RUN='^TestReference(Smoke)?(Insert)?Default'`
- STORED generated columns: `RUN='^TestReferenceGeneratedStored'`
- VIRTUAL generated columns (also runs the dml COPY tests): `RUN='^TestReferenceGeneratedVirtual'`
- Added generated columns: `RUN='^TestReferenceAlterAddColumnGenerated'`
- `SET/DROP EXPRESSION`: `RUN='^TestReferenceAlterGeneratedExpression'`
- Temporary tables: `RUN='^TestReferenceTemporaryTable'`

Package units for generated-column ALTER: `make check RUN='^TestAlterGeneratedExpression'`;
generated-column binder/rewrite units: `make check RUN='^TestGenerated'`.
