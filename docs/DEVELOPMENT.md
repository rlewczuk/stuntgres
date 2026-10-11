# Development and verification

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

```sh
make generate   # pinned parser and Unicode generation
make build      # compile production packages
make check RUN='^TestBinaryHashAllocations$' # selected tests; add REFERENCE=1 or RACE=1
make quickcheck # full Docker-free suite after edits, before review/integration
make racecheck  # same full Docker-free suite with race detection
make bench      # fresh engine + connector + pool + Ping + Close, five runs
make finalcheck # after merge: startup benchmark, full native race suite, dependencies
make deps       # list non-standard production dependencies
make refcheck   # human-only complete real PostgreSQL reference suite; NEVER run as an agent
```
Binary conversion is grounded in PostgreSQL **REL_18_0**, not local20devel:
the latter adds formats and changes legacy conversion source layout.
`internal/sqltype/pgencoding18/` contains release mapping inputs, attribution and
the deterministic offline generator; its static directional maps avoid runtime
initialization, host codecs and inverted reverse tables. Format algorithms follow
[release encode.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/adt/encode.c);
the standard-library-only charset provider consumes the release direct registry,
verifiers and specialized conversion algorithms. Preserve release licenses and
file-specific notices when regenerating data. The pinned18.0 public reference
goldens remain independent of these production tables. See the selected
[verification commands](TESTING.md).


Parser generation uses `internal/parser/generate.sh` through the existing
`go:generate` directive. The helper runs pinned `goyacc@v0.50.0` with the unchanged
`-l -p sql -o grammar_gen.go -v /dev/null grammar.y` flags in a unique temporary
directory beneath `internal/parser`, with a private copy of `grammar.y`.
It renames the completed `grammar_gen.go` over the checked-in output only after
successful generation. The temporary output is on the same filesystem, so this
publication is atomic: concurrent generators and builds cannot observe a partial
or interleaved parser. Failed generation leaves the previous parser intact;
normal exits and catchable termination signals clean up the temporary directory.
The original input/output basenames preserve the generated header and format.
Never hand-edit `grammar_gen.go`; continue to use `make generate`.

Grammar actions must never publish addresses of goyacc semantic cells (`&$n`)
into ASTs or retain them through helpers. Copy a value to an owned local before
taking its address, or publish an independently allocated node. Semantic cells
are overwritten during parsing and the entire receiver is cleared before its
scratch-pool return; AST ownership must not depend on parser lifetime.

SQL parsing, nested restricted INSERT replay and standalone type input use one
private `sync.Pool` of exclusively borrowed generated receivers. The
production-used `parseWithScratch` helper runs generated cleanup before a full
typed-zero reset; the checkout wrapper returns the receiver only after that reset,
including on errors, cancellation and propagated callback panics. Lexers, options,
contexts, notices and results are never pooled. Each receiver is fixed-sized;
expanded goyacc stacks remain invocation-local. Pool retention is GC-evictable,
not a hard object-count bound, and checkout does not limit active parses.

Measure changes against the current implementation with the identical
`BenchmarkParseScratchMixed` and `BenchmarkParseScratchNested` corpus, including
serial/parallel workloads and both default startup P and process-startup
`GOMAXPROCS=1`. Report allocation bytes/counts and runtime samples separately;
profiles must attribute all actual receiver allocation sites. Historical
fresh-parser profiles and a microbenchmark reduction do not establish a
whole-suite speedup. See [testing policy](TESTING.md) for required evidence.

Prepared transaction oracle scenarios use the existing pinned PostgreSQL18.0
UTF8/C fixture with startup `max_prepared_transactions=100`, preserving its
existing fsync option. Startup checks assert100. This is fixture capacity, not
a native settings API: native two-phase state is always enabled per Engine and
process-local. Every scenario cleans its own prepared GIDs before schema teardown.


The private copy isolates this generation's input after staging; atomic output
publication does not freeze other source files or the integration tip. Verification
must still compare its starting and ending integration tips and rerun on the
current tip if it advances; concurrent source changes are not made safe by the
generation helper.

Use `make check RUN='^TestName$'` for an individual test or small selected set;
add `REFERENCE=1` for real pinned PostgreSQL and `RACE=1` for race detection.
Every changed or new reference definition MUST pass the same selected definitions
in both modes before review; `finalcheck` cannot replace this proof.
Use `make quickcheck` only for whole-project verification after edits and before
review/integration, and `make racecheck` for the same Docker-free suite with race detection.
After merge, `make finalcheck` runs the five-run startup benchmark, full native
race suite and production dependency checks. It does not run real PostgreSQL.
`make refcheck` runs the complete real reference suite for humans only; agents
MUST NEVER invoke it. Commit any post-merge failure remedies separately.

Suite and selected-check targets regenerate parser and Unicode data; all targets keep output quiet.
The Makefile imposes no wall-clock deadline and disables Go's implicit test timeout
with `-timeout=0`. Agents MUST set an external command deadline of 900 seconds
for `check`, `quickcheck`, `racecheck` and `bench`, or 1800 seconds for `finalcheck`,
including generation and compilation. Slow or hanging tests must be fixed, not exempted.
Read `Makefile` for non-standard actions.

Every nonblank production dependency entry must be `stuntgres` or start with `stuntgres/`. `make deps` and the dependency phase of `make finalcheck` enumerate `. ./internal/...`, the production seams, rather than the whole reference tree. goyacc is a generation tool, not a runtime dependency. lib/pq, sqlmw and testcontainers are test-only: the importable `reference/fixture` is test support and MUST NOT be imported by production code. Benchmark startup target is below 1,000,000 ns/op; report observed allocations and timings, not a timing-sensitive test assertion.

Historical complete-suite baseline (2026-10-11): on Linux 7.0.0-30-generic x86_64,
AMD RYZEN AI MAX+ 395 (32 logical processors), Go 1.27.1 linux/amd64 and
Docker 29.1.3, the then-current corpus passed the ordinary native suite in
172.04 seconds, the native race suite in 399.58 seconds and the complete
pinned PostgreSQL18.0 UTF8/C reference race gate in 821.54 seconds
(reference package: 785.839 seconds). These generation/compilation-inclusive
wall times met the then-existing 900-second standalone limits; the historical
aggregate gate had a separate 3600-second allowance. These are measurement facts,
not current target behavior, speedup attribution or a post-integration finalcheck result.

