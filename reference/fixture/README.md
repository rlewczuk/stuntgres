# reference/fixture

Shared, importable fixture for the dual-mode reference packages: oracle container
lifecycle, scenario pairs, step runner, diagnostics, notice/COPY connections,
lock and cancellation observers, and helpers shared by several packages. The
common policy and basic helpers are in [docs/TESTING.md](../../docs/TESTING.md);
this file covers the rest. The fixture is exempt from the 750-line file limit,
not from the package byte cap.

## PostgreSQL oracle policy and provenance

The oracle is PostgreSQL 18.6, pinned by OCI index digest in
`fixture.go` (`referenceImage`):

```text
postgres:18.6-bookworm@sha256:afc7e2d441324c0388fa80c3d24f733b4194a4eb7f47dd8ee2b08eb1a24a647c
```

The digest, not the tag, selects the image; the verified linux/amd64 child is
`sha256:9551dd5bf356409a21b31248f49b33bba9bb1877365f3a7e933aecd9af3cf52d`.

- Startup queries the real server and requires release 18.6 (vendor suffix
  allowed), `server_version_num` 180006, UTF8, `datcollate=C`, `datctype=C`,
  `lc_messages=C`, `max_prepared_transactions=100` and the requested
  commit-timestamp mode. It logs one stderr provenance record and never
  synthesizes a version. Startup, image, network and configuration failures are
  errors, never skips.
- The fixture provisions `postgres` as a NOLOGIN superuser (the native bootstrap
  owner) for the container lifetime; PostgreSQL callers authenticate as
  `stuntgres`. Dump replay keeps owner directives, so owners are not remapped.
- Known upstream oracle defects and how tests record them are in
  [docs/POSTGRES_REGRESSION.md](../../docs/POSTGRES_REGRESSION.md). Native version
  reporting is independent of the oracle release.

## Lifecycle and reset

- Each package process owns one serialized container and one admin pool, torn down
  before exit; packages never share a container. Bounds: startup 3 min,
  scenario 60 s, reset 15 s, termination 45 s.
- Every pair owns a dedicated native `Driver`; its wrapped and control pools share
  the fixture DSN, so reopening a pool keeps the engine.
  `RecordedNativePool(t, connector)` applies that same recording adapter to a
  fresh native connector, such as a separate Driver's Restore destination.
  `RawBackend` selects codec assertions; it does not install recording middleware.
  `AssertRawValue` checks non-NULL internal-char arrays (`_CHAR`) as native
  strings and pq byte slices before normalization, like other generic arrays;
  scalar `CHAR` remains a string on both backends.
- Before and after each case, the reset borrows the admin pool's one idle session
  and runs standalone `ROLLBACK`, then standalone `DISCARD ALL` (it cannot run in a
  transaction block), then `DROP SCHEMA public CASCADE; CREATE SCHEMA public`, on
  the same connection. All borrowers, Rows and workers must be returned, closed or
  joined first.
- Public-schema owners differ by design: native `public` is owned by 6171
  (`pg_database_owner`), the oracle's rebuilt `public` by its bootstrap role 10.
  Never invent a common owner.
- The oracle's rebuilt `public` has no PUBLIC USAGE grant; tests that need
  non-owner access to it grant USAGE explicitly (or use `WithPublicSchemaOwner`).
- Prepared-transaction scenarios clean their own GIDs before schema teardown.

## Startup options

`fixture.Main(m, opts...)` applies options before eager startup; native scenario
engines inherit the same mode.

- `WithCommitTimestampTracking()` enables commit timestamps on the package's single
  oracle (used by `locking_transactions`; other packages stay default-off). It is
  not a session SET toggle and never a second container.
- `WithLogicalWAL()` starts the package's existing pinned oracle with
  `wal_level=logical`; it changes neither the image pin nor native configuration.
  Logical-decoding scenarios also require a positive stock
  `max_replication_slots` before asserting SQL validation and missing-slot errors.

### Test-only legacy crypto oracle

`WithLegacyCryptoProvider()` (used only by the catalog packages `catalogs`,
`catalog_objects`, `catalog_privileges`, `information_schema` and `extensions`) feeds an OpenSSL
configuration into the same container (`OPENSSL_CONF=/tmp/stuntgres-openssl.cnf`)
activating both `default` and `legacy` providers, so BF/DES/CAST are available to
the oracle. Legacy activation changes the oracle's DES/CAST initialization
outcomes; native behavior is unchanged. No host file, second container or
production dependency is involved.

