# reference/strings

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### String core constraint deparse lifecycle

`TestReferenceStringCoreReview2Deparse` gives each of its 74 independent
constraint cases a fresh scenario and the unchanged 30-second fixture context.
Each case creates its table, checks default/false/true deparse modes through both
`pg_get_expr` and `pg_get_constraintdef`, then repeats the same direct/prepared
checks after actual native Dump/Reset/Restore. PostgreSQL retains its original
table for the repeated checks; native controls are not simulated there.
The BYTEA value/NULL checks remain in their owning cases. A separate scenario
retains all ordinary/special trim grouping pairs, direct/prepared routes and
owned `[]byte` Scan assertions. Cases run serially so fixture cleanup closes
pools and resets PostgreSQL `public` before the next case.

```sh
make check RUN='^TestReferenceStringCoreReview2Deparse$' REFERENCE=1
make check RUN='^TestReferenceStringCoreReview2Deparse$'
make check RUN='^TestReferenceStringCoreReview2Deparse$' RACE=1
```

Use an external 900-second deadline per command. These selected public-API
scenarios exercise the lifecycle remedy directly; no additional standalone
string smoke program is needed for this test-only change. Selected proof does
not replace whole-project Docker-free quickcheck or postmerge finalcheck.

### Text slicing selected proof

`TestReferenceStringSlice*` covers `left`, `right`, positional
`substring`/`substr`, and the existing POSIX/SIMILAR substring overloads.
Independent cases specify codepoint boundaries, negative end counts, omitted
counts, strict NULLs, text coercion, TEXT metadata, English diagnostics,
held-parameter recovery, query/stored consumers, authorization, statement
atomicity and native Dump/Reset/Restore. The cancellation scenario proves the
ordinary adapter boundary; `TestStringSliceCancellationAndAllocations`
separately exercises actual provider count/cut polling and allocation-free views.
`Metadata` checks the complete unconstrained-TEXT descriptor on populated,
NULL and empty computed/view results through direct, prepared and reused held
statements. `Controls` repeats the stored-view descriptor checks across retained
Rows and replacement. Known-to-end provider slices retain byte suffixes without
rescanning them; `TestStringSliceBoundedSuffixWork` protects those bounded paths.

```sh
make check RUN='^TestReferenceStringSlice' REFERENCE=1
make check RUN='^TestReferenceStringSlice'
make check RUN='^TestReferenceStringSliceFeatureSmoke$'
make check RUN='^TestReferenceStringSliceFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceStringSlice(Cancellation|Atomicity|Controls)$' RACE=1
```

Selected private-provider/parser/binder proof and affected existing regressions:

```sh
make check RUN='^(TestStringSlice|TestBinarySliceIntervals|TestBinarySliceStrictOwnersAndCancellation|TestBitStringFunctions)'
make check RUN='^TestReference(BinarySliceCapabilityBoundaries|SimilarReviewDefaultBoundaries|PosixDiagnostics|PosixCapturePartitions|PosixHeldParameters|PosixTypeCollationDemand|SimilarExtraction|SimilarExtractionCollation)$'
make check RUN='^TestReference(BinarySliceCapabilityBoundaries|SimilarReviewDefaultBoundaries|PosixDiagnostics|PosixCapturePartitions|PosixHeldParameters|PosixTypeCollationDemand|SimilarExtraction|SimilarExtractionCollation)$' REFERENCE=1
```

Use an external 900-second deadline per command. The same definitions run on
both backends without discovery filters. Pinned PostgreSQL18.0 returns the
original source for `right(source,-2147483648)` and empty TEXT for
`left(source,-2147483648)`, including ASCII, Unicode and empty-source witnesses.
Unknown two-argument `substring` selects POSIX; unknown three-argument calls
select SIMILAR. `Inference`, `ResolutionDiagnostics` and `Rendering` retain
the independently encoded pinned values, full English diagnostics/stages and
compact/pretty stored rendering. Excess SIMILAR separators raise2200C with
the SQL-function Context, not the malformed-POSIX-pattern2201B diagnostic.