Five fresh-engine Ping/Close startup samples measured 318313–473804 ns/op,
458535–458701 B/op and 2448–2449 allocs/op, each below the 1 ms target.
Tests used `-count=1`; compiler/module/image caches were not forced cold.
The startup benchmark briefly overlapped the ordinary gate; subsequent suite
gates were serialized. These single gate measurements and contended startup
samples are not isolated performance attribution. Current whole-project commands
from the repository root (agents apply the external deadlines above) are:

```sh
/usr/bin/time -p make quickcheck
/usr/bin/time -p make racecheck
/usr/bin/time -p make bench
make deps
```
Real PostgreSQL proof uses selected `make check RUN='...' REFERENCE=1` definitions;
agents must not repeat the historical complete real-reference gate.

`TestBinaryHashAllocations` keeps equal small/large allocation counts (at most ten,
zero for CRC) and measures bytes with `testing.Benchmark` in 1000-evaluation
batches. Inputs and plans are constructed outside measurement. Batching amortizes
process-wide runtime/race allocation noise that can dominate a twenty-call
`TotalAlloc` sample; it does not disable race coverage. The three-byte and 1 MiB
inputs retain the same budgets: at most 32 KiB allocated per evaluation and at
most 1024 additional bytes for the large input. A payload-sized scratch copy
therefore still fails even if its allocation count is constant. Run the focused
gate with `make check RUN='^TestBinaryHashAllocations$'`
and again with `RACE=1`, under the existing deadlines.

The public smoke tests live in `smoke/*_test.go` and are part of `make quickcheck`.
Use a concrete `make check RUN='...'` selector for focused proof, not a package-wide run.
The baseline `TestSmoke` verifies rows, metadata, affected counts,
Dump/Reset/Restore and a second already-open pool. Run the catalog witness with
`make check RUN='^TestCatalogOwnersPublicWitness$'`.
Go test reports each test's pass or failure instead of standalone output labels.

The explicitly enabled reference suite requires working Docker and image/module access. Each reference test package independently owns one PostgreSQL 18.0 container pinned to `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`, UTF8/C locale, serialized fixtures and public-schema resets. Real-mode targets use `-p 1` to serialize package processes and their container lifecycles. sqlmw records both direct and prepared paths; native memory handles remain available for controls. Fixtures assert independently specified outcomes in addition to parity. Reference startup failures are errors, not silent skips or mock fallbacks.

### Driver, session and catalog whole-root cutover

The final driver/session/catalog migration carries all 372 remaining root SQL
scenarios into the complete original/reference assertion union:651 untagged
shared definitions, including the incoming `TestReferenceAnyValueSourceACL`,
and44 separately selected companions (39 PostgreSQL-only and five
PostgreSQL-required differential scenarios).36 genuine PostgreSQL-only and all
five differential definitions remain tagged; three genuine PostgreSQL-only
definitions are untagged external-only callers. Earlier migrated families remain
in `reference/` too. No root SQL scenarios or obsolete SQL row/diagnostic helpers
remain. Root retains eight original nonSQL units, two transaction-terminal
mechanics, three session-adapter mechanics and `BenchmarkOpenDatabase`.
This is test relocation and assertion deduplication, not new production SQL.

Use the same external definition in both modes; for example:

```sh
make check RUN='^TestReference(DirectAndPreparedParameters|RowsMetadataAndCommandSemantics|QueryCoreCapturedOwnership|SettingsPublicTimeoutStages|SettingsPublicTimeoutRollbackCleanup)$'
make check REFERENCE=1 RUN='^TestReference(DirectAndPreparedParameters|RowsMetadataAndCommandSemantics|QueryCoreCapturedOwnership|SettingsPublicTimeoutStages|SettingsPublicTimeoutRollbackCleanup)$'
```

Domain guides give cohesive family selections, not an exact acceptance inventory.
The catalog living plan records the complete-name shared and companion registries
separately; do not select PostgreSQL-only legacy companions for a tagged
Docker-free proof. Default/explicit false shared scenarios use real stuntgres and
independent constants. Explicit true adds the pinned PostgreSQL18.0 backend to
the same bodies. No flag changes the expected native capability boundaries.

Raw metadata and cells are asserted before normalization, including empty
results. Native eager cursors and detached Columns slices remain distinct from
pq streaming cursors and aliased Columns slices. Drain and close a PostgreSQL
cursor before another command on its physical connection; do not infer native
snapshot guarantees for an unread PostgreSQL stream. Native controls use the
scenario's actual unwrapped Driver/connector/DSN, never middleware forwarding.
Native precanceled calls retain ordinary context errors and nil outputs;
PostgreSQL cancellation uses observed server admission, full57014 diagnostics,
joined workers and same-owner health witnesses rather than racing fast commands.

All131 original helper dispositions are accounted for. Original public methods
are preserved, not inferred from equivalent SQL: actual DB/Tx Exec/Query/Prepare,
Stmt Exec/Query, typed QueryRow consumers, native raw acquisitions and the SAME
held statements run beside fresh recorded Context phases. An original adapter
whose Query delegates to a physical Conn.QueryContext retains that actual Conn;
it is not a fictitious plain-DB alias. Quiet Prepare, original setup and every
per-case sentinel, metadata flag, specialized Scan, owned buffer and failure
cleanup remain producer-specific. Row helpers do not implicitly execute metadata
callbacks. Cohesive guide selectors are examples; acceptance uses the same exact
651-name RUN for native, real PostgreSQL and race, with39+5 companions separately.

Before review, run complete quickcheck and the exact selected native/real
PostgreSQL definitions; finalcheck does not replace those required proofs.
After integration, use `timeout --signal=KILL 1800s make finalcheck`.
The catalog utility does not run verification. Historical task-specific deadline
overrides do not change current target policy or SQL1ms timeout vectors.
Commands describe required checks, not claims of final review, integration or
post-integration finalcheck success.

### Dual-mode query reads and legacy transition

The query-read migration originally carried 67 SQL-interaction tests into 80
untagged definitions; the final scalar/query assertion union consolidates overlaps. Families
are `TestReferenceNullCount*`, `TestReferenceQueryAggregate*`,
`TestReferenceCountExpression*`, `TestReferenceQueryCore*`,
`TestReferenceQueryJoins*`, `TestReferenceQueryLimit*` and
`TestReferenceQueryOrder*`. These include cohesive boundary, consumer, metadata
and lifecycle splits, plus the existing reference-only complements that belong
to the migrated definitions. Select the complete query-read family in both modes:

```sh
make check RUN='^TestReference(NullCount[A-Za-z0-9_]*|QueryAggregate[A-Za-z0-9_]*|CountExpression[A-Za-z0-9_]*|QueryCore[A-Za-z0-9_]*|QueryJoins[A-Za-z0-9_]*|QueryLimit[A-Za-z0-9_]*|QueryOrder[A-Za-z0-9_]*)$'
make check REFERENCE=1 RUN='^TestReference(NullCount[A-Za-z0-9_]*|QueryAggregate[A-Za-z0-9_]*|CountExpression[A-Za-z0-9_]*|QueryCore[A-Za-z0-9_]*|QueryJoins[A-Za-z0-9_]*|QueryLimit[A-Za-z0-9_]*|QueryOrder[A-Za-z0-9_]*)$'
```

The original query projection/bags/metadata and ORDER BY position pilots are
included in the query-read family. The short-INSERT pilot,
`TestReferenceInsertImplicitShortDiagnostics`, remains available alongside it.
Seven original root BYTEA SQL-interaction tests are merged with the complete
legacy reference and pilot assertions in 17 untagged `TestReferenceBytea*`
definitions. Select every BYTEA definition in either mode:

```sh
make check RUN='^TestReferenceBytea[A-Za-z0-9_]*$'
make check REFERENCE=1 RUN='^TestReferenceBytea[A-Za-z0-9_]*$'
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
make check RUN='^TestReference(InsertSelect[A-Za-z0-9_]*|UpdateCore[A-Za-z0-9_]*|DeleteCore[A-Za-z0-9_]*|DeleteReturning[A-Za-z0-9_]*)$'
make check REFERENCE=1 RUN='^TestReference(InsertSelect[A-Za-z0-9_]*|UpdateCore[A-Za-z0-9_]*|DeleteCore[A-Za-z0-9_]*|DeleteReturning[A-Za-z0-9_]*)$'
```

Scalar-type migration merges all 63 assigned root interaction tests from 25 files
with existing reference assertions into 89 untagged definitions. The complete
family, including the retained tagged NAME physical/binary complement in true
mode, is selected with:

```sh
SCALAR='^TestReference(Character[A-Za-z0-9_]*|Float[A-Za-z0-9_]*|Name[A-Za-z0-9_]*|Numeric[A-Za-z0-9_]*|OID[A-Za-z0-9_]*|Smallint[A-Za-z0-9_]*|TypeCore[A-Za-z0-9_]*|UUID[A-Za-z0-9_]*)$'
make check RUN="$SCALAR"
make check REFERENCE=1 RUN="$SCALAR"
```

Raw cells, empty-result descriptors and owned middleware recordings are checked
before normalization. pq INT2/FLOAT4 ScanType is int16/float32, despite actual
int64/float64 cells; NUMERIC/UUID/NAME return owned bytes rather than native
strings. FLOAT4 rounding, negative NUMERIC scale metadata and NAME/OID typed-nil
parameter behavior have independent backend expectations. Native controls and
capability exclusions remain explicit phases; guarded PostgreSQL replay consumes
the unchanged owner-bearing stream. Actual held-statement reuse, concurrency,
cancellation, failure atomicity and retained cursor metadata remain covered.

The mixed OID/integer DISTINCT matrix checks all twelve BOOL database type names
and `bool` ScanTypes on the actual direct and prepared cursors, together with
raw values and owned consumed recordings. SELECT diagnostics retain their
original Exec routes alongside Query/no-cursor and Prepare checks: eight NUMERIC
cases, the SMALLINT Boolean comparison, three UUID type/WHERE cases, and direct
and prepared Exec for denied UUID SELECT. Held UUID Query denial and recovery
remain separate witnesses; SQL-equivalent Query coverage does not replace Exec.
Original single-row consumer methods also remain distinct: FLOAT direct
`QueryRow().Scan(float64,float64)`, OID same-statement `QueryRow().Scan(bool,bool)`,
SMALLINT connection `QueryRowContext` recovery and UUID same-statement
`QueryRowContext` denial/recovery. Non-Context calls cannot carry the fixture's
recorder key; separate recorded Context companions retain the same independent
values and owned-output/closure checks without replacing those original methods.

The identity family also runs as untagged `TestReferenceIdentity*` definitions,
merging all 28 assigned former root interaction tests with the complete existing
reference assertions. It covers declaration and option precedence, ALWAYS/BY
DEFAULT and overrides, source/parameter evaluation order, sequence ownership,
transaction/savepoint/session publication, own-sequence CHECK evaluation and
native reconstruction. Select the entire family in both modes:

```sh
make check RUN='^TestReferenceIdentity[A-Za-z0-9_]*$'
make check REFERENCE=1 RUN='^TestReferenceIdentity[A-Za-z0-9_]*$'
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

The untagged `TestReferenceAlterAddColumn*` family runs the same scenarios in
native and pinned PostgreSQL modes. Its `Review*` complement combines existing
TYPE transforms with NULL, constant and volatile ADD backfills; counts effective
volatile demands through repeatable-read savepoints and prior own writes; restores
marks before the first snapshot after sequential and equal-width shape changes;
and asserts complete declaration/collision diagnostics, Unicode character positions,
and explicit serial/identity and mixed SET/DROP DEFAULT-list capability boundaries.
Select this complement with:

```sh
make check RUN='^TestReferenceAlterAddColumnReview'
make check REFERENCE=1 RUN='^TestReferenceAlterAddColumnReview'
```

Engine package units separately exercise real provider admission before DEFAULT/
CHECK demand and unpublished candidate/backfill vetoes at provider validation and
StatementScope.Finish. They retain original descriptor/storage/private-write roots,
nontransactional sequence effects, authorization/replacement/storage fences, and
hold/admission retirement. These units use existing authorities, not public
SQL mocks or production test callbacks.

The persistent-view migration adds 25 untagged, mode-aware definitions carrying
the complete assertion union of 21 former root interaction tests and their owning
reference cases. Select the exact migrated inventory, excluding unrelated tagged
view oracles and replacement families:

```sh
VIEWS_RUN='^TestReferenceView(ProjectedBackingSortGroupAndConcurrency|WriteCapabilityBoundary|InternalCHARStaysProviderBounded|CanonicalLabels|IntegratedOIDNativeRoundTrip|IntegratedBetweenDefinition|AnalyzedLiteralPersistence|LiteralOccurrencesBeforeGrouping|TemporalEndpointAndTypedNullPersistence|ContextualScalarLiteralNativeRoundTrip|CatalogCHARLiteralReconstruction|JoinedDefinition|JoinedReplayAtomicityAndDynamicConsumers|JoinedNamespaceReplay|JoinedSourcesAndFrozenLayouts|JoinedCompactLayouts|JoinedRepeatedOccurrences|NativeNestedSecondDumpAfterTemporaryRemap|Rows|Barrier|SourcesAndCatalogLinks|CatalogMetadata|CatalogInputs|NestedDependencies|IdentityDependenciesAndRestore)$'
make check RUN="$VIEWS_RUN"
make check REFERENCE=1 RUN="$VIEWS_RUN"
make check RACE=1 RUN="$VIEWS_RUN"
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
invalid/canceled Restore atomicity. Both backends execute the ADD COLUMN/NATURAL-key
freeze complement. PostgreSQL independently executes consumer SQL and supports
the simple updatable-view write branch and internal `"char"` table branch;
it has no fictitious host Restore API.