## Helpers

- **Scenarios.** `NewScenarioWithDriverConfig(t, configure)` runs
  `configure(*stuntgres.Driver)` before the first connector, for embedding
  registries such as foreign handlers and validators; it does not touch PostgreSQL
  or fixture ownership. Foreign sources use a separate in-process SQL database,
  never fixture internals. Legacy `NewPair(t)` always starts Docker; its notice
  helpers keep caller-specific `reference/` + `t.Name()` DSNs on the pair's Driver.
- **Database selection.** `DatabaseScenarioPool` selects an exact decoded database
  name using native `Connector.WithDatabase` or an independently escaped
  PostgreSQL URL path and trusted fixture login. It returns wrapped SQL and
  unwrapped control pools plus recorder without Ping, so callers assert startup
  failures themselves. The native opaque cluster key is never rebuilt from
  `current_database()`. Register `RoleReferenceCleanup`, then
  `DatabaseReferenceCleanup`, then the scenario on the same owning `testing.T`;
  append names before creation attempts. LIFO cleanup closes selected resources
  before bounded standalone administrative DROP DATABASE, then role removal.
  Native Driver lifetime owns registrations; there is no native DROP shim.
- **Plain-method routes.** `Step.NonContext=true` runs proven DB/Tx plain calls
  (Exec/Query/Prepare and the returned Stmt methods) with full assertions; keep a
  separate Context companion and never fabricate its recorder key. `*sql.Conn` has
  only Context methods. `HeldStatement` also serves plain DB/Tx consumers: the
  matched query uses the held statement, others use the underlying plain DB/Tx.
  Consumers such as `DeleteReferenceConsume` skip step metadata/Scan callbacks, so
  keep those checks at the callsite.
- **Access-method lifetime.** `AccessMethodReferenceCleanup(t, names)` registers
  database-global method cleanup before a scenario is created and returns its
  cleanup function. Invoke that function after `NewScenario` starts the oracle
  and before scenario SQL; registered cleanup runs after borrowers close.
  Schema reset alone cannot retire methods.
- **Lock observation.** `LockWaitBehind(t, ctx, waiter, holder, relation, mode,
  completed...)` requires the exact waiter/holder/relation/mode queue witness. The
  optional completion channel is only for self-timing SQL (return then means
  observed *or* already completed); callers still join and validate the outcome.
  External cancellation barriers omit the channel.
  `NativeUpdateRowWait` and `NativeDeleteRowWait` require the mutation's actual
  parked row-request frame and the `LockOperation` worker in one complete live
  native stack before its holder commits or rolls back. A started channel or
  elapsed delay is not admission; premature worker completion fails, and the
  caller still joins and validates its outcome.
  `NativeUniqueKeyWait` uses the same bounded observer, requiring the actual
  `statementHost.ClaimUniqueKey`, parked `relationRequest.wait` and
  `LockOperation` worker frames in one live stack. It observes the measured
  worker itself, not a prior timeout probe or an unrelated pending request.
  `RelationLockPending` observes the exact PostgreSQL relation/mode/blocker tuple
  (relation zero selects a real transaction-ID name-conflict wait), or the native
  relation-request wait and `LockOperation` frames in one complete live stack.
  Capture PostgreSQL PIDs before starting a worker; never query its busy Conn.
  The supplied completion signal fails premature native completion; callers
  retain `LockOperation`'s cancellation/join ownership on every exit.
  `ObjectLockPending` observes the actual dependency endpoint class/OID/subobject,
  requested mode and blocker, never a synthetic relation or name-conflict tag.
  Native admission additionally requires the parked manager/worker stack and
  the exact real object row with its granted blocker. Completion before the
  required witness fails; callers still own worker cancellation and joining.
