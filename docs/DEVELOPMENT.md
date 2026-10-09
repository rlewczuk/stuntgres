# Development and verification

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

```sh
make generate   # pinned goyacc; checked-in parser generation
make build      # compile production packages
make test       # uncached Docker-free tests, including smoke and dual-mode reference
make reference  # real pinned PostgreSQL differential fixtures
make smoke      # uncached public database/sql and controls smoke tests
make race       # Docker-free tests with the race detector
make test-all   # quick + full reference race suites under one aggregate deadline
make bench      # fresh engine + connector + pool + Ping + Close, five runs
make deps       # list non-standard production dependencies
```

Test targets enforce a fixed 900-second wall-clock deadline across all packages,
including compilation, plus a per-package timeout. `make test-all` chains ordinary
native/smoke/quick race tests and the full tagged reference race package under one
outer deadline; slow or hanging tests must be fixed, not exempted.
Output is quiet by default; use `VERBOSE=1` for test diagnostics, `RUN='^TestName$'`
for selection, and `PACKAGES=./internal/parser` to scope ordinary/race tests.
Read `Makefile` for non-standard actions. Only specialized, explicitly enabled
benchmarks may exceed the test budget; the default startup benchmark is bounded too.

Every nonblank production dependency entry must be `stuntgres` or start with `stuntgres/`. goyacc is a generation tool, not a runtime dependency. lib/pq, sqlmw and testcontainers are test-only. Benchmark startup target is below 1,000,000 ns/op; report observed allocations and timings, not a timing-sensitive test assertion.

The public smoke tests live in `smoke/*_test.go` and are also part of `make test`.
`make smoke` runs the entire package, uncached and quiet unless `VERBOSE=1`.
The baseline `TestSmoke` verifies rows, metadata, affected counts,
Dump/Reset/Restore and a second already-open pool. Run the catalog witness with
`make smoke RUN='^TestCatalogOwnersPublicWitness$'`.
Go test reports each test's pass or failure instead of standalone output labels.

The explicitly enabled reference suite requires working Docker and image/module access. It uses one PostgreSQL 18.0 container pinned to `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`, UTF8/C locale, serialized fixtures and public-schema resets. sqlmw records both direct and prepared paths; native memory handles remain available for controls. Fixtures assert independently specified outcomes in addition to parity. Reference startup failures are errors, not silent skips or mock fallbacks.

### Dual-mode query reads and legacy transition

The ordinary external `reference/` binary discovers 80 untagged query-read
definitions migrated/merged from 67 original SQL-interaction tests. The families
are `TestReferenceNullCount*`, `TestReferenceQueryAggregate*`,
`TestReferenceCountExpression*`, `TestReferenceQueryCore*`,
`TestReferenceQueryJoins*`, `TestReferenceQueryLimit*` and
`TestReferenceQueryOrder*`. These include cohesive boundary, consumer, metadata
and lifecycle splits, plus the existing reference-only complements that belong
to the migrated definitions. Select the complete query-read family in both modes:

```sh
make test PACKAGES=./reference RUN='^TestReference(NullCount[A-Za-z0-9_]*|QueryAggregate[A-Za-z0-9_]*|CountExpression[A-Za-z0-9_]*|QueryCore[A-Za-z0-9_]*|QueryJoins[A-Za-z0-9_]*|QueryLimit[A-Za-z0-9_]*|QueryOrder[A-Za-z0-9_]*)$' VERBOSE=0
make reference RUN='^TestReference(NullCount[A-Za-z0-9_]*|QueryAggregate[A-Za-z0-9_]*|CountExpression[A-Za-z0-9_]*|QueryCore[A-Za-z0-9_]*|QueryJoins[A-Za-z0-9_]*|QueryLimit[A-Za-z0-9_]*|QueryOrder[A-Za-z0-9_]*)$' VERBOSE=0
```

The original query projection/bags/metadata and ORDER BY position pilots are
included in the query-read family. The short-INSERT pilot,
`TestReferenceInsertImplicitShortDiagnostics`, remains available alongside it.
Seven original root BYTEA SQL-interaction tests are merged with the complete
legacy reference and pilot assertions in 17 untagged `TestReferenceBytea*`
definitions. Select every BYTEA definition in either mode:

