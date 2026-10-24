# reference/object_grants

GRANT/REVOKE on non-table objects: sequences (including sequence column grants),
schemas, domains/enums/composites and builtin types, routines (EXECUTE on
functions, procedures, aggregates and operator procedures, schema-wide
expansion, SQL bodies, stored/index/partition-key demand), databases (CONNECT,
CREATE, TEMP), parameters (SET, ALTER SYSTEM) and tablespaces; non-table REVOKE
with RESTRICT/CASCADE and warnings; large objects (byte functions,
`pg_largeobject_metadata`, privileges, transactions, Restore); and one
provider-authority witness per builtin or extension provider (current-invoker
EXECUTE through direct, held and stored consumers). Related: table/column
grants, default privileges, membership and ownership in `reference/roles`;
FDW wrapper/server grants (`TestReferenceFDWObjectGrants`) in `reference/tables`;
the `pg_largeobject` page catalog and ACL inquiry functions in `reference/catalogs`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.
Role-grant actors, permission diagnostics and notice helpers are the fixture's
`RoleGrant*` helpers ([fixture README](../fixture/README.md#helpers)).

- `objectGrantScenario(t, schemas, body)` (`object_grant_schemas_types_test.go`)
  runs `direct`/`prepared` × backend subtests, registers role and schema cleanup
  before the scenario, and creates the roles `og_object_reader` and
  `og_object_other`. `largeObjectScenario` (`large_object_helpers_test.go`) is the
  same shape for large objects.
- `objectGrantProviderLifecycle` (`object_grant_aggregate_provider_authority_test.go`)
  is the standard deny → grant → revoke cycle over `objectGrantProviderCase`
  consumers; `objectGrantBodyConsumer` (`object_grant_routine_body_consumers_test.go`)
  is the SQL-body variant.
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
- PostgreSQL held statements can keep plan-time results across GRANT/REVOKE
  (an inlined SQL-function body, a folded immutable constant); native held
  execution checks current authority on every execution. Specify each backend's
  held outcome independently and use fresh executions for immutable
  all-constant calls.
  Ordinary CHECK wait scenarios independently expect PostgreSQL's already-warmed
  execution to finish after a queued unique conflict clears, while native
  readmission requires current EXECUTE before resumption/publication. Both modes
  separately assert fresh revoked denial, unchanged committed rows and no
  volatile truth replay; ordinary held CHECK executions reinitialize admission.
- Large-object isolation witnesses use two physical connections; SQL-body
  snapshot witnesses guard against SQL-function inlining with a one-row source.
- Transactional privilege families cover autocommit, SQL BEGIN and Go BeginTx
  with direct/held utilities and real consumers. Language/tablespace ACL
  metadata uses represented `aclexplode` rows rather than adding unsupported
  inquiry functions; language declaration and post-admission consumers remain
  independent behavioral witnesses.
  Named aggregate/window and ALL FUNCTIONS/ROUTINES/PROCEDURES cases keep the
  same held consumers across private ACL changes and transaction recovery/end.
  Schema-CREATE delegation cases inspect CREATE-filtered origin/option rows,
  failed RESTRICT, private CASCADE and independent surviving grants. Actual
  fresh and same held owner/peer CREATE probes INSERT7/SELECT7/DROP and leave
  no target after denial or cleanup; USAGE and table SELECT remain independently
  unchanged prerequisites.
- Relation ACL wait witnesses take a real AX holder before starting the named
  grant/revoke. Sequence holders rename away and back: INCREMENT alone does not
  establish the required AccessShare conflict. Replacement cases compare
  distinct OIDs and actual old7/new9 recipient effects. Concurrent default
  writers distinguish absent-key uniqueness from existing-row update failure;
  independent keys preserve both ACLs and unrelated committed rows.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Sequences, sequence columns and targets: `RUN='^TestReferenceObjectGrantSequence'`
- Schemas and types: `RUN='^TestReferenceObjectGrantSchemasTypes$'`
- Type targets, raw type ACLs and Dump controls: `RUN='^TestReferenceObjectGrantType'`
- Routine EXECUTE (formals, operators, expansion, demand, SQL bodies): `RUN='^TestReferenceObjectGrantRoutine'`
- SQL routine bodies: `RUN='^TestReferenceObjectGrantRoutineBody'`
- JSON and JSONB providers: `RUN='^TestReferenceObjectGrantJSON'`
- pgcrypto providers: `RUN='^TestReferenceObjectGrantPGCrypto'`
- Access-privilege inquiry functions: `RUN='^TestReferenceObjectGrantAccessInquiry'`
- Partition-key recipes: `RUN='^TestReferenceObjectGrantPartitionKey'`
- Domain CHECK recipes: `RUN='^TestReferenceObjectGrantDomainCheck'`
- Ordinary table CHECK admission and readmission: `RUN='^TestReferenceObjectGrantCheck'`
- SQL cursors: `RUN='^TestReferenceObjectGrantSQLCursor'`
- Non-table REVOKE: `RUN='^TestReferenceObjectRevoke'`
- Large-object metadata: `RUN='^TestReferenceLargeObjectMetadata'`
- Large objects: `RUN='^TestReferenceLargeObject'`
- Transactional families, admission and independent writer outcomes:
  `RUN='^TestReferenceTransactionalPrivileges(Families|RoutineKinds|NonTableDelegation|Locks|Conflicts)$'`
  Run selected native and `REFERENCE=1` modes; add `RACE=1` to selected
  Locks/Conflicts for native race proof. Protocol coverage lives in
  `reference/sessions`, creator defaults and controls in `reference/roles`.
- Preserved routine-kind and non-table dependency controls:
  `RUN='^(TestReferenceObjectGrantRoutineSchemaKinds|TestReferenceObjectRevokeDependencies)$'`.

- Databases, parameters and tablespaces:
  `RUN='^TestReferenceObjectGrant(DatabaseAdmission|ParametersTablespaces)$'`
- Syntax, lifecycle and native controls:
  `RUN='^TestReferenceObjectGrant(SyntaxDiagnostics|NativeBoundaries|AuthorityLifecycle|Controls|FeatureSmoke)$'`
- Column eligibility, stored-recipe replacement and SQL PREPARE/EXECUTE actuals:
  `RUN='^TestReferenceObjectGrant(ColumnEligibility|StoredRecipeReplacement|SQLPreparedActual)'`
- Every other witness covers one provider or recipe kind and is named
  `TestReferenceObjectGrant<Provider>Authority`; select it alone, for example
  `RUN='^TestReferenceObjectGrantTIDAuthority$'`.
- Engine units: `make check RUN='^Test(ObjectGrant|LargeObject)'`.