- **Active cancellation.** `BinaryConversionActiveCancel` and `SleepActiveCancel`
  cancel only after real admission: natively, the exact provider and fixture worker
  frames in one complete live goroutine stack (creation metadata does not count);
  on PostgreSQL, the actual query and wait event. Admission deadline 15 s; every
  exit cancels and joins before statements and connections close. Sampling stops
  the world, so after a miss it waits 1 ms on a reusable timer. Native admission
  matches function names, so **update the probes when renaming or moving a
  provider or worker**; a stale name makes the observer wait on a runaway query.
  `BinaryConversionActiveCancel` also holds a native provider's actual context
  cancellation poll until cancellation, only when that poll's complete current
  stack contains both the requested provider and operation worker. The external
  live-stack observer still establishes admission before canceling; unrelated
  context callers remain unblocked and underlying context semantics are retained.
  Finite provider work therefore cannot escape between observer samples. Progress
  callbacks and sleep helpers keep their existing observation routes.
  - `SleepActiveCancelWithContext` and `PLpgSQLLoopActiveCancel` accept an
    independently observed PostgreSQL diagnostic Context. The loop wrapper admits
    the actual `executePLLoop` worker, including an empty body, without a sleep wait
    event. Helpers without a Context argument require empty Context. Native
    cancellation returns `context.Canceled`.
  - `PLpgSQLIntegerForActiveCancel` uses the same admission/cancel/join contract
    with the live `executePLIntegerFor` frame and caller-specified PostgreSQL
    Context. Finite integer ranges need no sleep statement or wait event.
  - `BinaryConversionActiveCancelAfter` runs a progress callback after admission
    and before cancellation, on the supplied context and a different connection;
    consumers assert a real progress transition, not a timer.
  - `SQLCursorCancelDemand(t, p, conn, query)` cancels an admitted oracle query and
    checks the complete diagnostic (cursor FETCH, HOLD COMMIT, SQL-prepared writes).