```sh
make test PACKAGES=./reference RUN='^TestReferenceBytea[A-Za-z0-9_]*$' VERBOSE=0
make reference RUN='^TestReferenceBytea[A-Za-z0-9_]*$' VERBOSE=0
```

It also discovers the migrated INSERT SELECT, UPDATE and DELETE/RETURNING
families. Their 61 former root interaction tests are merged with existing
reference assertions and split into cohesive definitions of at most 150 lines.
Original raw `Scan(&any)` representations are checked before value normalization,
including native `int64` and string outputs, SQL NULL, owned BYTEA and nonnil empty
bytes. PostgreSQL codec expectations are independent: pq returns actual `int64`
integer cells even when INT4 ScanType is `int32`, and NUMERIC/UUID cells are byte
slices rather than native strings. Retained cursors obey the same raw contract;
normalized complete rows and owned middleware recordings remain checked afterward.
Select every resulting DML definition, including retained tagged oracle edges:

```sh
make test PACKAGES=./reference RUN='^TestReference(InsertSelect[A-Za-z0-9_]*|UpdateCore[A-Za-z0-9_]*|DeleteCore[A-Za-z0-9_]*|DeleteReturning[A-Za-z0-9_]*)$' VERBOSE=0
make reference RUN='^TestReference(InsertSelect[A-Za-z0-9_]*|UpdateCore[A-Za-z0-9_]*|DeleteCore[A-Za-z0-9_]*|DeleteReturning[A-Za-z0-9_]*)$' VERBOSE=0
```

The identity family also runs as untagged `TestReferenceIdentity*` definitions,
merging all 28 assigned former root interaction tests with the complete existing
reference assertions. It covers declaration and option precedence, ALWAYS/BY
DEFAULT and overrides, source/parameter evaluation order, sequence ownership,
transaction/savepoint/session publication, own-sequence CHECK evaluation and
native reconstruction. Select the entire family in both modes:

```sh
make test PACKAGES=./reference RUN='^TestReferenceIdentity[A-Za-z0-9_]*$' VERBOSE=0
make reference RUN='^TestReferenceIdentity[A-Za-z0-9_]*$' VERBOSE=0
```

The same definitions retain actual direct/prepared routes and reused held
statements. Native empty integral metadata has `int64` ScanType and known
Nullable; pq independently reports INT2/INT4/INT8 ScanTypes of
`int16`/`int32`/`int64` and unknown Nullable, while raw integer cells remain
`int64` on both. Unordered linked sources check generated IDs and
duplicate-preserving payload bags without inventing portable ID/payload coupling.
Dump/Reset/Restore and retained native cursor/statement phases use the unwrapped
pool; PostgreSQL independently consumes the unchanged owner-bearing dump stream.
The owner-collision restrictive-drop diagnostic preserves native unqualified
names and PostgreSQL schema-qualified names as complete separate records before
any normalization. Catalog, INSERT-only authority and lock-freshness witnesses
remain tagged; this is not a conversion of unrelated reference families.

The persistent-view migration adds 25 untagged, mode-aware definitions carrying
the complete assertion union of 21 former root interaction tests and their owning
reference cases. Select the exact migrated inventory, excluding unrelated tagged
view oracles and replacement families:

```sh
VIEWS_RUN='^TestReferenceView(ProjectedBackingSortGroupAndConcurrency|WriteCapabilityBoundary|InternalCHARStaysProviderBounded|CanonicalLabels|IntegratedOIDNativeRoundTrip|IntegratedBetweenDefinition|AnalyzedLiteralPersistence|LiteralOccurrencesBeforeGrouping|TemporalEndpointAndTypedNullPersistence|ContextualScalarLiteralNativeRoundTrip|CatalogCHARLiteralReconstruction|JoinedDefinition|JoinedReplayAtomicityAndDynamicConsumers|JoinedNamespaceReplay|JoinedSourcesAndFrozenLayouts|JoinedCompactLayouts|JoinedRepeatedOccurrences|NativeNestedSecondDumpAfterTemporaryRemap|Rows|Barrier|SourcesAndCatalogLinks|CatalogMetadata|CatalogInputs|NestedDependencies|IdentityDependenciesAndRestore)$'
make test PACKAGES=./reference RUN="$VIEWS_RUN" VERBOSE=0
make reference RUN="$VIEWS_RUN" VERBOSE=0
make race PACKAGES=./reference RUN="$VIEWS_RUN" VERBOSE=0
```