The feature smoke independently expects `(Aé🙂, 🙂Z, ab, cde, o, oob)` with
six TEXT columns from new end/positional calls plus both matching overloads.
It also demands a `substr` DEFAULT yielding `bcd`, checks that a failed22011
UPDATE preserves `(1,bcd)`, and reuses a held positional statement with changed
integer parameters. Native controls have no invented PostgreSQL equivalent;
their emitted SQL is replayed on PostgreSQL in real mode.

### Identifier parsing selected proof

The nine `TestReferenceStringIdent*` definitions in `string_ident_values_test.go`,
`string_ident_diagnostics_test.go` and `string_ident_lifecycle_test.go` share the
existing scenario/recorder and independently specified PostgreSQL 18 outcomes.
They cover byte-oriented folding/quoting, strict suffix handling, NULL/coercion,
both formal-name orders (`str`, `strict`), complete diagnostics, direct/prepared/
held metadata, executable view deparse, atomicity and native control lifetime.

```sh
make check RUN='^TestReferenceStringIdent(Values|NullAndCoercion|NamedArguments|FramesAndMetadata|Diagnostics|BindingDiagnostics)$'
make check RUN='^TestReferenceStringIdent(Values|NullAndCoercion|NamedArguments|FramesAndMetadata|Diagnostics|BindingDiagnostics)$' REFERENCE=1
make check RUN='^TestReferenceStringIdent(Stored|Lifecycle|FeatureSmoke)$'
make check RUN='^TestReferenceStringIdent(Stored|Lifecycle|FeatureSmoke)$' REFERENCE=1
```

Use an external 900-second deadline per command. Both backends run without a
discovery filter. Backend child names permit `/postgres$` oracle selection, at
the cost of eagerly starting every reference package's oracle.

Private scanner/plan/binding/reconstruction proof:

```sh
make check RUN='^TestStringIdent(Parse|Diagnostics|Plan|CancellationWork|Binding|Reconstruction)$'
make check RUN='^(TestStringIdent(Reconstruction|CancellationWork)|TestReferenceStringIdent(FramesAndMetadata|Lifecycle))$' RACE=1
```

Feature smoke selects `make check RUN='^TestReferenceStringIdentFeatureSmoke$'`
and the same command with `REFERENCE=1`. Native precancellation and Dump/Reset/
Restore are explicit native-only phases; no PostgreSQL control API is invented.

The session-owned standalone smoke source is materialized outside the repository
and run from the worktree as `go run /ABS/OMP/SESSION/string-ident-smoke.go`.
Expected exit0 and empty stderr accompany exactly this stdout line:

```text
quoted={SomeSchema,sometable} suffix={a,b} null=true strict=22023 type=_TEXT restored={d,e}
```

This separately observes prepared execution, metadata, quoted/default and named
nonstrict results, typed NULL, strict22023, and native Dump/Reset/Restore. No
smoke executable or temporary discovery harness is committed.

### Array concatenation consumer

`TestReferenceStringCoreArrayConcat` replaces the obsolete missing-array-operator
boundary. It preserves both directions of text/`current_schemas(false)` concat,
their distinct TEXT[]/NAME[] results, PostgreSQL raw scan metadata, and all four
context/noncontext direct/prepared routes.

```sh
make check RUN='^TestReferenceStringCoreArrayConcat$'
make check RUN='^TestReferenceStringCoreArrayConcat$' REFERENCE=1
```

Use an external 900-second deadline for each selected command.

### String split selected proof

Untagged public `reference/strings/string_split_*_test.go` scenarios use the
existing fixture in both modes, with independently specified values, complete
English diagnostics, metadata, duplicate bags and actual Prepare/execute paths.
Run each bounded selector natively and with pinned PostgreSQL (`REFERENCE=1`):

```sh
make check RUN='^TestReferenceStringSplit(Scalar|Arrays|FramesAndMetadata|Diagnostics|Authorization)$'
make check REFERENCE=1 RUN='^TestReferenceStringSplit(Scalar|Arrays|FramesAndMetadata|Diagnostics|Authorization)$'
make check RUN='^TestReferenceStringSplit(TableSources|TargetExpansion|ClauseComposition|Locking)$'
make check REFERENCE=1 RUN='^TestReferenceStringSplit(TableSources|TargetExpansion|ClauseComposition|Locking)$'
make check RUN='^TestReferenceStringSplit(Stored|Lifecycle|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceStringSplit(Stored|Lifecycle|FeatureSmoke)$'
```