The unrelated `ViewOracle`, `FrozenRelativeLiteralOracle`,
`ContextualDateStyleOracle`, `OriginalInvokerControls` and
`SequenceCatalogPrivateVisibility` definitions remain tagged. View replacement
now has untagged dual-mode scenarios and separately retained tagged companions.
Keep Docker-requiring legacy selections separate:

```sh
make check REFERENCE=1 RUN='^TestReferenceView(Oracle|FrozenRelativeLiteralOracle|ContextualDateStyleOracle|OriginalInvokerControls|SequenceCatalogPrivateVisibility)$'
```

After merge, run `timeout --signal=KILL 1800s make finalcheck` and commit any
failure remedies separately. Prior task-specific 300/900-second aggregate bounds
are historical migration gates, not current instructions.

### Dual-mode transactions, savepoints and locking

All 111 assigned original SQL tests in 38 root files are migrated/merged with
their owning reference assertions into 130 untagged external definitions.
Families are `TestReferenceLockModes*`, `TestReferenceRelationDeadlock*`,
`TestReferenceRelationLock*`, `TestReferenceRowLock*`, `TestReferenceSavepoints*`
and `TestReferenceTransaction*`. The row-lock family includes the complete
same-family `TestReferenceRowLockViewInsert` union. Test bodies are at most
150 lines and functional reference files at most 750 lines; meaningful phase
splits retain the original matrices, metadata, diagnostics and cleanup.

Use this bounded family selector rather than former root SQL test names:

```sh
LOCKS_RUN='^TestReference(LockModes|RelationDeadlock|RelationLock|RowLock|Savepoints|Transaction)[A-Za-z0-9_]*$'
make check RUN="$LOCKS_RUN"
make check REFERENCE=1 RUN="$LOCKS_RUN"
make check RACE=1 RUN="$LOCKS_RUN"
```

These are family selections, not an exact catalog inventory: the tagged
`make check REFERENCE=1` command additionally selects retained PostgreSQL-only controls
within these prefixes. ACL/privilege, inheritance, primary-key row-lock and role
lifetime controls remain tagged; the cross-family
`TestReferenceControlTransactionsDiagnostics` is now untagged and dual-mode,
outside this transaction-family selector.
Do not claim a tagged legacy selection is Docker-free merely because
`-reference=false` is supplied. In the ordinary binary, false/default executes
the real native definitions without Docker; true starts actual pinned PostgreSQL
and startup failure is visible and nonzero, even with a no-match RUN.

Common phases independently assert literal rows/counts, direct and genuinely
held prepared routes, diagnostics and owned raw recordings. INT4 ScanType is
native `int64` versus pq `int32`, with raw integer cells `int64` on both.
OID cells are independently native `int64` versus pq decimal bytes before
normalization. Native Dump/Reset/Restore and retained rows use their actual
connector/Driver/DSN; legacy notice callers retain their separate subtest DSNs.
Already-canceled pq control commands can return success before physical retirement;
their isolated retirement/reopen witnesses do not replace common SQL timer,
rollback-to-savepoint or server-signal recovery.

`TestReferenceTransactionRawBeginContextLifetime` is an explicitly native-only
capability phase with real raw Begin-context cancellation and successful
caller-managed rollback in both binary modes. Its independent PostgreSQL
server/direct+prepared cancellation, preserved PID and healthy rollback proof
lives once in `TestReferenceTransactionBeginContextCancellation`; pq watcher
timing is not a portable raw-context guarantee. Public native deadlines establish
conflict, not exact queue membership; exact native queue/reorder witnesses stay
in engine tests. PostgreSQL phases observe actual waits, PIDs and object identities.

Two focused root units contain no SQL:

```sh
make check RUN='^TestTransaction(TerminalReasons|RawReuseState)$'
```

Terminal reasons preserve distinct CleanClose, Terminated and Abandoned mechanics;
raw reuse preserves local active-state invariants. They are not substitutes for
the migrated public rollback/publication witnesses. After merge, run
`timeout --signal=KILL 1800s make finalcheck` for generation, startup benchmark,
full native race tests and dependencies. Historical task-specific deadline
overrides do not change current policy.

### Dual-mode CHECK constraint interactions

The 31 former root CHECK interaction tests are merged with their existing
reference assertions into 38 untagged definitions. Select the complete migrated
group without selecting untouched PostgreSQL-only ACL, system-column, unsupported
expression or opposite-direction lock witnesses:

```sh
RUN='^TestReferenceCheck(AlterCandidates|AlterCandidateTransactions|AlterTransactionalPublication|AlterOwnedSequenceCandidate|AlterOwnedSequenceAutoConstraint|Controls|Lifecycle|LikeAndConstraintCollisions|ExplicitTransactionAndSavepoint|DefinitionDiagnostics|Names|OutputAndUTF8Names|UnicodePosition|MixedConstraintDiagnostics|PreparedExecutionOutputEnvironment|Review2OrderReverseMixed|Review2OrderMultipleOwners|Review2OrderLogicalRoundtrip|Review2SchemaPrivateDropCancellation|Review2SchemaPrivateDropOutcomes|ReviewDependencyRestrictComplete|ReviewDependencyPrivateDeletionNonwaiting|ReviewDependencyRestrictReversedDeclaration|ReviewReplayOwnedSequence|ReviewReplayHistory|ReviewReplayHistoryPublicRestore|ReviewScalarCommentDML|ReviewScalarCommentDMLRoutes|ReviewScalarNonNullMissingOID|ReviewScalarPrivateCommentBoundary|ScalarFamilies|ScalarConstants|VolatileEffects|VolatileSequenceDependency|SequenceDropTransactionalCandidates|CorePreparedFrames|CoreAtomicity|CompleteRowAndAtomicity)$'
make check RUN="$RUN"
make check REFERENCE=1 RUN="$RUN"
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

Root CHECK diagnostic helpers have been removed after all callers migrated.
The shared mode-aware `fixture.CheckBackends` lives in `reference/fixture/`.

Table DDL, defaults and nullability now contribute **134 untagged definitions**,
merging the full assertion union of **120 former root tests in 44 files** with
their owning reference scenarios. Tests remain at most 150 lines; functional
reference files remain at most 750 lines. The complete migrated selection excludes
unrelated tagged inheritance, catalog, privilege and server-lock complements:

```sh
RUN='^TestReference(AlterDefault(BooleanGrammar|Boundaries|Controls|Deferred|DeferredControls|Diagnostics|Eligibility|EmptyDemand|Lifecycle|Ownership)|AlterDropColumn(Comments|Controls|DistinctOwnerRestoreBoundary|NativeDependencyCapabilityBoundary|Notices|ParserBoundaries|PreparedLifetime|QuotedExpressionDiagnostics|Shape|ShapePositions|TransactionalDDL|WaitCancellationAndCommittedInsert|ZeroColumnControls)|AlterNullability(ActionLists|AuthorizationAndPhase|AuthorizationLifetime|ControlEpoch|Controls|DependencyAtomicity|FailingActionNotice|FreshValidation|FreshValidationAfterDelete|HeldValidation|Lifecycle|Names|OwnDeletesAndSequences|PreparedMetadata|ReviewActionMatrix|ReviewCascadeNotice|ReviewCombinedPeer|ReviewDependencyTransactions|ReviewFirstRRSnapshot|ReviewGeneratedCollision|ReviewProjectedUpdate|ReviewRemainingDependency|ReviewRenameWholeInput|ReviewRetainedPositiveDML|ReviewSnapshotTimelines|ReviewSuccessiveDependencies|TransactionBoundary|Transactions|Validation|WholeInput)|AlterRename(Controls|ControlsOwner|Diagnostics|LockTimeout|Prepared|Syntax|TableTargets|TransactionBoundary|Values)|AlterSchema(CollisionsAndSyntax|GenericAndTemporaryBoundaries|NativeControls|QuotedCollision|RetainedMetadata|SearchPathAndViews|TransactionBoundary|ValuesAndHeldPlans|ViewsAndFailedRestore)|Default(ArithmeticControls|ArithmeticPlanningDecisive|ArithmeticPlanningFailures|ByteaControls|CastControls|CastDeferredInputControls|CastParameterProhibitions|CharacterCastControls|CharacterCastDeferredAssignment|DateCompoundControls|DeclarationPrecedence|DecoratedValuesBoundary|DestinationTypes|DiagnosticPrecedence|DiagnosticsAndConversionTiming|ExistingTableSkipNotice|ExpressionsAndDescriptorLifecycle|NativeControls|ParameterUtilityArgumentBoundary|PredicateControls|PredicateOuterSyntax|PredicateProhibitions|QueryPlanningAnalysis|QueryPlanningDecisive|QueryPlanningRequiredBranches|QuerySourceAtomicityAndCancellation|QuerySourceControls|QuerySourceDeferredFailure|QuerySourceHeldPrepared|RegressionBeforeFix|SchemaAtomicity|TemporalEndpointControls|TransactionFailureAndExplicitBypass|ValuesPlanningDecisive|ValuesPlanningRequiredBranches|ValuesPlanningSelectiveColumns)|InsertDefault(MixedParameters)|NotNull(AbsenceAnalysisPhases|AlterNoticesAndOwnership|ColumnLocalDeclarations|Controls|ControlsExternalIOHasNoTargetLock|CreateSystemTarget|CurrentCommandNamesAndCompatibleReuse|DeclarationsAndNames|DropSystemColumnDiagnostics|ExternalTransactionalDDL|FormsAndAlter|InheritanceCompleteInputAndNoEffects|LikeConstraintProcessingOrder|OwnerControls|PublicExcludedSyntaxAndQuotedDiagnostics|RestoreAtomicity|RetainedParameterizedStatements|SystemColumnSetAddBoundaries)|TableCore(AuthorizationAndOwnershipCleanup|Diagnostics|HeldCursorAndPreparedRebinding|NativeControlRoundTrip|OrderedLikeShapeAndDrop|QuotedDuplicateRelation|QuotedTypeNamesAndBoundaries|SemanticSkipNotices))$'
make check RUN="$RUN"
make check REFERENCE=1 RUN="$RUN"
```

The same selection with `make check RUN="$RUN"` is
Docker-free. Native controls retain eager rows and live
metadata until after DDL/Reset/Restore; PostgreSQL portals close before conflicting
DDL and consume the unchanged owner-bearing dump through genuine SQL phases.
Public NOWAIT queue probes and external-writer channels establish native ordering
without sleeps. Native physical nullability is known; pq reports it unknown.
BPCHAR cells are strings on both backends despite different ScanType descriptors.
An already-canceled native Conn execution is an ordinary Go error/no-effects
witness, not a portable pq simple-protocol promise; real server cancellation
remains independently covered by tagged PostgreSQL scenarios.

Fresh notice pools reuse the scenario's native connector/Driver/DSN. Shared
character/time metadata and PostgreSQL lock helpers live under `reference/`;
cross-package common helpers are exported by `reference/fixture`.
Obsolete root table/nullability helper hosts are removed. The DROP-system union
has one untagged owner, `TestReferenceNotNullDropSystemColumnDiagnostics`.

Arithmetic, bitwise and mathematical migration adds 81 untagged definitions from
61 former root interaction tests. Full original/reference assertion unions retain
all three 136-case shift grids, 170 bitwise value vectors, 126 nonintegral signature
diagnostics, integer overflow, radix inputs, cast and contextual diagnostics,
precision/scale, held parameter recovery, transactions and cancellation.
Select the whole family in both modes:

```sh
make check RUN='^TestReference(Arithmetic|Bitwise|Math)[A-Za-z0-9_]*$'
make check REFERENCE=1 RUN='^TestReference(Arithmetic|Bitwise|Math)[A-Za-z0-9_]*$'
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
For final aggregate proof after merge, use
`timeout --signal=KILL 1800s make finalcheck`; commit any failure remedies separately.
Earlier task-specific aggregate bounds remain historical evidence only.