View rows assert raw OID `int64` cells on native and nonnil byte payloads on pq;
their normalized expectations remain native integers versus PostgreSQL decimal
strings. Finite PostgreSQL DATE cells remain `time.Time`; only exact `infinity`
and `-infinity` DATE payloads admit bytes. Native DATE/TIME values remain strings,
while PostgreSQL TIME uses `time.Time`, including the next-day 24:00 endpoint.
The nested replay probes preserve exact native INT4 ScanType `int64` and known
Nullable `(true,true)`, versus pq ScanType `int32` and unknown Nullable
`(false,false)`; actual raw INT4 cells are `int64` on both.

Real held statements, zero-width occurrences, typed catalog identity scans,
retained raw cells and eight joined concurrent consumers retain their metadata,
owned consumed recordings and closure assertions. Native-only controls cover
retained TEMP remapping, second-Dump topology, fresh-destination replay and
invalid/canceled Restore atomicity. PostgreSQL independently executes consumer
SQL, supports the simple updatable-view write branch and internal `"char"` table
branch, and alone runs the guarded ADD COLUMN/NATURAL-key freeze complement;
it has no fictitious host Restore API.

The unrelated `ViewOracle`, `FrozenRelativeLiteralOracle`,
`ContextualDateStyleOracle`, `OriginalInvokerControls` and
`SequenceCatalogPrivateVisibility` definitions remain tagged, as do view
replacement families. Keep their Docker-requiring selection separate:

```sh
make reference RUN='^TestReferenceView(Oracle|FrozenRelativeLiteralOracle|ContextualDateStyleOracle|OriginalInvokerControls|SequenceCatalogPrivateVisibility)$' VERBOSE=0
```

The view migration's aggregate verification uses an external task-specific
`timeout --signal=KILL 300s make test-all VERBOSE=0` bound for one full invocation;
this does not change the shared Makefile's 900-second policy.

### Dual-mode CHECK constraint interactions

The 31 former root CHECK interaction tests are merged with their existing
reference assertions into 38 untagged definitions. Select the complete migrated
group without selecting untouched PostgreSQL-only ACL, system-column, unsupported
expression or opposite-direction lock witnesses:

```sh
RUN='^TestReferenceCheck(AlterCandidates|AlterCandidateTransactions|AlterTransactionalPublication|AlterOwnedSequenceCandidate|AlterOwnedSequenceAutoConstraint|Controls|Lifecycle|LikeAndConstraintCollisions|ExplicitTransactionAndSavepoint|DefinitionDiagnostics|Names|OutputAndUTF8Names|UnicodePosition|MixedConstraintDiagnostics|PreparedExecutionOutputEnvironment|Review2OrderReverseMixed|Review2OrderMultipleOwners|Review2OrderLogicalRoundtrip|Review2SchemaPrivateDropCancellation|Review2SchemaPrivateDropOutcomes|ReviewDependencyRestrictComplete|ReviewDependencyPrivateDeletionNonwaiting|ReviewDependencyRestrictReversedDeclaration|ReviewReplayOwnedSequence|ReviewReplayHistory|ReviewReplayHistoryPublicRestore|ReviewScalarCommentDML|ReviewScalarCommentDMLRoutes|ReviewScalarNonNullMissingOID|ReviewScalarPrivateCommentBoundary|ScalarFamilies|ScalarConstants|VolatileEffects|VolatileSequenceDependency|SequenceDropTransactionalCandidates|CorePreparedFrames|CoreAtomicity|CompleteRowAndAtomicity)$'
make test PACKAGES=./reference RUN="$RUN" VERBOSE=0
make reference RUN="$RUN" VERBOSE=0
```

