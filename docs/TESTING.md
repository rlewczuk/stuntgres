# Adding and maintaining tests

This guide holds the shared test policy, fixture usage and test-first workflow.
Package-specific helpers, rules and family selectors belong in the owning
package's `README.md` under `## Testing`.

## Placement policy

- Tests that exercise public APIs by issuing SQL and checking responses MUST live
  in `reference/` as **dual-mode reference tests**. This covers direct and prepared
  execution, rows, metadata, diagnostics, cancellation and SQL witnesses around
  native controls. Root, `smoke/` (legacy; only `smoke/catalog_test.go` remains),
  `internal/` and other directories are forbidden for them.
- When working in an area with offending tests, migrate them to `reference/`.
  Keep the complete assertion set and execution routes, merge with existing
  coverage, and delete the originals; leave no native-only copies.
- Files tagged `//go:build reference` hold legacy PostgreSQL-only tests that run
  only with `REFERENCE=1` (a quick-mode run never covers them). Migrate them to
  untagged dual-mode tests when working in their area.
- Internal unit tests of package functions stay beside their package. They
  supplement, never replace, reference coverage.
- For retained prepared artifacts, assert real definition ownership, generation,
  immutable backing and rejected publication separately from public SQL outcomes;
  repeated pure-function equality cannot distinguish reuse from recomputation.
  Semantic dependency fixtures must advance effective `Revision()` stamps,
  not raw zero-default revision fields, and distinguish unchanged-codec
  presentation changes from executable codec replacement. Demanded unary
  ownership witnesses can use `COALESCE(-(7::integer),0)`: the cast keeps
  negation as an operator without crossing selected-call arithmetic limits.
  Structural comparisons across separate bindings must distinguish fold expansion
  ordinals from the semantic contract: assert valid distinct provenance, then
  compare all remaining fields, including original provider authority and logical
  properties. Reconstruction or completion of one binding must preserve its exact
  provenance and inputs. Captured identifier signatures retain every operand
  occurrence in source order; binder manifests project these stamps to operand
  purpose without discarding their exact identity or revision.
  Composed prepared tests use the real `core.Description`, definition registry,
  accepted binding and `core.FoldPublication` operations. Save immutable
  `exec.AcceptedFolds` values when comparing old backing and generations; copying
  a description does not snapshot its mutable definition owner. When a failed
  Execute releases a producer, distinguish terminal detachment of the live
  resource's graph and fold group from mutation of accepted artifacts. Retain
  the actual accepted graph and fold objects before execution; assert their
  provenance, values, generation and backing independently from live-resource
  cleanup, without constructing replacement bindings or snapshots of authority.
  Pure typed
  ownership, datum identity and cancellation cases live in `exec/enginetests`;
  stored retagging and pruning cases live in `stored/enginetests`. Keep actual
  graph/resource, occurrence, private-context and rejected-publication assertions
  in the composed owner tests, alongside shared reference scenarios for SQL
  outcomes. Do not add inspection exports or a second observation registry.
- Internal tests that need a composed engine are external tests
  (`package <owner>_test`) beside the package that owns the code under test,
  built with the [`enginetest`](../internal/engine/enginetest/README.md)
  helpers; statement runtime belongs to `internal/engine/core/`. New ones are
  never added to the root `internal/engine` package. A lower package with
  internal tests (the engine subpackages below core, `internal/sqltype`) keeps
  them in a test-only `enginetests/` subdirectory with a one-sentence `doc.go`:
  in the owner's own directory, `go test` would rebuild the root and every
  package in between against the owner's test variant. Internal tests cannot
  import `enginetest` (import cycle). Tests extracted from core go to
  `internal/engine/core/<x>` with their code, or to
  `internal/engine/core/enginetests/<area>`.
- Every new PostgreSQL feature needs new or changed reference coverage, and every
  compatibility bug needs a failing reference regression before its fix.
- One test body serves both modes. Mode changes which backends exist, never the
  scenario; do not maintain near-duplicate PostgreSQL and native versions.
- Use only public stuntgres APIs in `reference/` (no internal packages or
  root-private helpers). Name tests `TestReference<Feature><Behavior>`; use untagged
  `*_test.go` files so `make quickcheck` runs them.
- Assert behavior, boundaries, errors, atomicity and lifecycle, not source text,
  wiring counts, mock echoes or backend parity alone. Keep feature notices and
  their diagnostics exact; PostgreSQL DEBUG trace wording, allocator identities
  and loader order are not expectations. Keep output quiet.
- Organize tests in feature-group packages under `reference/`, with shared helpers
  in `reference/fixture`. At most 50 packages (including the fixture) and at most
  1,000,000 bytes per package; functional files at most 750 lines. Do not split or
  duplicate test bodies to meet these limits.
- Native Reset/Dump/Restore have no PostgreSQL equivalent: keep their witnesses as
  explicit native-only phases inside the same dual-mode scenario.

## Test-first workflow

