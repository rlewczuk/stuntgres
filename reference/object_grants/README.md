# reference/object_grants

GRANT/REVOKE on non-table objects: sequences (including sequence column grants),
schemas, domains/enums/composites and builtin types, routines (EXECUTE on
functions, procedures, aggregates and operator procedures, schema-wide
expansion, SQL bodies, stored/index/partition-key demand), databases (CONNECT,
CREATE, TEMP), parameters (SET, ALTER SYSTEM) and tablespaces; non-table REVOKE
with RESTRICT/CASCADE and warnings; large objects (byte functions,
`pg_largeobject_metadata`, privileges, ownership, transactions, Restore); and one
provider-authority witness per builtin or extension provider (current-invoker
EXECUTE through direct, held and stored consumers). Related: table/column
grants, default privileges, membership and ownership in `reference/roles`;
FDW wrapper/server grants (`TestReferenceFDWObjectGrants`) in `reference/tables`;
the `pg_largeobject` page catalog in `reference/catalog_objects` and ACL inquiry functions in
`reference/catalog_privileges`.

Strict scalar demand covers post-child literal and fresh bound NULL elimination,
including volatile `array_sample` with a discarded `nextval` sibling. Planning
preserves immutable child failures/current EXECUTE and imported input errors
before erasure. Runtime row NULL retains real sibling demand and effects;
nonNULL volatile calls still execute. RECORD results, SRFs and compound
NULL-aware operators are not scalar-NULL shortcuts. Original stored/default
graphs and lifetime manifests remain intact, including dependencies erased only
from the execution-private plan; this does not allow general volatile defaults.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.
Role-grant actors, permission diagnostics and notice helpers are the fixture's
`RoleGrant*` helpers ([fixture README](../fixture/README.md#helpers)).

- Shared parameter authority: `RUN='^TestReferenceParameterACLShared'`.
  `parameterSharedScenario` registers role/database teardown before selecting
  peers with `fixture.DatabaseScenarioPool`; held statements keep their physical
  borrower across peer grants. Contention uses the fixture's exact transaction
  lock observer, never worker-start or elapsed-time inference.

- `objectGrantScenario(t, schemas, body)` (`object_grant_schemas_types_test.go`)
  runs `direct`/`prepared` × backend subtests, registers role and schema cleanup
  before the scenario, and creates the roles `og_object_reader` and
  `og_object_other`. `largeObjectScenario` (`large_object_helpers_test.go`) is the
  same shape for large objects. Steps are not subtests: step runners call
  `objectGrantStep`, which runs the step on the backend subtest, stops at its
  first failure and logs the failing SQL.
- `objectGrantProviderLifecycle` (`object_grant_aggregate_provider_authority_test.go`)
  is the standard deny → grant → revoke cycle over `objectGrantProviderCase`
  consumers; `objectGrantBodyConsumer` (`object_grant_routine_body_consumers_test.go`)
  is the SQL-body variant. A held statement is prepared once and runs unchanged
  after the same fresh step, which does not depend on the route, so these helpers
  and `accessExecutionPairedHeld` hold statements in the `prepared` route only.
  Held witnesses that are the only check of a route-run statement (live authority,
  smoke, persistence and NULL-diagnostic tails) keep both routes.
- Rule/RI/COPY consumers keep distinct principals: rule-added relation access
  uses the event owner while routine calls use the invoker; actual SET DEFAULT
  RI uses the child owner; COPY uses the importing session. NEW substitutions
  demand omitted callable defaults. COPY borrows an unwrapped native connection
  or the fixture's pgx stream connection, never the recording pq wrapper; gated
  readers have an independent observer and are released and joined on every exit.
- `ObjectGrantControls` retains canonical parameter ACL roundtrips and
  zero-output rejection for unrepresentable authority. Ordinary builtin and conversion routines, as well
  as authentic installed members, prove exact raw ACLs, owner identity and
  deny/grant/revoke behavior through nonempty, canonical native
  Dump/Reset/Restore rounds inside the shared SQL scenario.
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
  Ordinary CHECK wait scenarios independently expect PostgreSQL's already-warmed
  execution to finish after a queued unique conflict clears, while native
  readmission requires current EXECUTE before resumption/publication. Both modes
  separately assert fresh revoked denial, unchanged committed rows and no
  volatile truth replay; ordinary held CHECK executions reinitialize admission.
- Large-object isolation witnesses use two physical connections; SQL-body
  snapshot witnesses guard against SQL-function inlining with a one-row source.
  Descriptor scenarios retain physical session ownership and recover expected
  transaction errors with savepoints. They distinguish frozen READ openings,
  current WRITE visibility, stable-isolation bases, SQL-body query frontiers,
  ACL-at-open and ordinal savepoint lifetime without rewinding older cursors.
  Literal-NULL planning witnesses assert erased sequence effects separately
  from runtime row-NULL demand and arithmetic errors. Guarded STABLE/VOLATILE
  bodies distinguish inherited/fresh own-write frontiers in RC and RR, retained
  RR committed bases, and exact outer restoration after success/failure.
  `descriptorOpen` checks INT4 metadata and captures each handle independently;
  byte reads use the shared raw BYTEA contract. Held preparation and native
  control phases retain actual statements and detached client results across
  transaction/catalog boundaries; dumps contain committed bytes, not handles.
- Transactional privilege families cover autocommit, SQL BEGIN and Go BeginTx
  with direct/held utilities and real consumers. Language/tablespace raw ACL
  metadata retains represented `aclexplode` witnesses; selected language,
  routine, wrapper and server inquiries additionally use the typed access path.
  Language declaration and post-admission consumers remain independent witnesses.
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
- Strict-NULL demand scenarios assert exact typed rows, full diagnostics/stages,
  complete sequence state and session currval/lastval, not only absence of a
  permission error. They cover direct, fresh-prepared and actual same-held
  handles, nil/nonNULL/nil frames, imports/domain boundaries, literal views and
  defaults, runtime-row views, current invoker authority and denied-write
  atomicity. PostgreSQL held-cache observations remain distinct from native
  current-authority requirements; an erased literal call has no invocation in
  either backend.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Sequences, sequence columns and targets: `RUN='^TestReferenceObjectGrantSequence'`
- Schemas and types: `RUN='^TestReferenceObjectGrantSchemasTypes$'`
- Type targets, raw type ACLs and Dump controls: `RUN='^TestReferenceObjectGrantType'`
- Routine EXECUTE (formals, operators, expansion, demand, SQL bodies): `RUN='^TestReferenceObjectGrantRoutine'`
- Strict scalar NULL demand, precedence, imports and actual held/stored consumers:
  `RUN='^TestReferenceObjectGrantStrictNull(Demand|Precedence|Imports|Held|Stored)$'`.
  Run this whole-definition selection both natively and with `REFERENCE=1`;
  the configured oracle is PostgreSQL 18.6, UTF8/C.
- Rule, RI and COPY authority: `RUN='^TestReferenceObjectGrant(Rule|RI|Copy)'`
- SQL routine bodies: `RUN='^TestReferenceObjectGrantRoutineBody'`
- JSON and JSONB providers: `RUN='^TestReferenceObjectGrantJSON'`
- pgcrypto providers: `RUN='^TestReferenceObjectGrantPGCrypto'`
- Access-privilege inquiry functions: `RUN='^TestReferenceObjectGrantAccessInquiry'`
- Routine/language/foreign execution inquiries: `RUN='^TestReferenceAccessExecution'`.
  This family uses the shared scenario, diagnostic, metadata and held-statement
  helpers. Its persistence phase replays the actual emitted native stream on
  PostgreSQL and provisions role names independently of allocated OIDs.
  Persistent procedure authority is checked through its actual kind, owner,
  raw ACL and name/OID inquiries; CALL effects use query_outer's shared scenarios.
  Language/wrapper/server checks preserve delegated grantors/options, changed
  server ownership and NULL versus empty ACLs. Temporary-routine cases verify
  their declarations and ACL repairs are omitted together while persistent
  authority survives fresh native and actual-stream PostgreSQL replay.
  Before actual-stream replay, capture the three retained stock `plpgsql`
  functions' original owner and ACL. Bounded independent-admin cleanup restores
  ownership and revokes scenario grants through ordinary DDL before repairing
  the exact raw ACL, so extension members retain no temporary-role dependencies.
  Persistent routine OIDs come from public regprocedure identities; temporary
  OIDs come directly from the public catalog because existing text routine
  selection excludes temporary namespaces, even with qualification. The
  temporary witness applies schema-wide grants before creating its absent-ACL
  sibling, without expanding that resolver boundary. Raw routine ACLs use
  `pg_get_acl`, without requiring optional `pg_proc` columns. Fresh-driver
  SQL witnesses use `RecordedNativePool` with its own recorder; controls retain
  the unwrapped pool for the same connector. Reset each borrowed connection
  before returning it; global cleanup
  drops owned foreign objects and revokes named language grants before restoring
  the original raw language ACL. A catalog UPDATE alone cannot remove
  PostgreSQL's shared role dependencies created by GRANT.
- Relation/column/any-column/sequence inquiries: `RUN='^TestReferenceAccessRelations'`.
  Lifecycle coverage retains relation/column rename and drop, role replacement,
  membership, SET ROLE and held-statement freshness; it does not require role
  rename. Attached-attribute persistence covers typed tables and RANGE partitions
  whose ordinary source drops columns 2 and 4 before OF/ATTACH. Both preserve
  physical attributes 1 and 3, column grants, rows, fresh-driver Restore and
  canonical redump, with actual emitted SQL replayed on PostgreSQL. Partition
  hierarchy column mutation and retirement/reattachment compositions are outside
  this coverage; ordinary mixed and detached inheritance histories remain covered.
- Large-object/parameter inquiries: `RUN='^TestReferenceAccessSpecial'`.
  Shared direct/prepared and held witnesses include active/current snapshots,
  current role authority and native combined ACL/byte persistence.
- Parameters and tablespaces:
  `RUN='^TestReferenceObjectGrantParametersTablespaces$'` in both modes.
  Canonical work_mem and its case-folded spelling now share real parameter ACL
  delegation/revocation in both backends. Only PostgreSQL's legacy sort_mem
  alias remains supplemental oracle evidence; native intentionally rejects
  that alias without rejecting the canonical resource setting.
- Partition-key recipes: `RUN='^TestReferenceObjectGrantPartitionKey'`
- Domain CHECK recipes: `RUN='^TestReferenceObjectGrantDomainCheck'`
- Ordinary table CHECK admission and readmission: `RUN='^TestReferenceObjectGrantCheck'`
- SQL cursors: `RUN='^TestReferenceObjectGrantSQLCursor'`
- Non-table REVOKE: `RUN='^TestReferenceObjectRevoke'`
- Large-object metadata: `RUN='^TestReferenceLargeObjectMetadata'`
- Large-object ownership: `RUN='^TestReferenceLargeObjectOwner'`.
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