The same definitions assert completed-row/default inputs, TRUE/NULL/FALSE,
statement atomicity, exact diagnostics, dependency order and held prepared frames.
Raw native nullability and ScanType are checked before normalization. Native
Dump/Reset/Restore phases retain cursors, atomic failed replacement, owner-bearing
streams and repeated replay; PostgreSQL consumes unchanged replay SQL where
applicable. Native CREATE waits/cancels behind private sequence DROP without
publishing fragments; PostgreSQL CREATE can finish and retain an orphan after
DROP commits. Transactional COMMENT remains rejected natively and is a real
private-comment witness on PostgreSQL. These are independent backend outcomes,
not parity exceptions or fabricated native successes.

Root CHECK diagnostic helpers and tagged `referenceCheckBackends` remain for
unmigrated DEPARSE/VISIBILITY callers. They are not migrated SQL test bodies.

Table DDL, defaults and nullability now contribute **134 untagged definitions**,
merging the full assertion union of **120 former root tests in 44 files** with
their owning reference scenarios. Tests remain at most 150 lines; functional
reference files remain at most 750 lines. The complete migrated selection excludes
unrelated tagged inheritance, catalog, privilege and server-lock complements:

```sh
RUN='^TestReference(AlterDefault(BooleanGrammar|Boundaries|Controls|Deferred|DeferredControls|Diagnostics|Eligibility|EmptyDemand|Lifecycle|Ownership)|AlterDropColumn(Comments|Controls|DistinctOwnerRestoreBoundary|NativeDependencyCapabilityBoundary|Notices|ParserBoundaries|PreparedLifetime|QuotedExpressionDiagnostics|Shape|ShapePositions|TransactionalDDL|WaitCancellationAndCommittedInsert|ZeroColumnControls)|AlterNullability(ActionLists|AuthorizationAndPhase|AuthorizationLifetime|ControlEpoch|Controls|DependencyAtomicity|FailingActionNotice|FreshValidation|FreshValidationAfterDelete|HeldValidation|Lifecycle|Names|OwnDeletesAndSequences|PreparedMetadata|ReviewActionMatrix|ReviewCascadeNotice|ReviewCombinedPeer|ReviewDependencyTransactions|ReviewFirstRRSnapshot|ReviewGeneratedCollision|ReviewProjectedUpdate|ReviewRemainingDependency|ReviewRenameWholeInput|ReviewRetainedPositiveDML|ReviewSnapshotTimelines|ReviewSuccessiveDependencies|TransactionBoundary|Transactions|Validation|WholeInput)|AlterRename(Controls|ControlsOwner|Diagnostics|LockTimeout|Prepared|Syntax|TableTargets|TransactionBoundary|Values)|AlterSchema(CollisionsAndSyntax|GenericAndTemporaryBoundaries|NativeControls|QuotedCollision|RetainedMetadata|SearchPathAndViews|TransactionBoundary|ValuesAndHeldPlans|ViewsAndFailedRestore)|Default(ArithmeticControls|ArithmeticPlanningDecisive|ArithmeticPlanningFailures|ByteaControls|CastControls|CastDeferredInputControls|CastParameterProhibitions|CharacterCastControls|CharacterCastDeferredAssignment|DateCompoundControls|DeclarationPrecedence|DecoratedValuesBoundary|DestinationTypes|DiagnosticPrecedence|DiagnosticsAndConversionTiming|ExistingTableSkipNotice|ExpressionsAndDescriptorLifecycle|NativeControls|ParameterUtilityArgumentBoundary|PredicateControls|PredicateOuterSyntax|PredicateProhibitions|QueryPlanningAnalysis|QueryPlanningDecisive|QueryPlanningRequiredBranches|QuerySourceAtomicityAndCancellation|QuerySourceControls|QuerySourceDeferredFailure|QuerySourceHeldPrepared|RegressionBeforeFix|SchemaAtomicity|TemporalEndpointControls|TransactionFailureAndExplicitBypass|ValuesPlanningDecisive|ValuesPlanningRequiredBranches|ValuesPlanningSelectiveColumns)|InsertDefault(MixedParameters)|NotNull(AbsenceAnalysisPhases|AlterNoticesAndOwnership|ColumnLocalDeclarations|Controls|ControlsExternalIOHasNoTargetLock|CreateSystemTarget|CurrentCommandNamesAndCompatibleReuse|DeclarationsAndNames|DropSystemColumnDiagnostics|ExternalTransactionalDDL|FormsAndAlter|InheritanceCompleteInputAndNoEffects|LikeConstraintProcessingOrder|OwnerControls|PublicExcludedSyntaxAndQuotedDiagnostics|RestoreAtomicity|RetainedParameterizedStatements|SystemColumnSetAddBoundaries)|TableCore(AuthorizationAndOwnershipCleanup|Diagnostics|HeldCursorAndPreparedRebinding|NativeControlRoundTrip|OrderedLikeShapeAndDrop|QuotedDuplicateRelation|QuotedTypeNamesAndBoundaries|SemanticSkipNotices))$'
make test PACKAGES=./reference RUN="$RUN" VERBOSE=0
make reference RUN="$RUN" VERBOSE=0
```