1. **Write the shared scenario and run real PostgreSQL first**, using the pinned
   oracle ([fixture policy](../reference/fixture/README.md#postgresql-oracle-policy-and-provenance)),
   never a local or unpinned server. Start from independently reasoned
   expectations; use the oracle to resolve unknown values, metadata and diagnostics.
2. **Record the oracle's results as the contract**: rows, column names, database
   type names, affected counts and diagnostics. Never derive expectations from
   stuntgres output, copy native failures into goldens or assert only equality.
3. **Run quick mode** and watch the regression fail. Implement the smallest correct
   change and rerun until it passes.
4. **Run both modes selectively** for every added, changed or migrated test, again
   after any later edit to those tests or their implementation. Then run
   `make quickcheck`. Whole-project targets never replace the selected runs.

```sh
make check REFERENCE=1 RUN='^TestReferenceFeatureBehavior$'  # oracle (and native)
make check RUN='^TestReferenceFeatureBehavior$'              # quick mode
make quickcheck                                             # before review/merge
```

For foreign support-function diagnostics and genuine SQL validators, use a small
selected pair in both modes:

```sh
make check REFERENCE=1 RUN='^(TestReferenceFDWSupportFunctions|TestReferenceFDWSQLValidator)$'
make check RUN='^(TestReferenceFDWSupportFunctions|TestReferenceFDWSQLValidator)$'
```

These scenarios assert full diagnostics, typed argument witnesses and failed
metadata/effect publication, rather than only comparing backend results.

The external SQL provider family keeps its registration/catalog contracts
Docker-free. Physical ownership, credentials, cancellation and notification
phases require `REFERENCE=1`; that mode exercises both a native registered GO
handler backed by genuine PostgreSQL and the stock postgres_fdw comparison, not
an embedded substitute. The actual fixture pin remains PostgreSQL 18.6.

```sh
make check RUN='^TestExternalSQLProvider(Registration|Ownership|ResultValidation)$'
make check RUN='^TestReferenceExternalSQLProvider(Contracts|Physical|Credentials|Transactions|Cancellation|DiagnosticsAndNotifications|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestExternalSQLProvider(Registration|Ownership|ResultValidation)$'
make check REFERENCE=1 RUN='^TestReferenceExternalSQLProvider(Contracts|Physical|Credentials|Transactions|Cancellation|DiagnosticsAndNotifications|Controls|FeatureSmoke)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceExternalSQLProvider(Cancellation|Transactions|Controls)$'
make check REFERENCE=1 RUN='^TestReferenceExternalSQLProviderFeatureSmoke$'
```

The column-option admission regression uses the same public SQL/MED contract
without opening a transport. Its DROP/re-add and unchanged-skip matrix asserts
full execution diagnostics, schema/data atomicity and genuine SQL-validator
rejection through direct/prepared calls and both foreign ALTER spellings:

```sh
make check RUN='^TestReferenceExternalSQLProviderColumnOptions(DropAddOrdinary|DropAddForeign|SkippedAdd)$'
make check REFERENCE=1 RUN='^TestReferenceExternalSQLProviderColumnOptions(DropAddOrdinary|DropAddForeign|SkippedAdd)$'
```

For oracle-first discovery, the actual physical leaves are
`TestReferenceExternalSQLProviderPhysical/postgres` then `/memory`. Endpoint
error DETAIL and PL/pgSQL Context remain complete golden fields, resolved from
the pinned oracle rather than removed for parity. Feature smoke performs real
Open/rows/types/error/cancel/notification/control operations; it is not a suite
alias. Quick mode intentionally does not exercise the network-only phases.

During discovery only, filter the shared `p.Backends()` loop to `backend.PG`
(`if !backend.PG { continue }`) so a missing native feature cannot block oracle
observation. Remove it before quick-mode validation; never commit a backend filter.
There is no golden recorder and no PostgreSQL-only mode: `p.Step` validates
expectations, it never learns them. Inspect unknown results through failure
diagnostics or temporary logging, encode them, then remove the scaffolding.

### Dblink connection verification

Shared connection scenarios use the explicit Driver binding and presence-aware
request API with the one genuine host adapter. Physical quick-mode skips are
explicit, never embedded/fake transport proof. Declaration, NULL, canonical
binding-prerequisite and persistent-object phases remain Docker-free.
Selected checks must include both native and real PostgreSQL leaves after every
new, changed or migrated definition; whole-project quickcheck is not oracle proof.

```sh
make check REFERENCE=1 RUN='^TestReferenceDblinkConnection(Signatures|Nulls|Names|Lifecycle|Conninfo|PortBounds|Security|Controls|Cleanup|FeatureSmoke)$'
make check RUN='^TestReferenceDblinkConnection(Signatures|Nulls|Names|Lifecycle|Conninfo|PortBounds|Security|Controls|Cleanup|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceDblinkConnectionFeatureSmoke$'
make check RUN='^TestExternalSQLProviderAuthObservation$'
make check REFERENCE=1 RUN='^TestExternalSQLProviderAuthObservation$'
make check RUN='^TestDblinkConnection.*$'
make check REFERENCE=1 RUN='^TestDblinkConnection.*$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceDblinkConnection(Lifecycle|Security|Controls|Cleanup)$'
```

No discovery filter belongs in `connectionCase`. Full logical diagnostics and
metadata use independent expectations, including superuser duplicate-before-parse
versus restricted original-secret-before-duplicate admission. New URI/resolver,
alias, notice and schema-position cases still need actual pinned-oracle evidence,
never native-generated goldens.
PG checks filter the seven scoped functions and six prior relationships; the
native manifest alone is exactly13 members. Do not constrain PG's full extension
to thirteen or claim its genuine query/cursor functions are absent.

Connection security distinguishes constant-NULL STRICT elimination from a
surviving call: literal NULL can return typed NULL without EXECUTE admission;
independent nonNULL and NULL-column calls must still reject untrusted callers
with exact `42501` diagnostics. Do not move all runtime NULL handling before
ACL checks. Held SQL can be reanalyzed after rename/move and resolve an ordinary
shadow; stored view dependencies and held current-name calls separately check
authentic identity and current admission. URI query values use percent-encoded
spaces (`%20`), not form `+`, which libpq preserves literally. The fixture emits
full logical diagnostics for unexpected SQL errors without adding success logs
or weakening fixed diagnostic comparisons.

Physical assertions use real PIDs/user/database, isolated remote state,
retained public provider Execute failures and independent backend disappearance.
PG-only query-family helpers observe the oracle's remote state; they do not add
native query-family implementations. The shared adapter passively inspects only
backend Authentication codes and keeps a mechanism enum, never protocol trace
or password/SASL payloads. A role-scoped actual HBA trust fixture plus observed
successful handshake distinguishes trust from real SCRAM; configured passwords
and HBA text alone are not proof. Fragmentation units supplement, not replace,
real authentication. Only externally variable connection-failure Detail is
bounded as a comparison limit; parser Detail/full remaining diagnostics are exact.
Native controls cover matching, missing, unbound and version-mismatched binding;
canonical bound/unbound prerequisite omission, malformed and duplicate markers;
persistent identities/data; actual close and cancellation-ignoring pending returns
for physical Close, Reset and Restore. Failed/canceled controls preserve current
handles and pending work. Successful publication cleans only affected-database
dblink generations and leaves the same Owner usable, with other-database/Driver
and existing TEMP/settings contracts separately witnessed. Postpublication close
errors report published state. Raw host establishment markers retain ordinary
errors.Is/As and no SQLState; only dblink Open projects explicitly attested genuine
connection failures to08001. Context/callback/Execute/Close errors stay ordinary.


### Command details

- `check` requires `RUN` selecting individual tests or a small set; `RACE=1` adds
  the race detector. Targets impose no deadline (`-timeout=0`); agents use 900 s
  for `check` and 1800 s for `quickcheck`. `refcheck` is human-only.
- `REFERENCE=1` adds the `reference` build tag and `-reference=true`, runs **both**
  native and PostgreSQL backends, and serializes packages with `-p 1` (each package
  owns one container). `reference/cmd/checkpackages` selects packages with
  matching top-level test names; selectors containing `/` keep every package, and
  a selector that matches nothing still starts the fixture package's oracle.
  Startup failures fail visibly; they are never skipped.
- Make expands `$` in `RUN`. A `$` before `|`, `/` or `)` must be written `$$`
  (`RUN='^TestX$$/^postgres$$'`), or wrap alternatives once
  (`RUN='^(TestA|TestB)$'`). An internal `$|` silently selects nothing; a
  no-match run is not evidence.

## The dual-mode fixture

`fixture.NewScenario(t)` creates a fresh native engine with its own Driver, a
middleware-wrapped native pool, an unwrapped native control pool and, in real
mode, the pinned PostgreSQL pool. It owns a `fixture.ScenarioBudget` context and pool cleanup.
Real mode resets `public` before and after each case. All real fixtures in a
package share one container, so **never use `t.Parallel()`**. Each package's
`TestMain` is `os.Exit(fixture.Main(m))`; import helpers as
`"stuntgres/reference/fixture"` without dot imports or local aliases.

- **Shared catalog state.** Only `public` is reset. Register
  `fixture.SchemaReferenceCleanup(t, "name")` *before* creating the scenario when a
  case creates or moves objects into another schema (otherwise later cases fail
  with 42P06). Register `fixture.RoleReferenceCleanup(t, names)` before creating the
  scenario, on the `testing.T` that owns it, for cluster roles. Catalog queries such
  as `pg_class` are database-wide: use scenario-unique object names or constrain
  namespaces when asserting a complete candidate bag.
- **Steps vs callbacks.** Use `p.Step` for plain statement streams. Use
  `p.Backends()` with one shared callback for transactions, pinned sessions, held
  statements, richer metadata, concurrency and oracle discovery.
- **Simulated physical statistics.** `TestReferencePgstattupleHeapKinds` keeps
  independent PostgreSQL and native expectations: after DELETE PostgreSQL retains
  the B-tree/hash physical tuple, while native current logical roots count zero.
  Run changed reference definitions with both native and `REFERENCE=1` selected
  checks. This model difference does not relax heap logical live/dead/rollback
  assertions, typed results, diagnostics, privileges or numerical consistency.
- **Sessions and cleanup.** Keep session state on a borrowed `*sql.Conn` or
  `*sql.Tx`. Close rows, then statements, then return connections before fixture,
  schema and role cleanup. Register cleanup immediately; cancel and join goroutines
  on every exit; synchronize deterministically, never with sleeps.
- **Long scenarios.** Never refresh or extend the scenario context. Give
  long inventories or heavy matrices one scenario per serial subtest, and run
  cleanup under its own bounded context after handles close.
- **Deadlocks.** Identify the victim from its error, not worker completion order.
  Join both workers under a bounded deadline and keep the victim, survivor and
  side-effect assertions.
- **Speed.** Tests must run as fast as possible: aim for under 10 ms per leaf
  case. A case that spends its time waiting rather than computing is a bug, and
  a matrix multiplies its per-leaf cost, so check leaf count × leaf time before
  adding a dimension. Never wait out a fixed delay (sleep, long timer or context
  deadline) and then assert. Wait for the condition itself: poll it in a loop with
  a very short pause (`runtime.Gosched()` or a 1 ms tick). Bound the loop by an
  overall deadline long enough for a loaded machine (seconds), so it returns as
  soon as the condition holds. Reuse the fixture observers (`RelationLockPending`,
  `LockWaitBehind`, `LockWait`) where they fit.
- **Timeouts.** Prepare held statements and finish setup at timeout 0, then arm a
  short timer: `lock_timeout` 10 ms (it counts only the lock wait, so it cannot
  fire before the statement queues) or `statement_timeout` 100 ms (it also counts
  parse and plan time). A short timer leaves no room for an observer: let its
  exact diagnostic prove the wait, and keep the strict admission observer in the
  matching cancellation case. Once armed, even `SHOW` or `RESET` can time out:
  dispose of the armed owner instead of resetting it, and never raise the timer
  or retry.
- **Cancellation.** Native pre-canceled and expired routes return ordinary context
  errors. An operation can classify an elapsed source deadline before the source's
  Done notification: check the operation error independently, then wait for the
  owned context's Done and snapshot its terminal Err before cleanup cancellation.
  For an admitted pq deadline probe, require the exact `57014` user-request
  cancellation diagnostic before waiting for Done; reject unrelated SQL and
  transport errors. Validate any physical-tuple Context with an anchored grammar
  that varies only the heap coordinates, not the action, relation or cascaded SQL.
  Return context-interrupted pq borrowers instead of reusing their possibly closed
  transport. Use an independent admission/release witness rather than racing an
  observer against a 100ms interruption probe. For PostgreSQL user cancellation,
  observe real server admission, cancel, join, then check the same physical owner.
  Never assert that a fast pre-canceled pq command returns nil outputs.
- **Randomness and floats.** Test random functions through independent
  properties, never backend parity. Finite irrational FLOAT8 results may differ by
  at most 2 ULP; NaN, infinities, signs and errors stay exact.
- **Client differences.** lib/pq reports unknown nullability for every column,
  MERGE affected count 0, INT4 `ScanType` int32 (native int64), and OID/REGTYPE raw
  values as strings or bytes. Native reports known nullability for stored table and
  view columns and unknown for most computed results. Native rows are eager with
  detached `Columns` slices; pq streams. Branch on `backend.PG` only for such independently specified
  expectations, never to duplicate the scenario.
- **Throwaway programs.** Standalone smoke programs used during development are
  never committed.
- **Explicit sepgsql simulation.** `TestReferenceSepgsqlSimulation*` in
  `reference/extension_ownership` separates two phases within each definition. The
  provider-independent phase uses `Pair.Backends` for meaningful ordinary and
  inactive-provider PostgreSQL compatibility. The named `native_simulation`
  phase configures its private Driver with `RegisterSepgsqlSimulation` and
  asserts independently specified policy outcomes. It is source-grounded native
  simulation coverage, not an active SELinux PostgreSQL oracle. Never install
  fake oracle functions/providers or count an unexecuted active phase as parity.
  Select Boundary, Labels, SharedLabels, Access, Demands, DDL, Retirement,
  Subject, Routines, Initialization, Functions, FTS, Dictionaries, Sources, PageSources, Codecs,
  Callbacks, Support, NativeBodies, Handlers, ForeignFactory, AccessMethodFactories,
  OriginalAccessMethodFactories, SurgeryIncoming, IncomingVPC, Audit, Controls,
  ControlConsent, LoadDispatch, RestoreconSharedDatabases, RestoreconAuthorization,
  RestoreconEscapedBrackets, RestoreconDroppedColumns, RestoreconPhases,
  RestoreconUnmatched, RestoreconRelabelTo, RestoreconTransactions,
  RestoreconIdentity, RestoreconFeatureSmoke,
  GenericLabelResolution, GenericLabelRoleOwnership, GenericLabelDomainKind,
  GenericLabelColumnIntegrity and FeatureSmoke in native and `REFERENCE=1` modes.
  `LoadDispatch` checks ordinary authority and genuine module activation on the
  same physical connection, including no overexplain activation from sepgsql.
  The restorecon scenarios check both-root shared labels, peer-local nonmutation,
  atomic denied-peer failure and current superuser-before-profile diagnostics.
  Their genuine post-peer Dump/Reset/Restore phases distinguish the selected
  database's serialized labels from retained destination peer labels and absence.
  `RestoreconEscapedBrackets` checks inactive provider rejection and ordinary
  catalog/data independently, then direct/prepared native restorecon BOOL results,
  actual `pg_seclabel` associations (`a`/`z` secret, `b` public), and unchanged
  unrelated/local/shared labels and rows. Select
  `make check RUN='^TestReferenceSepgsqlSimulationRestoreconEscapedBrackets$'`
  natively, with `REFERENCE=1`, and with `RACE=1`. Select
  `make check RUN='^TestSepgsqlSimulationPatternEscapedBrackets$'` natively and
  with `RACE=1` for literal hyphen/metacharacter, negation, ordinary range and
  default/named first-match registration assertions. Active results remain
  native simulation evidence, not active PostgreSQL SELinux parity.
  The dedicated restorecon family checks released five-phase boundaries rather
  than within-phase heap order, genuine dropped attnum2 versus retained tail3,
  and direct/prepared dropped-name rejection. Inactive SECURITY LABEL resolves
  the provider first (`22023`, provider not loaded); the active simulation reaches
  missing-column resolution (`42703`). Ordinary SELECT rejects the dropped name
  in either phase. Materialized-view setup asserts its populated row count.
  Wrong-arity, incompatible INTEGER and dropped-column boundaries compare
  complete diagnostics, including English primary, explicit-casts hint where
  applicable, position and every otherwise-empty exposed field. Authorization
  exercises VARCHAR and CHARACTER(6) arguments through the actual TEXT
  declaration, requiring BOOL true and an S→P association change for each direct
  and prepared call. Rejections preserve the complete local/shared association
  before-state; dropped-name probes also retain exact rows. Ordinary PostgreSQL
  witnesses use genuine builtin character/text coercions and unresolved calls,
  never substitute provider functions.
  It also checks exact unmatched WARNING fields and association absence, independent destination
  `relabelto` denial, transaction/savepoint recovery and renamed/replaced/dropped
  declaration capabilities. Dropped-column controls use real typed metadata and
  test malformed/duplicate/missing/live/undeclared-context rejection, direct SQL
  non-admission, fresh destination resolution and old/new transfer denial with
  effective-unlabeled counterparts. Select all changed definitions in both modes:

  ```sh
  make check RUN='^TestReferenceSepgsqlSimulationRestorecon(SharedDatabases|Authorization|EscapedBrackets|DroppedColumns|Phases|Unmatched|RelabelTo|Transactions|Identity)$'
  make check REFERENCE=1 RUN='^TestReferenceSepgsqlSimulationRestorecon(SharedDatabases|Authorization|EscapedBrackets|DroppedColumns|Phases|Unmatched|RelabelTo|Transactions|Identity)$'
  make check RACE=1 RUN='^TestReferenceSepgsqlSimulationRestorecon(SharedDatabases|Authorization|EscapedBrackets|DroppedColumns|Phases|Unmatched|RelabelTo|Transactions|Identity)$'
  make check RUN='^TestReferenceSepgsqlSimulationRestoreconFeatureSmoke$'
  make check REFERENCE=1 RUN='^TestReferenceSepgsqlSimulationRestoreconFeatureSmoke$'
  make check RACE=1 RUN='^TestReferenceSepgsqlSimulationRestoreconFeatureSmoke$'
  make check RUN='^Test(SepgsqlRetiredColumnMetadata|RestoreSepgsqlRetiredColumns|SepgsqlSimulationRestorecon(ScopeAndOrder|SeedAndControlScope|PublicationFences|DroppedColumns)|SepgsqlDumpSelectedDatabaseLabels|SepgsqlRoutinePayloadRemap)$'
  make check RACE=1 RUN='^Test(SepgsqlRetiredColumnMetadata|RestoreSepgsqlRetiredColumns|SepgsqlSimulationRestorecon(ScopeAndOrder|SeedAndControlScope|PublicationFences|DroppedColumns)|SepgsqlDumpSelectedDatabaseLabels|SepgsqlRoutinePayloadRemap)$'
  ```

  `RestoreconFeatureSmoke` performs changed-feature traversal, raw tombstone
  labels, rollback, denied relabel and actual renamed-capability native replay.
  `SepgsqlRetiredColumnMetadata` covers the strict typed codec;
  `RestoreSepgsqlRetiredColumns` checks actual private finish publication,
  retained immutable descriptors and failed/canceled atomic rejection. These
  internal owner units supplement, not replace, dual-mode external definitions.
  `GenericLabelResolution` checks real missing/foreign/owned objects before
  unsupported-provider rejection, including type/domain and existing tablespaces,
  without adding label support or physical tablespace creation. The focused
  `GenericLabel(RoleOwnership|DomainKind|ColumnIntegrity)` definitions check
  current CREATEROLE/ADMIN and SUPERUSER-target precedence, actual domain kind
  before ownership, and column ownership before generic kind before provider.
  They retain complete primary/detail diagnostics, real held authority/identity
  changes, unchanged labels/data/generators, and independent ordinary
  provider-unloaded plus rename/drop/comment contrasts. Active expectations are
  released-source-grounded native simulation, not active PostgreSQL SELinux parity.
  Select `TestSepgsqlSimulationGenericLabel(RoleOwnership|DomainKind|ColumnIntegrity|NoPublication|FreshOwnership|LockLifecycle)`
  natively and with `RACE=1`; select
  `TestReferenceSepgsqlSimulationGenericLabel(RoleOwnership|DomainKind|ColumnIntegrity|Resolution)`
  natively, with `REFERENCE=1`, and with `RACE=1`. Real object-lock capture must
  retain genuine domain TypeExclusive and whole-sequence ShareUpdateExclusive
  grant/wait/cancel/currentness paths, distinguish local large-object authority
  from its released lock tag, and preserve shared tablespace identity.
  `TestReferenceSepgsqlSimulationSequenceCreate` retains independent ordinary
  CREATE/options/ACL/notice/serial/identity expectations on inactive native and
  pinned PostgreSQL, plus source-grounded active-native direct/prepared/held
  default-P/name-selected-Q labels without explicit relabel. It checks zero
  sequence-column labels, genuine generator/metadata/owned links, schema-before-
  sequence denial, ordinary ACL precedence, actual denied audit bits (including
  permissive success), initial IF NOT EXISTS noevent, TX/savepoints and TEMP.
  Deterministic concurrent binding skip and namespace/OWNED BY wait currentness
  belong to `TestSepgsqlSimulationSequenceCreate(Currentness)?`: public fixtures
  have relation-lock observers but no namespace/name-binding admission observer.
  No elapsed-delay or synthetic test API substitutes for that core witness.
  Run `make check RUN='^TestReferenceSepgsqlSimulationSequenceCreate$'` with
  native defaults, `REFERENCE=1`, and `RACE=1`; select the internal owner with
  `make check RUN='^TestSepgsqlSimulationSequenceCreate(Currentness)?$'`
  natively and with `RACE=1`. The existing
  `TestReferenceSepgsqlSimulationControls` now restores real explicit, serial and
  identity generators, default/relabeled/NULL-absent sequence labels, ACLs/options/
  owners/dependencies and TEMP exclusion, with genuine sequence RelabelTo
  transfer-denial and policy-mismatch atomicity. A CREATE-only denial rejects
  public SQL through the same R pool but permits trusted Restore.
  `TestReferenceSepgsqlSimulationControlConsent`
  retains sequence state across actual reader-I/O root/role fences. Run both
  controls selectors natively, with `REFERENCE=1`, and with `RACE=1`.
  `TestSepgsqlSimulationTrustedReplayInitialization` exercises genuine isolated
  RestoreStage/session construction, ordinary scalar/default/type-input admission
  without fabricated MAC evidence, denied SQL EXECUTE, nontransferable origins,
  cancellation and closed-worker rejection.
  `TestSepgsqlSimulationRemappedRepairInitialization` covers actual generated-row
  and BRIN-expression collision repair with both an internally acquired operation
  and an authentic supplied stage-bound operation: distinct stage/destination
  epochs, unchanged original origin, exact rows/index membership and no fabricated
  MAC evidence, plus stale-origin/binding/staging/cancellation atomic rejection.
  Its inactive witness retains nil-origin multi-session maintenance.
  `TestSepgsqlSimulationGeneratedDefinitionInitialization` covers the genuine
  CREATE prospective-stage and ALTER nil-stage definition prewalk, live/import
  receipt ownership, exact generated values3/2, stable parent ownership across
  stored-program cache hits/rows, distinct column sources, original manifests
  and exact old ALTER/retained-TEMP consumers. CREATE denial selects a genuinely
  folded native helper; SQL wrappers retain FoldNever and are denied at real
  ALTER/INSERT demand. Host audit is observed only after actual operation Done.
  Real direct and prepared UPDATE retain the same explicit captured-source owner.
  Negative probes pair successful original manifests/sites with rejected changed
  manifests, completed sites, command/origin/lifecycle or physical-source headers;
  they do not promise new FHET checks on raw original INTERNAL kernels.
  Runtime JSON work is observed through the real operation policy activated by
  the unlocked statement owner; merely attaching a policy leaves it inert.
  Retained-generation row evaluation requires actual strip work on each non-NULL
  row while keeping the original initializer and receipt counts unchanged.
  Source-header probes use a new physical capture under the still-admitted
  SELECT's same prepared leaves, header, owner and catalog, then drain that same
  live nested producer after restoring its header. Eager FinalRows deliberately
  detaches live headers and is not an initialization-owner witness.
  Whole-stage epoch corruption retains the earlier exact generation-plan owner
  rejection; separate original-origin field negatives retain their own diagnostics.
  Original source/virtual/immutability/coercion order and unlocked constant-work
  vetoes remain covered: snapshot completed strip traversal at the downstream
  pause and require no additional work after stale/cancel return, not a golden
  work-unit count across legitimate definition and rewrite phases. Run
  `make check RUN='^TestSepgsqlSimulation(GeneratedDefinitionInitialization|TrustedReplayInitialization|RemappedRepairInitialization)$'`
  natively and with `RACE=1`, alongside unchanged generation source/provider/
  publication and initialization owners. Existing
  `TestReferenceGeneratedStored(Smoke|Authorization|ControlsSmoke|TableOIDControls|TableOIDRepairAtomicity|TableOIDOwnerAuthority|SameNameReferenceDiagnostics)`
  requires both native and `REFERENCE=1` selected runs (and native `RACE=1`).
  Its released ordinary SQL oracle is not active simulation/import parity.
  Retain the full inactive callsite regressions
  `TestExecutionPauseRemappedRepair(Child|Destination|Authority)`,
  `TestCatalogReplacementOperationOwner` and
  `TestRangePartitionPublicationTrustedImporterProvenance`, with all their
  healthy followups and authorization negatives. Run both modes of the public
  control scenarios above; the native six-sequence image witness remains in
  `TestSepgsqlSimulationControls`. Import-origin/private-repair proofs have no
  PostgreSQL native-control-format counterpart.
  Extend, do not replace, feature smoke:
  `make check RUN='^TestReferenceSepgsqlSimulationFeatureSmoke$'`, also with
  `REFERENCE=1`, observes genuine P/Q CREATE labels, exact create denial/no
  publication, true permissive denied-create audit and persistent replay.
  The unchanged `GenericLabelColumnIntegrity` whole-sequence P assertion remains
  a regression for the genuine producer, not a fixture relabel workaround.
  `TestSepgsqlCatalogIdentityComposition` selects the real composed label/statistics
  catalog, producer, nominal row/array and definition-reservation owners; run it
  natively and with `RACE=1`. `TestSepgsqlDumpSelectedDatabaseLabels` and
  `TestSepgsqlSimulationGenericLabelLockLifecycle` cover the genuine serializer
  and composed grant/wait/cancel/currentness paths. Public
  `TestReferencePGRewriteBuiltinDeclarations` checks direct/prepared/held
  relation/rule/type/dependency relationships using each backend's actual OIDs,
  not native-number parity with PostgreSQL initdb.
  `Handlers` includes both row-count and duration factories, with subject
  restoration before runtime arguments and reuse of the selected method table.
  `SurgeryIncoming` checks real scalar freeze/kill effects and error unwind;
  `IncomingVPC` checks buffer/visibility lazy first work, row/DONE entry, scalar
  visibility sources and independent VM/PD flags, not event-only wrappers.
  FTS distinguishes actual outer/nested/query/callback subjects without claiming
  optimizer-dependent audit counts. Dictionaries distinguishes genuine cold
  physical initialization from warm reuse across subject/label changes.
  Codecs distinguishes ordinary SQL execute initialization from actual direct
  text input/output, including nested array/string output, NULL suppression,
  renamed genuine helpers and hard policy denial before a soft parser error.
  CodecTransport retains the encoded-COPY pre-sink cases and exercises genuine
  finalized simple-query INSERT RETURNING stores, not SELECT portals. Actual
  installed hstore output and trusted Latin1 conversion pause at the existing
  WorkEncodingConvert boundary. The fixture activates the same admitted scope's
  runtime on the real operation context at the unlocked finalized-store metadata
  callback, leaving SQL binding's request inert. Peer relabel/descriptor changes
  must return the exact catalog-publication conflict before any row callback
  receives fields.
  The unchanged-authority counterpart delivers exact Latin1 bytes and SQL NULL.
  All legs retain the original observation predecessors and initialization owner,
  balance the subject phase, close the real store, join the worker and preserve
  INSERT commit/rollback behavior. Existing public Codecs and InsertReturning
  direct/prepared scenarios supply independent ordinary SQL/reference coverage;
  the internal publication race is not an active-provider PostgreSQL parity claim.
  Initialization distinguishes original preargument execute checks from actual
  strict/body entry, fresh prepared executions from row/VPC reuse, and genuine
  SQL/PL/default/domain/index/trigger/cursor attachment phases. Conditional NULL
  bodies do not preauthorize unexecuted helpers.
  The owner family is `TestSepgsqlSimulation(Policy|Labels|Subject|Demands|Routine|Support|NativeBodies|Codecs|CodecTransport|Conversions|Dictionaries|Audit|Publication|Controls|Cleanup|Retirement)`;
  Also select `TestOwnedCleanupRawPrefix`,
  `TestPreparePartitionConstraintDropDepthFirst`,
  `TestPartitionConstraintCapturedProgram`,
  `TestSepgsqlSimulationForeignFactory` and
  `TestSepgsqlAccessMethodFactory(Owners|Denied)` for focused actual-owner proof.
  `TestSepgsqlSimulationTimeFactory(Rescan)?`, `TestSepgsqlSimulationIncomingVPC` and
  `TestPgBuffercacheBorrowedSnapshot` cover the incoming factory and source owners;
  run their selected `RACE=1` variants alongside the corresponding legacy owners.
  `TestSepgsqlSimulationAuditDone(Concurrent|Detached)` covers operation-owned
  audit delivery: barrier-start concurrent completion with no provider, enforcing
  denial and permissive denial, plus a blocked callback that reenters Done while
  a competing Done and fresh session operation complete. Select it natively and
  with `RACE=1`, alongside `TestSessionTimeoutPending(Completed)?Lifecycle`.
  These are internal lifecycle/ownership units, not new SQL or active-provider
  PostgreSQL parity claims. The unchanged shared Audit and FeatureSmoke scenarios
  retain the independently specified ordinary and native-simulation SQL outcomes.
  `TestSepgsqlSimulation(Session|Initialization)Cleanup` in core covers actual
  simulation-session startup and production RequestEnd/WaitEnd cleanup, not a
  direct subject EndSession substitute. It verifies operation cancellation and
  drain, subject lifetime through unlocked terminal consumers, continuation after
  a cleanup error, once-only resource consumption, erased contexts/trusted state
  and failed initialization after subject attachment with no leaked reservation.
  Run `make check RUN='^TestSepgsqlSimulation(Session|Initialization)Cleanup$'`
  natively and with `RACE=1`; no active PostgreSQL SELinux oracle is claimed.
  Use `RACE=1` for Subject/Routine/Codecs/CodecTransport/Conversions/Dictionaries/Publication/Controls/Cleanup/Retirement
  with joined deterministic synchronization. Native controls prove admitted
  physical subject ownership, immutable labels, TEMP retention and atomic
  replacement independently of PG. ControlConsent rejects effective-unlabeled
  old/new transfers and peer label/role changes during real reader I/O.
  Demands distinguishes SQL ACL from the physical subject, count from real
  column reads, row locks from update demands, empty inheritance and FK-cascade
  cohorts. FeatureSmoke performs real installation, labels/denial, lexical/TX
  subjects, intagg helpers, VPC/materialized sources, cold dictionary denial,
  owned-child retirement, GUC/restorecon and portable-control operations.
  Handlers distinguishes sampling's factory from its count argument and returned
  row methods, including revoked SQL execute and restore-before-argument timing.
  ForeignFactory checks genuine registered handler selection before consuming
  the native method table, not a fabricated pg_proc call around every Go
  estimate/scan/modify/import/analyze callback. Detached sources retain selected
  immutable metadata, never an active evaluator/session/scope.
  `TestSepgsqlInitializationCompletion(Marks|Exhaustion)` independently checks
  monotonic typed receipt watermarks and rollback filtering; it does not replace
  genuine grant, source-owner, ordinary-call or closed-worker integration proofs.
  Genuine initialization ownership uses
  `RUN='^TestSepgsqlSimulationInitialization(Closed|Support|Parallel|VPC|Constructors|CursorDetach|Exception|Source)?$'`,
  also with `RACE=1`; preserve the existing inactive parallel and
  `TestPgFreespaceWrapperParameterDemand` regressions alongside these witnesses.
  Callbacks exercises actual hstore scalar/array grouping and UTF8→LATIN1
  conversion in both modes; native direct support/conversion entry is independent
  of SQL execute ACLs, skips unused empty/same/all-NULL work and unwinds on denial.
  Native cube dimension fingerprints do not manufacture a nonexistent hash routine.

### Common helpers

| Helper | Contract and use |
| --- | --- |
| `fixture.NewScenario(t) *fixture.Pair` | Entry point for new tests; honors `-reference`. Do not use legacy `fixture.NewPair(t)` (always starts Docker). |
| `fixture.NewFixture(t, withPostgres, nativeRaw)` | Low-level seam for ownership/provenance tests only. |
| `p.Step(t, s)` | Run and independently assert one step on every backend, then compare observations. |
| `p.Backends()` | Backend descriptors: `Name`, `DB`, `Recorder`, `PG`. Loop once with a shared callback. |
| `p.Ctx`, `p.Native`, `p.NativeConnector`, `p.Memory`, `p.Postgres` | Scenario context; unwrapped native control pool; native connector; wrapped pools (`p.Postgres` is nil in quick mode). |
| `fixture.Exec(query, count, args...)` | Exec success with exact `RowsAffected` (DDL usually 0); `LastInsertId` must be unsupported. |
| `fixture.Select(query, columns, types, rows, args...)` | Query success with exact column names, `DatabaseTypeName`s and normalized rows. Give metadata even for zero rows. |
| `fixture.Prepared(s)` | The same step via a fresh Prepare; does not prove held-statement reuse. |
| `fixture.Error(query, state, args...)` | Exec failure with exact SQLSTATE via `SQLState()`. |
| `fixture.ErrorDiagnostic(query, want, args...)`, `fixture.ImplicitError(...)` | Exec failure with a complete `fixture.Diagnostic` (`ImplicitError` fills both severities with `ERROR`). |
| `fixture.Position(n)` | Character (not byte) diagnostic position; 0 means absent. |
| `fixture.ControlsReferenceDiagnostic(code, message)` | Diagnostic with both severities `ERROR`. |
| `fixture.RunStep(t, ctx, executor, recorder, s)` | Run standard assertions on a chosen DB/Conn/Tx; does not compare backends. With `s.NonContext=true` it requires actual DB/Tx plain methods. |
| `fixture.HeldStatement{Executor, Statement, SQL}` | Run steps through an already prepared `*sql.Stmt` (leave `s.Prepared` false; close it yourself). The matching query uses the held statement; other queries use the underlying executor. |
| `fixture.State(t, err, want)` | Check SQLSTATE through the interface; empty `want` requires success. |
| `fixture.ExtractDiagnostic(t, err)`, `fixture.AssertDiagnostic(t, err, &want)` | Extract or assert complete structured diagnostics for custom calls. |
| `fixture.Normalize(value)` | `[]byte` → string, `time.Time` → UTC; applied automatically by steps. |
| `fixture.NoticeReferencePool(t, p, postgres, &records)` | Notice-enabled pool, pinned connection and recorder collecting complete notice diagnostics. |
| `fixture.SessionCancel(t, parent, conn, prepared, queryMode...)` | Observe PostgreSQL `pg_sleep` admission, cancel through the server, join and assert 57014. |
| `fixture.SchemaReferenceCleanup`, `fixture.RoleReferenceCleanup` | Register before the scenario (see above). |

More helpers (lock observers, active-cancel admission, role grants, COPY notices,
server fixture, driver configuration) are documented in the
[fixture README](../reference/fixture/README.md).

### Step fields

- Constructors fill `Query`, `Args`, `Prepared`, `SelectRows`, `Affected`, `State`,
  `Columns`, `Types` and `Rows`.
- `s.Bag = true` ignores row order but keeps duplicate counts; otherwise add
  `ORDER BY` when order matters. Never depend on traversal order.
- `s.CheckMetadata(t, rows)` checks raw column descriptors before iteration, even
  for empty results. `s.CheckScan(t, rows, i)` rescans the current row into
  specialized destinations and must not issue SQL.
- For Query errors use `fixture.ErrorDiagnostic` with `s.SelectRows = true`; set
  `s.ErrorStage` to `"prepare"` or `"execute"` when the stage matters.
- `s.PostgresDiagnostic` gives an independently specified PostgreSQL diagnostic
  where the oracle differs (for example a known oracle defect); both records are
  asserted in full.
- `fixture.Diagnostic` fields: `Severity`, `SeverityUnlocalized`, `Code`,
  `Message`, `Detail`, `Hint`, `Position`, `InternalPosition`, `InternalQuery`,
  `Context`, `Schema`, `Table`, `Column`, `DataType`, `Constraint`. Unset fields
  must be empty; source file/line/routine are never reference data.
- Normalized cells: integers `int64`, floats `float64`, booleans `bool`, NULL
  `nil`, byte payloads `string`. `ColumnType.ScanType` is not the scanned value.

## Self-contained example

Place an untagged file such as `reference/query_core/feature_answer_test.go` in an
existing package (new packages need their own `TestMain`):

```go
package reference_test

import (
    "testing"

    "stuntgres/reference/fixture"
)

func TestReferenceFeatureAnswer(t *testing.T) {
    p := fixture.NewScenario(t)
    for _, backend := range p.Backends() {
        t.Run(backend.Name, func(t *testing.T) {
            conn, err := backend.DB.Conn(p.Ctx)
            if err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() {
                if err := conn.Close(); err != nil {
                    t.Error(err)
                }
            })
            run := func(s fixture.Step) {
                fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
            }

            run(fixture.Exec("CREATE TABLE answer (v INTEGER)", 0))
            run(fixture.Exec("INSERT INTO answer VALUES (41), (NULL)", 2))
            for _, prepared := range []bool{false, true} {
                s := fixture.Select(
                    "SELECT v + $1::integer AS result FROM answer ORDER BY v NULLS LAST",
                    []string{"result"}, []string{"INT4"},
                    [][]any{{int64(42)}, {nil}}, int64(1),
                )
                if prepared {
                    s = fixture.Prepared(s)
                }
                run(s)
            }
            run(fixture.Error("SELECT absent FROM answer", "42703"))
        })
    }
}
```

Run `make check REFERENCE=1 RUN='^TestReferenceFeatureAnswer$'`, then
`make check RUN='^TestReferenceFeatureAnswer$'`.

## Allocation checks

For allocation budgets of package functions, use `util.AllocationsPerCall`
(`internal/util`): it reports allocations and bytes per call over a fixed batch
at `GOMAXPROCS(1)` and keeps the smallest of three samples, so unrelated runtime
allocations cannot fail an exact budget. Do not use `testing.Benchmark`: it
calibrates for a second per call.

Whole-command relative budgets must also match unrelated physical work.
Persistent-map copy costs depend on hashed key paths: updating different parent
rows or consuming different physical version sequences is not a matched control.
`TestForeignActionsExclusionCompletion` uses separate engines with identical
parent keys and two guarded insertions each, asserting equal row/version
frontiers before and after measurement. Immediate distinct guarded rows provide
the settled control; actual duplicate rows retain 32 deferred events on the
other side. Completion must preserve every retained event and the vector's exact
backing, independently of the unchanged one-allocation relative cap. The later
deferred-violation error and both observers' exact row-state assertions remain
independent witnesses; none of these assertions may replace the allocation cap.

## Package testing guides

Reference packages: [aggregates](../reference/aggregates/README.md#testing),
[alter](../reference/alter/README.md#testing),
[binary_json_arrays](../reference/binary_json_arrays/README.md#testing),
[catalog_objects](../reference/catalog_objects/README.md#testing),
[catalog_privileges](../reference/catalog_privileges/README.md#testing),
[catalogs](../reference/catalogs/README.md#testing),
[cursors](../reference/cursors/README.md#testing),
[datetime](../reference/datetime/README.md#testing),
[dml](../reference/dml/README.md#testing),
[domain_types](../reference/domain_types/README.md#testing),
[extension_cube](../reference/extension_cube/README.md#testing),
[extension_hstore](../reference/extension_hstore/README.md#testing),
[extension_ownership](../reference/extension_ownership/README.md#testing),
[extension_ownership_ltree](../reference/extension_ownership_ltree/README.md#testing),
[extension_types](../reference/extension_types/README.md#testing),
[extension_xml2](../reference/extension_xml2/README.md#testing),
[extensions](../reference/extensions/README.md#testing),
[fixture](../reference/fixture/README.md#testing),
[geometry_network](../reference/geometry_network/README.md#testing),
[indexes](../reference/indexes/README.md#testing),
[inheritance](../reference/inheritance/README.md#testing),
[information_schema](../reference/information_schema/README.md#testing),
[large_objects](../reference/large_objects/README.md#testing),
[locking_transactions](../reference/locking_transactions/README.md#testing),
[numeric](../reference/numeric/README.md#testing),
[object_address](../reference/object_address/README.md#testing),
[object_grants](../reference/object_grants/README.md#testing),
[operators](../reference/operators/README.md#testing),
[partitions](../reference/partitions/README.md#testing),
[patterns_encoding](../reference/patterns_encoding/README.md#testing),
[query_core](../reference/query_core/README.md#testing),
[query_order](../reference/query_order/README.md#testing),
[ranges_text_search](../reference/ranges_text_search/README.md#testing),
[roles](../reference/roles/README.md#testing),
[sequences](../reference/sequences/README.md#testing),
[session_state](../reference/session_state/README.md#testing),
[sessions](../reference/sessions/README.md#testing),
[strings](../reference/strings/README.md#testing),
[tables](../reference/tables/README.md#testing),
[temporal_types](../reference/temporal_types/README.md#testing),
[type_resolution](../reference/type_resolution/README.md#testing),
[types](../reference/types/README.md#testing),
[views](../reference/views/README.md#testing),
[xml_mapping](../reference/xml_mapping/README.md#testing).
`datetime_formatting` and `query_outer` have no package guide. Internal packages
document unit tests in their own READMEs (for example
[parser](../internal/parser/README.md#testing)).