The scalar-expression migration originally carried all62 assigned root tests
into93 untagged definitions across BETWEEN, explicit casts, null/truth/distinct
predicates, comparisons, conditionals, expression core and logical operators.
The final cutover merges duplicate scalar/query aspects into QueryCore owners.
The same definitions preserve complete
raw rows and metadata, empty results, full diagnostics and execution stages,
ordinary errors, LastInsertId, actual SAME-held reuse and concurrent frames.
Comparison frames consume serial 7/NULL/7 before eight concurrent calls on that
identical handle; expression-core frames consume 1/2/NULL (including the empty
result) before sixteen concurrent calls on that identical handle. Each concurrent
execution owns its recorder context. Native captured cursors,
Dump/Reset/Restore and rejected-restore atomicity use the unwrapped native pool;
PostgreSQL has independently specified real SQL consumer/state/cancellation
phases, not fabricated fixture controls. Select the complete scalar family:

```sh
make check RUN='^TestReference(Between[A-Za-z0-9_]*|CastExplicit[A-Za-z0-9_]*|CompareNull[A-Za-z0-9_]*|CompareOps[A-Za-z0-9_]*|Conditional[A-Za-z0-9_]*|ExpressionCore[A-Za-z0-9_]*|QueryCore[A-Za-z0-9_]*|Logical[A-Za-z0-9_]*)$'
make check REFERENCE=1 RUN='^TestReference(Between[A-Za-z0-9_]*|CastExplicit[A-Za-z0-9_]*|CompareNull[A-Za-z0-9_]*|CompareOps[A-Za-z0-9_]*|Conditional[A-Za-z0-9_]*|ExpressionCore[A-Za-z0-9_]*|QueryCore[A-Za-z0-9_]*|Logical[A-Za-z0-9_]*)$'
```

Metadata expectations distinguish computed targets from stored/identity columns:
computed Nullable remains unknown; stored native BOOL columns can have known
nullability. Native INT4 ScanType is `int64`, PostgreSQL's is `int32`, while raw
integer cells are `int64` on both. Conditional unbounded VARCHAR exposes native
Length `(MaxInt64,true)` versus actual lib/pq `(-5,true)`, not a normalized
common descriptor. Pinned PostgreSQL can plan an immutable parameter conversion
before CASE/COALESCE runtime demand and reject bad TEXT where native skips it;
the original good `"7"` held calls consume and assert row 7 on both backends.
Native `NULLIF(NULL,10/v)` demands division at v=0 and reports22012, whereas the
same pinned PostgreSQL query returns one SQL NULL. These are explicit backend
expectations, not a generic-plan setting or optimizer short-circuit promise.

The DATE, TIME and timestamp-without-time-zone families also migrate all 51
assigned root SQL-interaction definitions into untagged external scenarios.
They retain the full original/reference assertion union, including duplicate
input vectors, direct/prepared/actually-held routes, statement atomicity,
transaction aborts, precision notices, raw ownership, concurrency, cancellation
and metadata after EOF/Close. Shared row/diagnostic helpers now live only in
`reference/`; obsolete root hosts are gone, without production semantics changes.

The temporal route union includes one reused TIMESTAMP INSERT per backend
across all twelve fractional vectors with text and `time.Time` argument frames,
and another reused INSERT across both displacement limits. DATE civil bounds
run through both direct and retained INSERT routes with exact doubled rows.
The six original DATE literal failures run direct/prepared against the same
initially empty target, checking its empty metadata after each failure before
the populated-sentinel phase. Infinity ownership checks retain both stored
TIMESTAMP cells and the noisy typed-literal constant, mutating scanned bytes
after checked consumption/Close and checking an independent fresh read.

Temporal checks assert raw adapter values before normalization: native DATE and
TIME are strings, pq DATE/TIME are `time.Time`, finite TIMESTAMP values are
zero-offset `time.Time`, and DATE/TIMESTAMP infinities are owned pq byte payloads.
The DATE domain consumer handles infinity without weakening the shared raw
checker. Pinned pq reports temporal nullability as unknown, while native physical
columns retain their declared known nullability. A Go DATE bind at year-zero
February 29 has independently expected native February 29 BC versus pq March 1
BC, reflecting the pinned adapter's BC formatting rather than a changed codec.
Finite TIMESTAMP raw offsets are asserted before clock expectations are
canonicalized to UTC. pq may decode the same zero-offset civil value with an
unnamed fixed Location rather than `time.UTC`; comparisons retain the instant,
civil fields and precision, not Go Location pointer identity.

Native controls run on the same scenario's unwrapped public control pool as
the wrapped SQL reads. PostgreSQL phases consume the unchanged dumped SQL;
they do not pretend to provide stuntgres Reset/Restore APIs. Prepared and
transaction clocks use bounded strict clock-advance synchronization rather
than sleeps. Full diagnostics distinguish analysis from execution and encode
PostgreSQL setting-function and explicit-transaction DDL differences separately.
The DATE PostgreSQL timestamp-comparison/transaction-relative complements and
TIME optional-date completion stay tagged; whole migrated scenarios are not
skipped in either mode. TIMETZ/TIMESTAMPTZ and other unmigrated families keep
their existing coverage.

Complete temporal migration selection (true mode also discovers the three
retained tagged oracle complements):