The same selection with `go test -tags reference -run "$RUN" ./reference -args
-reference=false` is Docker-free. Native controls retain eager rows and live
metadata until after DDL/Reset/Restore; PostgreSQL portals close before conflicting
DDL and consume the unchanged owner-bearing dump through genuine SQL phases.
Public NOWAIT queue probes and external-writer channels establish native ordering
without sleeps. Native physical nullability is known; pq reports it unknown.
BPCHAR cells are strings on both backends despite different ScanType descriptors.
An already-canceled native Conn execution is an ordinary Go error/no-effects
witness, not a portable pq simple-protocol promise; real server cancellation
remains independently covered by tagged PostgreSQL scenarios.

Fresh notice pools reuse the scenario's native connector/Driver/DSN. Schema,
character/time metadata and PostgreSQL lock helper declarations are untagged
without converting their unrelated legacy scenarios. Root `tableCoreExec`,
`tableCoreMetadata`, `notNullDiagnostic` and `alterDropMetadata` remain helper-only
for unmigrated callers. The cross-family DROP-system assertion union also retains
its tagged legacy owner until the catalog's final driver/session deduplication.

Arithmetic, bitwise and mathematical migration adds 81 untagged definitions from
61 former root interaction tests. Full original/reference assertion unions retain
all three 136-case shift grids, 170 bitwise value vectors, 126 nonintegral signature
diagnostics, integer overflow, radix inputs, cast and contextual diagnostics,
precision/scale, held parameter recovery, transactions and cancellation.
Select the whole family in both modes:

```sh
make test PACKAGES=./reference RUN='^TestReference(Arithmetic|Bitwise|Math)[A-Za-z0-9_]*$' VERBOSE=0
make reference RUN='^TestReference(Arithmetic|Bitwise|Math)[A-Za-z0-9_]*$' VERBOSE=0
```

The tagged command also includes retained PostgreSQL-only family complements.
For Docker-unavailable explicit false, use the ordinary untagged reference binary
with the same selector. Native public driver cursors retain snapshots and repeated
cancellation errors across Dump/Reset/Restore; PostgreSQL replay consumes unchanged
owner-bearing streams only in selected server phases. Raw integral ScanType widths
and derived NUMERIC DecimalSize differ: native computed NUMERIC has unknown typmod,
whereas pq exposes unconstrained `(65535,65531,true)`. Both are independently
asserted before fixture normalization. Native-excluded scalar-input and no-FROM
spellings retain their errors alongside exact PostgreSQL results/diagnostics.
For this migration's stricter aggregate proof, use
`timeout --signal=KILL 300s make test-all VERBOSE=0`; the shared target's 900-second
deadline is unchanged.

Their single definitions run the real public stuntgres `database/sql` adapter
against independently hardcoded expectations by default. The explicit boolean
test-binary flag adds real PostgreSQL; the build tag is not the mode selector:

```sh
go test ./reference
go test ./reference -args -reference=false
go test ./reference -args -reference=true
go test -tags reference ./reference -args -reference=true
```

Absent/false quick mode does not contact Docker, fetch an image, open PostgreSQL
or read server state. Explicit true starts the pinned oracle before test execution,
even if selection matches no tests; startup/configuration errors fail visibly,
never skip or fall back. Only `./reference` accepts this custom flag: do not pass
it to unrelated package binaries. `make test` and `make race` discover migrated
quick scenarios normally. `make reference` selects true and includes every
legacy tagged case. `make test-all` first runs `go test -race ./...`, then
`go test -race -tags reference ./reference -args -reference=true`, with uncached
test flags and one shared 900-second wall-clock bound.