Each external command has a900-second deadline. Oracle-first expectations
include literal strict NULL wrapper folding to one NULL row, runtime strict
NULL wrappers preserving two fields, real sequence draws on final exhausted
attempts, mixed-depth zip/repetition and contextual ROWS identifier regressions.
Native precancellation supplements the shared SQL contracts; it does not claim
pq's fast-query precancellation behavior is identical.
Private native proofs select
`make check RUN='^TestStringSplit(Part|Array|Table|TableCancellationWork|Syntax|Expansion|Frames|Binding|Lifecycle|Preflight|PreflightSiblingError|CopiedFunction|RowMarks)$'`;
invocation/frame race proof uses
`make check RACE=1 RUN='^(TestStringSplit(Lifecycle|Frames|Preflight|CopiedFunction|Expansion)|TestReferenceStringSplit(Lifecycle|FramesAndMetadata|TargetExpansion|Locking))$'`.
These supplement rather than replace both public modes. Whole-project
Docker-free verification uses quickcheck.
Stored/Lifecycle retain scalar expression-index success and array
DEFAULT/CHECK/generated/view/storage/restore coverage. Real PostgreSQL also
creates the array expression index; native mode independently asserts the
existing complete `42704` TEXT[] btree-opclass boundary without aborting setup.
Catalog publication, dependency retirement and post-Restore absence use the
existing `pg_catalog.pg_class` relname projection with NAME metadata and an
independently expected zero-row absent bag; `to_regclass` is not an admitted
native function. Private Lifecycle distinguishes retained contextual view
analysis from invalid unresolved executable outputs; existing SetOps stored-view,
controls and restore proofs remain unchanged and must continue passing.
TargetExpansion reuses one held prepared plan across changing inputs after
strict-NULL simplification; ClauseComposition checks ORDER-demanded zip peers.
Its bounded `mixed_depth_effects`, `erased_wrapper_effects` and
`decreased_depth_peer_zip` cases assert complete emitted rows and subsequent
currval in direct and twice-reused held prepared executions. The mixed case
emits sequence values 1,2,4,5 and leaves currval6; both simplification cases emit
1,2 and leave currval3. Private Preflight additionally checks surviving local
topology, fixed slots, scalar ownership and immutable retained plans. Lifecycle
rejects invalid/foreign executable schemas and semantically invalid collation
usage while retaining the descriptor-free contextual-analysis success cases.
The same held-plan harness covers scalar-first and set-first source-argument
draws: one-field rows are (1,'2') and ('1',2), leaving currval3; two-field rows
are (1,'2'),(3,'x') and ('1',2),('x',3), leaving currval4. The empty-argument
control retains INT8/TEXT metadata with no rows and currval2. The upper-wrapper
control emits (2,'1') and leaves currval2. These are independent shared values,
not native-generated expectations. Private Expansion asserts scalar/source
error precedence, multirow/empty terminal attempts, two interleaved scalar/call
pairs and no argument reopening. Expansion/Lifecycle reject missing, duplicate,
out-of-range and unknown-kind programs; retained preflight/capture preserve the
template schedule and reconstruct only the surviving stage's events.
Preflight's surviving-group cases execute the actual private projection and
assert complete rows on every reuse. Its decreased-depth probe observes scalar,
outer-source and peer-source effects in SQL target order, including exhaustion
and no argument reopening; it does not equate execution order with the local
call-array or fixed-slot layout.
The effective-shallow-scalar witness keeps nextval inside a shallow SRF-bearing
TEXT target beside a deeper bare set: expected TEXT/TEXT rows are
('1x',a),('2x',b),('4y',c),('5y',d), with currval6. Wrapping the deepest set in
upper instead expects ('1x',A),('2x',B),('3y',C),('4y',D), with currval4.
Both definitions use direct and actual twice-reused held execution; their rows,
metadata and sequence states were captured against pinned PostgreSQL before the
F4 production remedy. Private Expansion observes the transformed shallow scalar's
multirow/empty terminal attempts and both scalar-first/set-first failure
frontiers through actual projection execution, without schedule-array assertions.
Private Frames executes mixed-depth projections rather than counting scalar
candidates. Lifecycle rejects invalid explicit scalar maps and wrong candidate
owners while retaining identity-layout capture/remap. Preflight's owner-boundary
test supplies typed lowered references directly: inactive/upper owners read
those expressions, while scheduled owners read their actual reserved results.
That boundary test does not claim a legal SQL optimizer fold exposes a bare call.
Stored/Lifecycle/FeatureSmoke also retain a real UNION ALL view with NULL and
string contextual outputs beside target SRFs, asserting TEXT/TEXT metadata and
the complete (NULL,a),(NULL,b),(kept,z) rows before and after native Restore.
Private template-analysis units retain legal NULL/string syntax and reject
nonliteral, other literal-kind and nonempty-usage contextual placeholders.
The Locking group uses the existing bounded public contention rendezvous. It
checks refreshed physical/function payloads, rescan ordinality and ROWS FROM
padding in direct/prepared commit/rollback reads, including later qualifying
fields, nested predecessor prefixes, zero-field LEFT JOINs and independent
volatile argument materialization. It does not assert ORDER sequence call
counts during replacement rechecks.
The public-API smoke source is an OMP session artifact, not repository or shared
infrastructure scaffolding. Root materializes it in an external temporary
directory and executes it from the worktree; no smoke executable is committed.

