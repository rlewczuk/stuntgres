# reference/object_grants

Dual-mode scenarios for non-table authorization, routine/provider authority and
large-object APIs. Table/column grants and defaults belong to
[roles](../roles/README.md#testing); FDW wrapper/server grants to
[tables](../tables/README.md#testing); descriptor positioning/truncation to
[large_objects](../large_objects/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.
Role-grant actors, permission diagnostics and notice helpers are the fixture's
`RoleGrant*` helpers ([fixture README](../fixture/README.md#helpers)).

- `objectGrantScenario(t, schemas, body)` (`object_grant_schemas_types_test.go`)
  runs `direct`/`prepared` × backend subtests, registers role and schema cleanup
  before the scenario. `largeObjectScenario` (`large_object_helpers_test.go`) is the
  same shape for large objects. Steps are not subtests: step runners call
  `objectGrantStep`, which runs the step on the backend subtest, stops at its
  first failure and logs the failing SQL.
- `objectGrantProviderLifecycle` (`object_grant_aggregate_provider_authority_test.go`)
  is the standard deny → grant → revoke cycle over `objectGrantProviderCase`
  consumers; `objectGrantBodyConsumer` (`object_grant_routine_body_consumers_test.go`)
  is the SQL-body variant. A held statement is prepared once and runs unchanged
  after the same fresh step, which does not depend on the route, so these helpers
  and `accessExecutionPairedHeld` hold statements in the `prepared` route only.
- COPY borrows an unwrapped native connection or the fixture's pgx stream,
  never the recording pq wrapper. Gated readers have an independent observer
  and are released and joined on every exit.
- Builtin routine ACLs, large objects and database, parameter and tablespace
  ACLs survive the `public` reset. Defer `objectGrantRoutineCleanup`
  (`object_grant_routines_scalar_test.go`) for every builtin routine whose
  EXECUTE a test changes, unlink created large objects with `largeObjectCleanup`,
  and restore database ACLs in `t.Cleanup` under a bounded context
  (`objectGrantDatabaseCleanup` in `object_grant_database_admission_test.go`).
- CONNECT witnesses open fresh authenticated login pools and close them on
  every path, including denial.
- ACL text names the bootstrap role: `postgres` natively, `stuntgres` on the
  oracle. Specify each independently. lib/pq returns OID cells as strings;
  `largeObjectBackendStep` converts expected int64 OIDs for PostgreSQL.
- `largeObjectOwnerScenario` adds dedicated role cleanup and exact notice checks
  for ownership scenarios; held utilities reuse `fixture.HeldStatement` while
  raw metadata and physical sparse pages remain independent expectations.
- PostgreSQL held statements can keep plan-time results across GRANT/REVOKE
  (an inlined SQL-function body, a folded immutable constant); native held
  execution checks current authority on every execution. Specify each backend's
  held outcome independently and use fresh executions for immutable
  all-constant calls.
- Capture server handles independently; keep detached client values distinct
  from live server resources during transaction/control tests.
- Transactional helpers retain actual direct/held consumers across SQL BEGIN and
  Go BeginTx. Conflicts require observed lock admission and joined workers.
- Restore actual emitted authority into destination-owned recorded SQL pools;
  controls use the corresponding unwrapped pools. Provision named roles before replay.
  Remove grant dependencies before restoring raw PostgreSQL ACLs: catalog UPDATE
  alone does not remove GRANT-created shared dependencies.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Non-table grants: `RUN='^TestReferenceObjectGrant'`
- Revocation: `RUN='^TestReferenceObjectRevoke'`
- Access inquiries: `RUN='^TestReferenceAccess'`
- Large objects: `RUN='^TestReferenceLargeObject'`
- Transactional privileges: `RUN='^TestReferenceTransactionalPrivileges'`