The tag retains untouched legacy PostgreSQL-only definitions. A tagged selection
restricted to mode-aware migrated scenarios with `-reference=false` (flag after
`-args`) is still Docker-free; selecting actual legacy cases lazily starts the
same singleton even when the runtime flag is absent/false. The tag alone
therefore does not make the whole legacy suite quick. Other unmigrated families,
richer tagged ordering/lock complements, the standalone PostgreSQL count
column-ACL test and DML rules/triggers, privilege and exact blocker/server-
cancellation edges remain tagged. Root `queryCoreRows`, `nullCountError` and
`queryJoinsPublicFixture` remain for unmigrated callers, not as SQL test bodies.
Untagged migrated definitions use the mode-aware fixture; host Dump/Reset/Restore
and raw native cursor phases use its unwrapped native pool, with independently
specified SQL consumer phases on PostgreSQL.

For example, these tagged legacy join diagnostics explicitly own a real pair
even with the migrated runtime mode disabled. This command requires Docker:

```sh
go test -tags reference -count=1 -run '^TestReferenceQueryJoins(Diagnostics|DiagnosticsOracle|ScopeDiagnostics|ScopeDiagnosticsOracle)$' ./reference -args -reference=false
```

Their shared SQL seed is reused without making migrated quick fixtures start
PostgreSQL. Keep this legacy selection separate from Docker-unavailable quick
mode checks.

Each selected backend independently asserts exact rows, column labels/type names
including zero-row results, NULLs, duplicate-preserving bags versus ordered rows,
direct/prepared parameters and affected counts. Diagnostics retain full English
logical fields, UTF8 character positions and Prepare-versus-Execute stages.
Backend differences are asserted before parity: native parameter analysis and
PostgreSQL simple-protocol zero-argument errors are distinct, and the original
native `count(1/0)` Prepare-or-Execute tolerance does not impose PG stage parity.
Raw null-count payloads/ScanType/sums, LIMIT precision/length/nullability,
zero-column occurrence counts and merged-view typmods are checked before
normalization. Held statements are actually reused; sqlmw checks consumed
outputs and closure. Joined BYTEA scans retain independent owned bytes on both
backends; detached `Rows.Columns` slices are a native guarantee, not a lib/pq
copy promise. Native unwrapped Dump/Reset/Restore phases retain snapshots and
pristine restored sequence state; actual PostgreSQL identities/waiters prove
the PG-only lock/refill complements. No unsupported native success is invented.

The short-INSERT pilot retains its independently specified unnamed-portal
parameter Context and failed-INSERT sentinel checks. BYTEA checks complete
literal and inference diagnostics (the inference error has no Context), raw
ScanType, physical/computed Nullable and known/unknown Length metadata, NULL
versus nonnull empty values, and owned recorded arguments/consumed buffers
before normalization. Literal lexical NUL/invalid-UTF8 errors are distinct from
permitted raw BYTEA octet binds. Native Dump/Reset/Restore, retained snapshots
and deterministic cancellation have explicit unwrapped native phases. The same
public raw connection survives successful catalog replacement, retained-row
mutation and failed restores, then opens a new cursor before synchronous
cancel-before-Next. Selected native and PostgreSQL backends independently check
the ordinary SQL consumers.
The focused byte-copy regression and existing row comparator remain unchanged.

For a single migrated lifecycle definition, use the same exact name in both modes:

```sh
make test PACKAGES=./reference RUN='^TestReferenceQueryJoinsExportAndSchemaOwnership$' VERBOSE=0
make reference RUN='^TestReferenceQueryJoinsExportAndSchemaOwnership$' VERBOSE=0
```

Mode-aware pools use a fresh native driver per fixture; wrapped and native control
pools share that driver/DSN without exposing controls through middleware. Cleanup
closes rows before statements and all pools before schema reset. PostgreSQL
lifecycle, owner provisioning and administrative-session safeguards below remain
shared by eager migrated and lazy legacy startup.
Failed eager or lazy startup releases any acquired administrative pool/container
before returning the cached error, with bounded termination and visible cleanup
errors. Legacy administrative probes require successful startup; registered
role/schema cleanup runs only after initialization succeeds.

