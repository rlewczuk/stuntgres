# reference/sequences

Sequences and value-producing columns: CREATE/ALTER/DROP SEQUENCE, `nextval`/
`currval`/`setval` and their REGCLASS inputs, sequence ownership, prepared
sequence statements, TEMP sequence namespaces; serial and identity columns and
serial/identity inquiry; column DEFAULT expressions; STORED and VIRTUAL generated
columns and `ALTER COLUMN ... SET/DROP EXPRESSION`; temporary tables. Related
families: `random()` in `reference/numeric`; COPY into virtual generated columns
in `reference/dml`; inheritance row-lock rechecks of generated columns in
`reference/locking_transactions`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.

- Helpers: `generatedStoredScenario` (`generated_stored_test.go`),
  `newSequencePreparedSession` (`sequence_prepared_helpers_test.go`),
  `sequenceRestartValue`/`sequenceRestartState` (`sequence_restart_helpers_test.go`),
  `serialIdentityScenario` (`serial_identity_inquiry_test.go`),
  `identityReferenceCase` (`identity_test.go`) and `defaultConsumerReference*`
  (`default_consumer_test.go`). TEMP sequence and temporary-table scenarios use
  `fixture.TempReferenceSession`.
- Sequence physical fields are backend-specific: native `log_cnt` is 0, while
  PostgreSQL reports WAL/cache-dependent values. Assert each independently.
- Lock-queue witnesses use the captured relation OID and actual blocker PID, not
  hard-coded names or timing.
- pq returns raw REGCLASS and VOID cells as bytes (native: strings); a VOID result
  is non-NULL and empty.
- Never name a file `*_windows_test.go`: Go builds it only on Windows (hence
  `generated_virtual_window_identity_test.go`).

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Sequences (DDL, functions, REGCLASS, ownership, prepared, TEMP namespaces): `RUN='^TestReference(Smoke)?Sequence'`
- Serial columns and serial/identity inquiry: `RUN='^TestReference(Smoke)?Serial'`
- Identity columns: `RUN='^TestReference(Smoke)?Identity'`
- Column defaults: `RUN='^TestReference(Smoke)?(Insert)?Default'`
- STORED generated columns: `RUN='^TestReferenceGeneratedStored'`
- VIRTUAL generated columns (also runs the dml COPY tests): `RUN='^TestReferenceGeneratedVirtual'`
- `SET/DROP EXPRESSION`: `RUN='^TestReferenceAlterGeneratedExpression'`
- Temporary tables: `RUN='^TestReferenceTemporaryTable'`

Package units for generated-column ALTER: `make check RUN='^TestAlterGeneratedExpression'`;
generated-column binder/rewrite units: `make check RUN='^TestGenerated'`.