- **Schema and role scopes.** `WithPublicSchemaOwner(t, pair, backend, owner,
  body)` transfers `public` to `owner` on a separate bootstrap connection and
  restores the prior owner on return or panic; PostgreSQL setup grants PUBLIC
  USAGE, never CREATE. Never provision the namespace whose denial is under test,
  take atomicity snapshots after provisioning, and do not wrap role-drop/Dump
  assertions or a whole fixture lifetime. Schema SQL GRANT/REVOKE tests use real
  ACL authority: establish reader USAGE with grants, never by transferring
  ownership, and never assume private schemas grant USAGE by default.
  `RegtypeDisplacementScenario` registers `SchemaReferenceCleanup` before its
  scenario; teardown runs after handles close under its own 15 s context.
  `UniqueReferencePublicNamespace` returns the actual `public` namespace OID (2200
  natively; the oracle's generated OID).
- **Role grants** (`role_grant_columns.go`, shared by `roles` and `object_grants`):
  `RoleGrantActor` (SET SESSION AUTHORIZATION, body, RESET),
  `RoleGrantPermissionDenied` (complete permission diagnostics and stages; distinct
  from the `RoleGrantDenied` step constructor), `RoleGrantError`,
  `RoleGrantSyntax`, `RoleGrantEnvironment`, and `RoleGrantNoticeExec` (quiet
  Prepare, close, execute, ordered notices).
- **Shared role runtime** (`role_runtime.go`): `RoleAttributesRun`, `Pool` and
  `Physical` use the scenario's exact cluster and trusted/oracle login;
  `RoleTransactionBegin`, `Step`, `Conn`, `PID` and `Wait` preserve transaction,
  actual role-lock admission and joined-owner conventions. `RoleTransactionIdentity`,
  `Exists`, `ValidOID` and independent `RolePasswordKeys`/`Encoded` supply shared
  assertions for role identity and persisted-default catalog families.
  `RoleScenarioNameScan`, `Command` and `Identity` retain exact metadata and
  held/direct identity assertions.
- **Role membership** (`role_membership.go`): `RoleMembershipScenario` exposes the
  original `T`, `P`, `Backend`, `C`, `Record`, `Notices`, `Prepared` and `Bootstrap`
  state. `RoleMembershipCases`/`CasesFor` register role cleanup and create LOGIN
  actors for each backend/prepared mode. `Run`/`Exec`/`Actor`, exact failures,
  membership flags and notices share one pinned notice-enabled connection.
  `Bootstrap` is `postgres` natively and `stuntgres` on the oracle. Family wrappers
  keep their own setup; copies used for alternate owners retain existing cleanup.
- **Role inquiries** (`access_role.go`): `AccessRoleCases`, `Metadata`, `Select`,
  `Bool`, `OID`, `Vector`, `Graph`, `Table`, `RuntimeError` and `HeldBool` share the
  membership scenario across `roles` and `object_address`, preserving strict
  Boolean descriptors, schema setup, direct/held routes and complete diagnostics.
- **Operator aliases.** `regoperator.go` owns `RegOperatorRows`, `Metadata`,
  `DiagnosticRoutes`, `Error`, `Literal` and `Context`, shared by `operators` and
  `type_resolution`.
- **COPY.** Native COPY uses the public pinned-connection `CopyFrom`/`CopyTo`;
  PostgreSQL uses `Pair.CopyPostgres`, a pgx stdlib stream on the same oracle.
  `CopyNoticeConnection(t, pair, postgres, &records, handler)` returns a pinned
  borrower whose notices arrive through native `WithNoticeHandler` or pgx
  `OnNotice`, and owns its cleanup. Never pass wrapped pq connections to pgx Raw,
  and never re-enter the Raw-owning `*sql.Conn` from the handler. Inspect records
  after the command; notice timing may differ by backend.
- **MERGE.** `MergeRun(t, p, backend, prepared)` runs MERGE steps with the
  backend's affected count (lib/pq reports 0; native reports real counts), so
  persisted rows must prove the effects.
- **Sessions and settings.** `SettingsPhysical` compares the public physical
  driver object; `BatchSet`, `BatchReadSet` and `BatchSets` assert multi-result
  direct-message batches; `NoticeWitnesses` and `NoticesInternalDebug` keep SQL
  notices exact while treating PostgreSQL DEBUG tracing as incidental.
- **Transactional definitions.** `DefinitionLockOperation` uses the existing
  joined worker/native scheduler observer and checks the oracle's exact queued
  relation, transaction-ID or object lock, mode and blocker. `DefinitionLockCall`
  accepts an execution closure for genuinely held statements on the same owner,
  with the same queue observation and joined-worker lifetime. Object queues include
  captured class/OID/subobject identity; no name-derived replacement is accepted.
  `DefinitionActiveCancel` cancels only after this admission and joins before
  returning. `DefinitionIndexWaitDiagnostic` checks all portable error fields,
  allowing only physical heap coordinates to vary in the anchored PostgreSQL
  index-insertion Context; native Context remains empty. Consumers include the
  collation, conversion and cast `TransactionConflicts` reference definitions.
  `DefinitionIdentityRows` captures every exposed catalog field and
  `DefinitionIdentityEqual` compares the full identity/settings rows.
  `DefinitionAlterIdentity` shares collation/conversion original-target cases:
  observe the original AX wait, commit a holder rename/schema move or roll it
  back, then assert full OID/settings/dependency preservation and healthy reuse.
  After holder commit PostgreSQL may alter the search-path fallback; native
  instead returns ordinary `ErrCatalogReplaced` without SQLSTATE and leaves that
  fallback untouched. Native recovery fully rolls back, opens a fresh transaction
  and savepoint on the same physical owner, and exercises qualified ALTER;
  PostgreSQL and holder-rollback success paths retain rollback-to-savepoint.
  Collation CREATE admission cases distinguish pre-SRX semantic errors and
  same-encoding duplicate/skip completion from protected cross-encoding checks
  and concurrent exact-key `23505`, including IF NOT EXISTS.
- **Server.** `NewServerFixture(t)` builds and launches a real `cmd/stuntgres`
  subprocess (temporary userfile, dumps, logs and PID file; reduced-KDF build tag)
  for `reference/sessions` wire tests. Embedded scenarios never start a server.
  `NewServerFixture(t, fixture.ServerQueryLog())` adds `-q` so the native server
  writes `Logdir/query.log`. `fixture.ServerArgs(...)` appends serve options;
  `fixture.ServerDualReference()` runs `ServerDatabase` in dual-backend mode
  against the pinned PostgreSQL (real mode) or a second native server (quick
  mode), exposed directly as `ReferenceDB`/`ReferenceURL`.

## Testing

Fixture self-tests (`fixture_*_test.go`) cover oracle provenance, native isolation,
reset isolation, session-authorization reset, notice and byte ownership:

```sh
make check RUN='^TestReferenceFixture'
make check REFERENCE=1 RUN='^TestReferenceFixture'
```

A change to central fixture code (step runner, `HeldStatement`, reset, pool
ownership) affects every package: run `make check RUN='^TestReference'` and the
same selector with `REFERENCE=1`. After changing a shared helper, run every
consumer's selector in both modes.