Some researched cases cannot be forced into identical static row snapshots:

| Retained case | Concrete fit boundary |
| --- | --- |
| Physical sequence catalog/cache probes | Server `log_cnt=22` versus native logical `log_cnt=0` would assert nonexistent native WAL/cache durability. |
| Lock/blocker probes | OID/PID/`pg_locks`/`pg_blocking_pids` relationships require locally captured server identities and bounded concurrency, not portable fixed rows. |
| Random draws | Independent range/reseed witnesses fit selected backend scenarios; identical random streams are not a portable expectation. |
| Native Dump followed by PG replay | Requires an unwrapped native control phase and a real PostgreSQL replay phase, not a query-only snapshot. |
| PostgreSQL-only `INHERITS` | Outside the bounded native dialect; retain the real server assertion without inventing native success. |

These are concrete boundaries of static snapshot comparison, not blanket
exceptions to future shared scenarios. No extra migration or engine/oracle
surface change is implied.


The shared fixture provisions native bootstrap owner `postgres` as a separate
NOLOGIN superuser for the container lifetime, with bounded startup and cleanup.
PostgreSQL callers still authenticate as `stuntgres`; native callers retain
`postgres`. Dump replay executes the unchanged owner-bearing stream, including
its first explicit owner directive, so schema/table/sequence owners are not
silently remapped. Pooled administrative session state is discarded before
schema resets. Sequence queue witnesses observe the captured local OID and
actual blocker, not a hard-coded relation name or sleep-based timing.

Reference tests live in the dedicated `reference/` external test package and use only the public stuntgres API. Shared setup and helpers belong in `reference/fixture_test.go`; functional groups belong in separate test files of at most 750 lines each. `make reference` runs these fixtures uncached, with `VERBOSE=1` available for diagnostics.
The cohesive common fixture is exempt from the 750-line limit; it keeps container lifecycle, diagnostic assertions, notice connections and middleware recording together.

Implicit INSERT and UPDATE diagnostic fixtures independently specify preparation versus execution failure stages, not just backend agreement. Native engine tests also use test-owned contexts to deterministically cancel after row staging and immediately before publication, checking complete sentinel preservation and subsequent engine usability without sleeps or production hooks.

### Dual-mode timezone and INTERVAL migration

The timezone/INTERVAL family merges 58 former root interaction tests with 48
existing reference definitions into 80 untagged scenarios (48 timezone, 32
INTERVAL), each at most 150 lines. Select the complete family in both modes:

```sh
make test PACKAGES=./reference RUN='^TestReference(Interval[A-Za-z0-9_]*|Timezone[A-Za-z0-9_]*)$' VERBOSE=0
make reference RUN='^TestReference(Interval[A-Za-z0-9_]*|Timezone[A-Za-z0-9_]*)$' VERBOSE=0
```

These preserve physical settings, captured defaults/views/cursors, DST and
offset boundaries, interval representation versus comparison, full diagnostics,
and native Dump/Reset/Restore. Raw native INTERVAL strings and pq byte slices
are asserted before normalization; temporal civil clocks and full offsets have
independent codec expectations. Transaction-clock cases use each selected
backend's own captured instant rather than querying PostgreSQL in quick mode.
Real server cancellation and schema-authority witnesses are guarded PostgreSQL
phases, not fake native success. Native controls use the unwrapped fixture pool.
Root `timezoneRows`/`timezoneExec` remain only for unmigrated temporal callers.
This migration's aggregate verification uses an outer 300-second deadline;
the shared Makefile's documented 900-second deadline is unchanged.

Authoritative references: [PostgreSQL 18 commands](https://www.postgresql.org/docs/18/sql-commands.html), [SQLSTATE codes](https://www.postgresql.org/docs/18/errcodes-appendix.html), [testcontainers PostgreSQL API](https://golang.testcontainers.org/modules/postgres/), and [pinned sqlmw APIs](https://github.com/ngrok/sqlmw/blob/97c9c04efc79/interceptor.go). Local PostgreSQL source is 20devel and useful for implementation research; the pinned PostgreSQL 18.0 runtime is the compatibility oracle.