### Variadic string and quote proofs

Six-function contracts use the existing public fixture and independent pinned
PostgreSQL expectations; no Discovery/Observe definitions are verification.
Run each selector once natively and once with `REFERENCE=1`:

```sh
make check RUN='^TestReferenceStringVariadic(Values|Arrays|Output|Quoting)$'
make check RUN='^TestReferenceStringVariadic(Encoding|Metadata|Settings|FirstExecution)$'
make check RUN='^TestReferenceStringVariadic(Diagnostics|Parameters|Namespaces|Evaluation)$'
make check RUN='^TestReferenceStringVariadic(Consumers|Stored|Authorization|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceStringVariadic(DeparseRelations|BoundFormat|ViewWrites|MergeDefaults|Parameterized)$'
make check RUN='^TestReferenceSettingsBuiltinRegistry$'
make check RUN='^TestReferenceSettings(ShowAllPlaceholdersAndLifecycle|ShowAllCurrentOverrides)$'
make check RUN='^TestReferenceTimezoneBuiltinRegistryDescriptions$'
make check RUN='^TestReferenceFormatEncodingSettings$'
```

Supplemental private ownership units are selected by
`make check RUN='^TestStringVariadic(ArrayBorrowing|RetainedGeneration|BoundedCancellationAndSizes)$'`.
They prove borrowed array storage, owner-qualified parameterless retained scalar
generations, analysis-only root refresh versus unchanged stored captures,
equal-offset lowered view layers and local NEW checks, the exact stale-group →
temporal/analysis advance → demanded DEFAULT fold failure → unchanged quote
publication → changed-setting retry fence, actual parameterless MERGE origins,
fresh operation-local parameterized MERGE groups with unchanged retained stamps,
and bounded cancellation/size arithmetic. Selected race proof is
`make check RUN='^TestStringVariadicRetainedGeneration$' RACE=1`.
These units supplement rather than replace independent public PostgreSQL proof.
Registry snapshot ownership uses
`make check RUN='^TestSettingUtilitySnapshotOwnership$'`.
The exact timezone and encoding SHOW ALL rosters include an independently
expected quote_all_identifiers off/description row, including the PostgreSQL
pg_settings filter. String output tests retain real PostgreSQL successes for
nested internal CHAR-to-TEXT casts alongside native's independently asserted
`0A000` boundary; direct selected CHAR input and quote overload ambiguity remain
shared assertions. Settings inquiry tests cast REGCLASS explicitly to OID for
the pg_attrdef OID predicate, check live index quoting, and retain canonical Dump
access-method spelling independently of session policy.

