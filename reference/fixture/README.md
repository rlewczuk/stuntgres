# reference/fixture

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### PostgreSQL oracle policy and provenance

The current external oracle is PostgreSQL18.6, pinned to the verified OCI index:

```text
postgres:18.6-bookworm@sha256:afc7e2d441324c0388fa80c3d24f733b4194a4eb7f47dd8ee2b08eb1a24a647c
```

The index is multi-platform, not a platform-specific manifest. The verified
linux/amd64 child is
`sha256:9551dd5bf356409a21b31248f49b33bba9bb1877365f3a7e933aecd9af3cf52d`.
Acquisition and runtime smoke observed
`18.6 (Debian 18.6-1.pgdg12+2)`, numeric version180006, UTF8 encoding and
C database collation/ctype. See the [18.6 release notes](https://www.postgresql.org/docs/release/18.6/),
[official image definitions](https://github.com/docker-library/official-images/blob/master/library/postgres)
and [registry tag metadata](https://hub.docker.com/v2/repositories/library/postgres/tags/18.6-bookworm).
The digest, rather than the mutable tag, determines the image used.

Startup queries the actual server and requires release18.6 (allowing its vendor
suffix), numeric180006, UTF8, `datcollate=C`, `datctype=C`, `lc_messages=C`,
prepared capacity100 and the requested commit-timestamp tracking mode. After
bootstrap-owner setup succeeds, one stderr provenance record includes container
ID, full pinned image/index digest and all actual version/encoding/locale fields.
It does not synthesize a server version or require Docker manifest inspection.
Startup/image/network/configuration failures remain visible errors, never skips.

Each package retains one serialized, bounded-lifecycle container and shared admin
pool, with fresh scenario pools and wrapped/native controls. Startup is bounded
by three minutes, scenarios by30 seconds, resets by15 seconds and termination
by45 seconds. Borrowers, rows and workers must finish before cleanup; resets
retain standalone ROLLBACK then DISCARD ALL then public-schema reconstruction.
Native wire/runtime version reporting is unchanged; native is not PostgreSQL18.6.
Historical PostgreSQL18.0 observations and feature goldens remain18.0 evidence:
this oracle alignment neither relabels them nor gates unchanged behavior.

`TestReferenceFixtureOracleProvenance` independently asserts direct/prepared
`SELECT 6*7 AS answer` returns one INT4 column `answer` and one row42 on each
available backend, plus an unwrapped native control read. Only actual PostgreSQL
is queried for18.6/180006 and UTF8/C/C/C metadata. Existing
`TestReferenceRoleGrantDiagnostics` and `TestReferenceRoleGrantAtomicity` retain
independent SQLSTATE42601 with exact English `unrecognized privilege type "bogus"`
and `syntax error at or near ")"`, and42501 `permission denied for table t`,
including their direct/prepared stages and syntax-prefix rollback assertions.
No backend-parity result substitutes for these expectations.

Feature smoke and selected proof (external900-second deadline per command):

```sh
make check RUN='^TestReferenceFixtureOracleProvenance$' REFERENCE=1
make check RUN='^TestReferenceFixtureOracleProvenance$'
make check RUN='^TestReferenceFixture(NativeIsolation|ResetIsolation|SessionAuthorizationReset)$' REFERENCE=1
make check RUN='^TestReferenceFixture(NativeIsolation|ResetIsolation|SessionAuthorizationReset)$'
make check RUN='^TestReference(DirectAndPreparedParameters|RoleGrantDiagnostics|RoleGrantAtomicity)$' REFERENCE=1
make check RUN='^TestReference(DirectAndPreparedParameters|RoleGrantDiagnostics|RoleGrantAtomicity)$'
```

Expected: all pass with independent SQL/diagnostic and ownership assertions
unchanged; real mode validates the pinned runtime, while quick mode needs no
Docker and makes no PostgreSQL-release claim. Successful-package stderr may be
suppressed by Go's quiet runner; the real-backend metadata assertions still run.
These checks do not replace whole-project Docker-free quickcheck or post-merge
finalcheck.

### Shared grant scenario helpers

`role_grant_columns.go` owns the common helpers used by both
[role proofs](../roles/README.md#testing) and
[non-table object-grant proofs](../object_grants/README.md#testing):

- `RoleGrantActor` retains SET SESSION AUTHORIZATION, the supplied body and RESET.
- `RoleGrantPermissionDenied` asserts complete permission diagnostics, including
  schema positions and direct/prepared error stages. The existing
  `RoleGrantDenied` table-error step constructor remains a separate API.
- `RoleGrantError` and `RoleGrantSyntax` retain exact diagnostics and error stages.
- `RoleGrantEnvironment` obtains and validates current user/database identities.
- `RoleGrantNoticeExec` requires quiet Prepare, closes that description, then
  executes the requested direct/prepared route and checks ordered full notices.

These are single shared implementations, not compatibility wrappers. Migrated
callers retain their SQL, held consumers, native controls and independent
expectations; run every affected definition in both modes before review.

### Fixture reset isolation coverage

`TestReferenceFixtureResetIsolation` and
`TestReferenceFixtureSessionAuthorizationReset` independently cover this boundary:

```sh
make check RUN='^TestReferenceFixture'
make check REFERENCE=1 RUN='^TestReferenceFixture'
```

Schema-owner introspection uses actual `pg_namespace`/`pg_roles` identities.
Fresh native public has owner6171 (`pg_database_owner`); the ordinary PostgreSQL
fixture's reconstructed public belongs to its actual bootstrap role10. Neither
baseline is replaced with an invented common owner. Native reset isolation also
asserts bootstrap principals, fresh settings, an empty public Dump and same-name
empty-table recreation. Its native-only public Reset removes actual data/catalog
state while preserving bootstrap principals and initialized namespace metadata.

### Scoped public schema ownership

`WithPublicSchemaOwner(t, pair, backend, owner, body)` supplies real, bounded
schema ownership for intended positive CREATE or successor-owner eligibility.
It captures public's actual prior owner, transfers ownership through supported
SQL on a separate bootstrap connection, invokes the body without changing its
actor handle, and restores the exact prior owner before returning or unwinding.
PostgreSQL setup grants PUBLIC USAGE, never CREATE. No new ACL API, membership,
superuser capability or permissive global reset is involved; table owners and
ACL effects remain genuine. Never provision the namespace whose USAGE/CREATE
denial is under test. A bounded positive public destination prerequisite may
surround a statement expected to fail an independently denied source/type/table
or ownership check. Capture exact atomicity snapshots after provisioning and
immediately before the tested statement. Do not wrap role-drop/Dump assertions
or an entire fixture lifetime in this scope.

The public regression is part of `TestReferencePGNamespaceVisibility`: actual
direct/prepared creates, namespace/actor/object identities, post-scope denial
and panic unwinding with prior-owner restoration are independently asserted.

```sh
make check RUN='^TestReferencePGNamespaceVisibility$'
make check REFERENCE=1 RUN='^TestReferencePGNamespaceVisibility$'
```

Use external900-second deadlines and the migration owner's additional exact
changed-definition selectors for every helper consumer.

### Held plain-method consumers

The shared fixture.HeldStatement retains its three-field layout and adds actual
plain DB/Tx Prepare/Exec/Query consumers alongside existing Context consumers.
Its matched query uses the genuinely held statement; unmatched queries use the
underlying plain DB/Tx, never a Context stand-in. This central fixture change also
requires supplemental identical all-reference risk selection (each900 seconds):

```sh
make check RUN='^TestReference'
make check REFERENCE=1 RUN='^TestReference'
```

### Session authorization reset coverage

`TestReferenceFixtureSessionAuthorizationReset` retains its schema-owner and
empty-public assertions and now deliberately contaminates authorization, SET
ROLE, settings, TEMP, named PREPARE, advisory state and an aborted transaction
before both reset boundaries. The same definition runs without Docker, proving
fresh wrapped/native control settings, identities, prepared consumers and case
catalogs; admin-only sanitation checks run in pinned mode. It does not assert
incidental backend PIDs, reconnection counts or timing. Focused gates, each with
a 900-second deadline:
`make check RUN='^TestReferenceFixture(SessionAuthorizationReset|NativeIsolation)'`
and
`make check REFERENCE=1 RUN='^TestReferenceFixture(SessionAuthorizationReset|NativeIsolation)'`.

### Fixture ownership regressions

Fixture ownership regressions live beside the common exported helpers in
`reference/fixture/fixture_native_isolation_test.go` and
`reference/fixture/fixture_notice_ownership_test.go`. These identical definitions
run in both modes (900 seconds per invocation):

```sh
make check RUN='^TestReferenceFixture(NativeIsolation|LegacyNoticeOwnership|ByteOwnership|ResetIsolation|SessionAuthorizationReset)$'
make check REFERENCE=1 RUN='^TestReferenceFixture(NativeIsolation|LegacyNoticeOwnership|ByteOwnership|ResetIsolation|SessionAuthorizationReset)$'
```

These selectors do not replace whole-project Docker-free `make quickcheck`
verification or post-merge `make finalcheck`. Use `RACE=1` on a selected
`make check` command for bounded race proof, and `REFERENCE=1 RACE=1` for the
same definitions against pinned PostgreSQL. Reduced engine retention is an
ownership correction, not evidence of a measured performance improvement.

### Regtype displacement schema teardown

`RegtypeDisplacementScenario` registers `SchemaReferenceCleanup` before creating
its scenario. PostgreSQL schema teardown runs after borrowed handles and pools
close, under the existing independent 15-second cleanup context. Native catalogs
are fixture-isolated and need no cross-case SQL teardown. Cleanup never borrows
the scenario's 30-second work context, including after foreign replay.

Run each affected selector in quick mode and with `REFERENCE=1`, using an
external 900-second deadline:

```sh
make check RUN='^TestReferenceRegtypeArrayDisplacement(Collisions|Atomicity)?$'
make check RUN='^TestReferenceRegtypeArrayDisplacement(Controls|Executable)$'
make check RUN='^TestReferenceRegtypeArrayDisplacement(MixedReplay|NamedDomainReplay)$'
make check RUN='^TestReferencePartialIndexTypeGeneration$'
```

Selected proof does not replace `make quickcheck` or post-merge `make finalcheck`.

### Active provider cancellation observation

`BinaryConversionActiveCancel` and `SleepActiveCancel` require real admission
before cancellation, not merely a sufficiently large query or a precanceled
context. Native admission requires the exact provider and fixture worker in the
same complete live goroutine stack; creation metadata is not a live frame.
PostgreSQL independently observes its actual query and optional wait event.
The existing admission deadline remains15 seconds, and every exit cancels and
joins the worker before held statements or physical connections close.

`BinaryConversionActiveCancelAfter` uses the same observer, deadline, cancellation
and worker-join implementation, with an additional progress callback after real
provider/server admission and before cancellation. The callback uses the supplied
bounded context and a different connection, never the busy physical connection.
Consumers must assert a real independently specified progress transition, not
delay cancellation by a timer or weaken provider admission. The password multirow
witness observes nontransactional sequence progress into the later cost31 row
after an earlier literal-hash row.

Native complete-stack sampling stops the world. The first sample is immediate;
after an unsuccessful complete sample, a reusable timer waits1 millisecond with
context/completion-aware selection before sampling again. The delay begins
after sampling, not on a ticker that can accumulate ticks. This bounds observer
pressure without substituting fake admission, changing workloads or deadlines,
or accepting a completed query as successful cancellation. Actual cancellation,
no published cursor/result, stored-state checks and held-statement recovery
remain required.

Every direct or indirect consumer requires selected native and real proof:

```sh
make check RUN='^TestReference(OrderedSetAggregateActiveCancellation|BinaryConversionActiveCancellation|JSONArrayLengthCancellation|ConversionCancellation)$'
make check REFERENCE=1 RUN='^TestReference(OrderedSetAggregateActiveCancellation|BinaryConversionActiveCancellation|JSONArrayLengthCancellation|ConversionCancellation)$'
make check RUN='^TestReferenceDatetime(SleepCancellation|SleepFeatureSmoke|ZoneActiveCancellation)$'
make check REFERENCE=1 RUN='^TestReferenceDatetime(SleepCancellation|SleepFeatureSmoke|ZoneActiveCancellation)$'
make check RUN='^TestReference(QueryParallelCancellation|QueryRecursiveCTELifecycle|TemporaryViewCancellation)$'
make check REFERENCE=1 RUN='^TestReference(QueryParallelCancellation|QueryRecursiveCTELifecycle|TemporaryViewCancellation)$'
make check RUN='^TestReferencePgcryptoPasswordCancellation$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoPasswordCancellation$'
make check RACE=1 RUN='^TestReference(OrderedSetAggregateActiveCancellation|BinaryConversionActiveCancellation|JSONArrayLengthCancellation|ConversionCancellation)$'
make check RACE=1 RUN='^TestReferenceDatetime(SleepCancellation|SleepFeatureSmoke|ZoneActiveCancellation)$'
make check RACE=1 RUN='^TestReference(QueryParallelCancellation|QueryRecursiveCTELifecycle|TemporaryViewCancellation)$'
make check RACE=1 RUN='^TestReferencePgcryptoPasswordCancellation$'
```

Each invocation has an external900-second deadline. These selected proofs do
not replace whole-project Docker-free quickcheck or post-merge finalcheck.

### Pre-engine embedding configuration

`NewScenarioWithDriverConfig(t, configure)` runs `configure(*stuntgres.Driver)`
before the native fixture opens its first connector. It permits real embedding
capabilities such as foreign handlers and validators to use the ordinary frozen
Driver registry. It does not replace SQL execution, alter pinned PostgreSQL,
or change fixture reset, session, notice, pool or cleanup ownership.
`NewScenario(t)` retains the same behavior with no configuration callback.

The public foreign-source consumers use a separate in-process SQL database,
not fixture-internal engine objects. Run the changed consumer definitions in
both modes, with external 900-second deadlines:

```sh
make check RUN='^TestReferenceFDW'
make check REFERENCE=1 RUN='^TestReferenceFDW'
```

### Shared operator-alias assertions

`regoperator.go` owns the existing `RegOperatorRows`, `RegOperatorMetadata`,
`RegOperatorDiagnosticRoutes`, `RegOperatorError`, `RegOperatorLiteral` and
`RegOperatorContext` contracts shared by
[operator definition proofs](../operators/README.md#testing) and
[operator OID alias proofs](../type_resolution/README.md#operator-oid-aliases).
They preserve raw alias/OID transport, full metadata and all literal,
direct/held inferred-parameter and direct/prepared runtime-text diagnostic routes.
No scenario body or backend implementation is duplicated.

Root selectively runs all callers in both modes after helper migration
(external 900-second deadline per invocation):

```sh
make check RUN='^TestReferenceRegOperator(ValuesMetadata|Signatures|Diagnostics|ParserEdges|NamespaceAuthorization|Inventory)$' REFERENCE=1
make check RUN='^TestReferenceRegOperator(ValuesMetadata|Signatures|Diagnostics|ParserEdges|NamespaceAuthorization|Inventory)$'
make check RUN='^TestReferenceRegOperator(ExternalBoundary|PreparedLifetime|PreparedAnalysis|Persistence|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceRegOperator(ExternalBoundary|PreparedLifetime|PreparedAnalysis|Persistence|FeatureSmoke)$'
```