```sh
RUN='^TestReference(DateDocumentedInputsAndCivilBounds|DateBindingRawMetadataAndNull|DateHeldBindingsAndCancellation|DateOrderingAndOperators|DateGregorianOrdering|DateDecodedCivilInputs|DateRepeatedSeparatorsContextualAndBound|DateDiagnosticsAndAtomicity|DateFullDiagnosticsAtomicityAndBoundaries|DateTimeParameterRangeDiagnostics|DateDecodeDiagnosticsAndModifierPhases|DateModifierDiagnostics|DateNotNullTransaction|DateTypedAssignmentDiagnostics|DateModifierConversionPrecedence|DateDumpRestoreAndPostgresReplay|DateRetainedCursorsAndCancellation|DateConcurrentPreparedOwnership|DateCanceledStagedRestorePreservesSnapshot|DatePostgresTimestampComparisons|DateTransactionRelativeInputs|TimeValuesParametersAndMetadata|TimeDeclarationsAndRetainedMetadata|TimeAcceptedInputsPrecisionAndOrdering|TimeHeldParametersAndOwnership|TimeDiagnosticsAndAtomicity|TimeDiagnosticBoundariesAndTransaction|TimeModifierDiagnostics|TimeNoticePhases|TimeNumericPrecision|TimeReviewClockPrecedence|TimeReviewAssignmentIdentity|TimeClocksAndAtomicity|TimeContextualPreparedClocksAndCancellation|TimePreparedCancellationAndArgumentCount|TimeDumpRestoreAndPostgresReplay|TimeRetainedCursorsAndCancellation|TimeOptionalDateCompletion|TimestampDocumentedInputs|TimestampHeldCorpusComparisons|TimestampPrecisionAndCivilParameters|TimestampCivilParameterOwnership|TimestampDeclarationsOccurrenceSizingAndOwnership|TimestampPublicDescriptionAndPreparedRelatives|TimestampSettingArgumentsUseScopeEnvironmentWithoutNewCasts|TimestampInvalidDeclarationNeverPublishesAndAbortsTransaction|TimestampQuotedUnicodeTypesAndKeywordIdentifiers|TimestampOrdinalBoundaryOrdering|TimestampBranchErrorsAbortTransaction|TimestampBranchCanonicalControlsAndComparison|TimestampNoisyNowPhysicalPrepare|TimestampTimeFractionConversion|TimestampSQLTransactionChainClock|TimestampClockAndPreparedLifetime|TimestampContextualPreparedConstants|TimestampCoreSpecialZoneAdjacency|TimestampPrecisionNoticePhase|TimestampOrderingAndNativeDump|TimestampRetainedNativeCursorAndRoundTrip|TimestampRestoreDiagnosticsAndCancellation|TimestampDiagnosticsAndAtomicity|TimestampEmptyDiagnosticsAndNotNullTransaction|TimestampDiagnosticModifiersAndInference|TimestampTimeParameterRangeDiagnostics|TimestampTimeDisplacementBounds|TimestampNewInputErrorsAbortTransaction|TimestampGenericNumericPrecisionExecution|TimestampPrecisionScannerDiagnostics)$'
make check RUN="$RUN"
make check REFERENCE=1 RUN="$RUN"
```

Their single definitions run the real public stuntgres `database/sql` adapter
against independently hardcoded expectations by default. The explicit boolean
test-binary flag adds real PostgreSQL; the build tag is not the mode selector.
Run selected definitions in both modes, for example:

```sh
make check RUN='^TestReferenceDateDocumentedInputsAndCivilBounds$'
make check REFERENCE=1 RUN='^TestReferenceDateDocumentedInputsAndCivilBounds$'
```

Absent/false quick mode does not contact Docker, fetch an image, open PostgreSQL
or read server state. Explicit true starts each selected package's pinned oracle
before test execution, even if selection matches no tests; startup/configuration errors
fail visibly, never skip or fall back. Reference test packages under
`./reference/...` accept this custom flag; `check` scopes `REFERENCE=1` to that
recursive package set and uses `-p 1` to serialize independent package processes,
startup and teardown. `make quickcheck` and `make racecheck` discover migrated
quick scenarios normally. Selected `make check RUN='...' REFERENCE=1` commands
include matching legacy tagged cases.

Selective real-mode checks use the stdlib-only
`reference/cmd/checkpackages` command to discover packages recursively through
`go list -json -tags reference ./reference/...`. It parses actual top-level test
declarations in the discovered `TestGoFiles` and `XTestGoFiles`, without
executing tests or `TestMain`. Ordinary `RUN` regexes select only packages with
matching top-level test names, including tagged companions. The final `go test`
still receives the unchanged selector and controls all test execution.

Selectors containing `/` conservatively select every test package rather than
approximate Go's slash-separated subtest grammar or dynamically registered
subtests. Invalid regexes fail instead of narrowing selection. If no package
matches, the fixture test package runs to preserve eager real startup and
visible startup/configuration failure despite no matching tests. Packages with
no test files, including the selector command itself, are excluded. Human-only
`refcheck` uses the same discovery command with `.*` to select every test
package; agents must not invoke it.

This reduces ordinary selective checks to matching package lifecycles while
retaining conservative complete selection for subtest patterns. Each selected
package eagerly starts its independent container; `-p 1` serializes startup,
tests and teardown. The at-most-30-package cap includes the fixture and selector
command and bounds the remaining conservative startup overhead. A
`go test -list` discovery pass would itself run package `TestMain` startup and
does not enumerate subtests, so it would not safely remove eager-startup cost.

After merge, `timeout --signal=KILL 1800s make finalcheck` runs generation,
startup benchmark, full native race tests and dependencies together, without Docker.
Do not substitute selected tests for this aggregate invocation; commit any failure
remedies separately. Original 300/900-second migration gates are historical evidence,
not current Makefile deadlines; SQL1ms timeout vectors are unchanged.

The tag retains untouched legacy PostgreSQL-only definitions. A tagged selection
restricted to mode-aware migrated scenarios with `-reference=false` (flag after
`-args`) is still Docker-free; selecting actual legacy cases lazily starts the
same package-owned singleton even when the runtime flag is absent/false. The tag alone
therefore does not make the whole legacy suite quick. Richer tagged
ordering/lock complements, the standalone PostgreSQL count column-ACL test and
DML rules/triggers, privilege and exact blocker/server-cancellation edges remain
tagged. Obsolete root query/comparison/cast helpers are removed; their complete
assertion union now lives in reference owners. Join product/alias/prepared/held-row
and physical view-provenance witnesses live in untagged reference scenarios;
`make check RUN='^TestReferenceQuery(OuterJoins|Joins)'`
and the same RUN with `make check REFERENCE=1` exercise the migrated assertion union.
Untagged migrated definitions use the mode-aware fixture; host Dump/Reset/Restore
and raw native cursor phases use its unwrapped native pool, with independently
specified SQL consumer phases on PostgreSQL.

### Dual-mode sequences and random

The sequence/random migration carries all 58 former root interaction definitions
and the prepared helper matrices into 132 untagged external definitions, merging
the complete root and reference assertion unions. Obsolete root sequence/random
helper hosts are removed after their callers migrated. Select the whole family
and its retained tagged oracle complements with the same anchored expression:

```sh
make check RUN='^TestReference(Sequence[A-Za-z0-9_]*|Random[A-Za-z0-9_]*)$'
make check REFERENCE=1 RUN='^TestReference(Sequence[A-Za-z0-9_]*|Random[A-Za-z0-9_]*)$'
```