The five review-policy definitions are feature smoke in both modes. They assert
absent/visible/shadowed schema identity and bound format off/on/off inquiries
without changing canonical Dump, layered view writes with full 44000 diagnostics
and atomic rejection, and actual MERGE default/root rows. Parameterized asserts
the pinned simple SELECT/MERGE custom1–5/generic6+ outputs and real PostgreSQL
plan counters, DISCARD and transaction/savepoint rollback retention, separately
from the approved native fresh-constants-on-every-parameterized-execution policy.
No fake native generic parity, counters/catalog or plan_cache_mode is added.
Both modes must pass their independently declared rows; PostgreSQL diagnostic
and row expectations are not weakened to accommodate the native boundary.
Changed bound-format provenance/replay also requires selected
`make check RUN='^TestReferenceFormat(Stored|Controls|Metadata|Collations)$'`
and the identical command with `REFERENCE=1`. Use `make quickcheck` only for
whole-project Docker-free proof after all edits; the root owns every check.
Bound ANY string-literal provenance and typed TEXT replay are supplemented by
`make check RUN='^TestFormat(ViewVariadicSerializationReplay|NodeIdentityAndRetainedGeneration)$'`.

The catalog-part standalone smoke source is supplied in the session handoff as
`local://string-variadic-smoke.go`. The root materializes it temporarily at
`.string-variadic-smoke/main.go`, runs `go run ./.string-variadic-smoke`, and
removes the temporary source/directory. Expected success is exactly
`string-variadic smoke: ok`, covering all six functions, direct/prepared routes,
parameterless first-Execute immutable quotes versus live parameters/format/concat, complete
diagnostics, metadata and real native Dump/Reset/Restore. Permanent public smoke:
`make check RUN='^TestReferenceStringVariadicFeatureSmoke$'`, followed by the
identical command with `REFERENCE=1`. These are proof commands and expected
observations, not claims of an exercised passing run.

### Integer-base formatting and TEXT MD5

The integer-base/TEXT-MD5 family asserts independent 32/64-bit two's-complement
vectors, RFC/UTF8 digest constants, unknown-TEXT versus known-BYTEA selection,
held parameter recovery and retained TEXT metadata, complete diagnostics,
consumer/write atomicity, symbolic stored widths and MD5 through native controls
with actual emitted SQL replay, and public cancellation. Run the complete
affected family in both modes before review, each under the 900-second deadline:

```sh
make check RUN='^TestReference(StringNumericBase|BinaryHash)'
make check REFERENCE=1 RUN='^TestReference(StringNumericBase|BinaryHash)'
make check RUN='^TestReferenceStringNumericBaseFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceStringNumericBaseFeatureSmoke$'
make check RUN='^Test(StringNumericBase|BinaryHash)'
make check RUN='^Test(StringNumericBase|BinaryHash)'
make check RUN='^TestStringNumericBase'
```

The smoke expects `fffffb2e` versus `fffffffffffffb2e` for INTEGER/BIGINT -1234,
`0` for zero, RFCabc/empty MD5, typed NULL, exact ambiguity/missing/input
diagnostics, no partial publication, and successful held-call recovery. Typed
nil `[]byte` inferred as TEXT intentionally retains native empty-input MD5 versus
pq NULL; do not erase this inherited adapter difference or use Go kinds for
signature selection. These selectors do not replace ordinary quickcheck or
post-merge finalcheck.

### String encoding

The string-encoding smoke executes all five functions with independently expected
rows/types, Unicode16 assigned/unassigned codepoints, exact conversion and escape
diagnostics, held valid/NULL/error/recovery frames and stored NAME/INT4 overloads.
Native phases exercise retained snapshots, Dump/Reset/Restore, failed Restore
atomicity and real PostgreSQL replay of actual emitted native SQL:

```sh
make check RUN='^TestReferenceStringEncodingFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceStringEncodingFeatureSmoke$'
make check RUN='^TestReferenceStringEncoding'
make check REFERENCE=1 RUN='^TestReferenceStringEncoding'
```

Each invocation has a900-second deadline. Smoke does not replace the complete
family, ordinary quickcheck or post-merge finalcheck. The offline Unicode generator
also emits assignment ranges from the existing vendored Unicode16 data:

```sh
go run ./internal/sqltype/unicode16/generate -source ./internal/sqltype/unicode16/data -output ./internal/sqltype/unicode16/tables_generated.go -case-output ./internal/sqltype/unicode16/case_tables_generated.go -assigned-output ./internal/sqltype/unicode16/assigned_tables_generated.go
```