Each selected backend independently checks rows, metadata, complete logical
diagnostics, direct/held-prepared routes and consumed middleware recordings.
Random values are checked against that backend's own reseed, range, lattice and
demand witnesses, never against the other backend's stream. Native unread
cursors and Dump/Reset/Restore use the fixture's unwrapped control pool; PostgreSQL
uses valid consume-first or peer-session phases and unchanged owner-bearing replay.
Native sequence `log_cnt=0` and PostgreSQL physical cache fields are distinct
expectations. The enormous-cache case executes native constant-space allocation;
PostgreSQL checks the huge declared cache and allocates only a bounded cache.
Server-only authority, actual namespace and lock-queue witnesses are guarded
PostgreSQL phases, not fabricated native success. The older SRX restart queue
complement remains tagged.

Arithmetic rows retain their original `sql.NullInt64` destinations, and frozen
REGCLASS cursors retain string/integer scans on the same current row: these
checks never re-execute volatile SQL. Restart scalars and generic sequence state
retain raw cells, metadata, exhaustion and recorded closure. Owner forms preserve
both the authority value `14` and a fresh uncalled sequence's first `1` across the full
transfer/link/no-op/denied-transfer chain, without a post-operation restart.
Random rows also retain the original string, float64 and int64 Scan destinations
alongside raw cells and recorded closure. Endpoint, grouped, transaction, frozen
control and sampled-numeric checks rescan the same current row without advancing
the cursor, consuming another draw or replacing backend-local seed witnesses.

Sequence scalars, actual held parameters and TEMP pairs/triples also retain
their int64 destinations. Complete state rows rescan int64/int64/bool, preserving
independent value/called expectations and backend-specific physical log checks.
Frozen movement rows retain int64/bool destinations on the original cursor.
Query versus Exec diagnostics, explicit prepared failures and direct transaction
observations stay on their original operations; no volatile statement is replayed
to inspect metadata or add a specialized Scan.

Raw REGCLASS and VOID cells are native strings versus pq byte slices; VOID's
nonnull empty result is not SQL NULL. BPCHAR cells are strings on both backends,
while native ScanType is string and pq ScanType is interface{}. Zero-argument
pq execution of parameterized SQL and cancellation on an already borrowed pq
connection differ from native analysis/cancellation: diagnostics use independent
backend expectations, and the PostgreSQL pre-canceled seed witness uses the
`database/sql` pool-borrow boundary. Native retains the original borrowed-Conn
cancellation and unchanged-stream assertion.


For example, these tagged legacy join diagnostics explicitly own a real pair.
This command requires Docker:

```sh
make check REFERENCE=1 RUN='^TestReferenceQueryJoins(Diagnostics|DiagnosticsOracle|ScopeDiagnostics|ScopeDiagnosticsOracle)$'
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
make check RUN='^TestReferenceQueryJoinsExportAndSchemaOwnership$'
make check REFERENCE=1 RUN='^TestReferenceQueryJoinsExportAndSchemaOwnership$'
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
exceptions. The sequence/random definitions use the explicit backend phases above
without changing the SQL surface or the pinned release oracle.


The shared fixture provisions native bootstrap owner `postgres` as a separate
NOLOGIN superuser for the container lifetime, with bounded startup and cleanup.
PostgreSQL callers still authenticate as `stuntgres`; native callers retain
`postgres`. Dump replay executes the unchanged owner-bearing stream, including
its first explicit owner directive, so schema/table/sequence owners are not
silently remapped. Pooled administrative session state is discarded before
schema resets. Sequence queue witnesses observe the captured local OID and
actual blocker, not a hard-coded relation name or sleep-based timing.

Reference tests live in feature-group external test packages under `reference/`
and use only the public stuntgres API and the importable shared
`reference/fixture` package. Shared setup and helpers belong in
`reference/fixture/`; functional groups contain whole test files of at most 750
lines each. Keep at most 30 packages total, including the fixture, and at most
1,000,000 bytes per package counting all files. Select affected fixtures with
`make check RUN='...' REFERENCE=1`; runs are uncached and quiet.
The cohesive common fixture is exempt from the 750-line file limit, not the
package byte cap; it keeps container lifecycle, diagnostic assertions, notice
connections and middleware recording together. Each test package calls
`os.Exit(fixture.Main(m))` from its `TestMain`, independently owns its fixture
state and container, and tears them down before its process exits. Packages do
not share a container or administrative session across processes.

Implicit INSERT and UPDATE diagnostic fixtures independently specify preparation versus execution failure stages, not just backend agreement. Native engine tests also use test-owned contexts to deterministically cancel after row staging and immediately before publication, checking complete sentinel preservation and subsequent engine usability without sleeps or production hooks.

### Dual-mode timezone and INTERVAL migration

The timezone/INTERVAL family merges 58 former root interaction tests with 48
existing reference definitions into 80 untagged scenarios (48 timezone, 32
INTERVAL), each at most 150 lines. Select the complete family in both modes:

```sh
make check RUN='^TestReference(Interval[A-Za-z0-9_]*|Timezone[A-Za-z0-9_]*)$'
make check REFERENCE=1 RUN='^TestReference(Interval[A-Za-z0-9_]*|Timezone[A-Za-z0-9_]*)$'
```

These preserve physical settings, captured defaults/views/cursors, DST and
offset boundaries, interval representation versus comparison, full diagnostics,
and native Dump/Reset/Restore. Raw native INTERVAL strings and pq byte slices
are asserted before normalization; temporal civil clocks and full offsets have
independent codec expectations. Transaction-clock cases use each selected
backend's own captured instant rather than querying PostgreSQL in quick mode.
Real server cancellation and schema-authority witnesses are guarded PostgreSQL
phases, not fake native success. Native controls use the unwrapped fixture pool.
Obsolete root timezone row/Exec helpers are removed after their callers migrated.
This migration's original outer300second aggregate deadline and later task-specific
900second override are historical evidence. Current external agent deadlines are
900 seconds for quickcheck and 1800 seconds for finalcheck.

Authoritative references: [PostgreSQL 18 commands](https://www.postgresql.org/docs/18/sql-commands.html), [SQLSTATE codes](https://www.postgresql.org/docs/18/errcodes-appendix.html), [testcontainers PostgreSQL API](https://golang.testcontainers.org/modules/postgres/), and [pinned sqlmw APIs](https://github.com/ngrok/sqlmw/blob/97c9c04efc79/interceptor.go). Local PostgreSQL source is 20devel and useful for implementation research; the pinned PostgreSQL 18.0 runtime is the compatibility oracle.
