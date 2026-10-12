# reference/datetime

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Datetime truncation

The datetime-truncation family independently asserts canonical fields/aliases,
calendar and BC boundaries, DATE/TIME selection, unrestricted metadata,
interval component preservation, strict NULL, complete diagnostics and held
recovery. Zone witnesses cover New York folds/spring, Sao Paulo midnight gaps,
Lord Howe half-hour transitions, explicit Sydney, POSIX/fixed/dynamic zones.
Configured-abbreviation direct/prepared/held witnesses cross Default/Australia
under UTC/New York: session EST retains05UTC, while Australia UTC EST is14UTC
and CST is14:30UTC on the preceding day. The captured execution environment,
not Prepare-time settings or a global Default table, selects the resolver.
Consumer atomicity, savepoint recovery, stored definitions, compatible Retag,
native destination replacement and actual emitted SQL replay share the bodies.
Fast precanceled INSERT is native-only: lib/pq may finish it before cancellation;
real session cancellation instead uses the fixture's observed admission barrier.
The excluded three-argument naive inputs retain separately asserted native0A000
and PostgreSQL implicit-coercion success, not parity-derived goldens.

Run changed definitions in both modes, each under900seconds:
```sh
make check RUN='^TestReferenceDatetimeTrunc'
make check REFERENCE=1 RUN='^TestReferenceDatetimeTrunc'
make check RUN='^TestReferenceDatetimeTruncFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeTruncFeatureSmoke$'
make check RUN='^TestDatetimeTrunc'
make check RUN='^TestDatetimeTrunc'
make check RUN='^TestDatetimeTrunc'
```
Smoke expects distinct NY fold hours05/06UTC, spring day05UTC, Sydney day13UTC,
held NULL/error recovery, unchanged late-write sentinel and symbolic stored
default/view/generated/index replay. Full family checks additionally cover
week2006-01-01→2005-12-26, quarter2001-02-16→2001-01-01, negative interval
seconds→-1second and TIME24:00→zero-day24hour INTERVAL. These selected gates
do not replace ordinary quickcheck or post-merge finalcheck.

### Date binning

The date-bin family uses independently specified shared direct/prepared bodies
for both timestamp variants: shifted-origin boundaries, fractional bins,
negative exact multiples, fixed-day DST behavior, mixed/date/unknown resolution,
held session coercion and NULL/import recovery. Complete logical diagnostic
records include exact stage/character Position and empty runtime optional fields.
Overflow witnesses cover the release subtraction/day-product/lower-bound vectors,
with package units distinguishing checked stride addition, component cancellation,
int64 correction underflow and retained END. Metadata asserts unrestricted
computed types and precision-2 inputs yielding an unrounded .119 result.
Stored/default/CHECK/view/generated/index consumers include actual emitted SQL
replay, fresh restored writes, compatible remapping and retained native rows/types.

Run selected proofs with an external900-second deadline per command:
```sh
make check RUN='^TestDatetimeBin'
make check RUN='^TestReferenceDatetimeBin'
make check REFERENCE=1 RUN='^TestReferenceDatetimeBin'
make check RUN='^TestReferenceDatetimeBinFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeBinFeatureSmoke$'
```
The smoke independently expects before-origin23:47:30, first-bin00:02:30,
next-bin00:17:30, negative-exact15:00, typed metadata, full22008 zero-stride
diagnostics, NULL/error/good recovery, failed-later-write preservation and actual
stored control replay. A root-owned ephemeral public Go smoke additionally runs
`timeout 900s go run ./.date-bin-feature-smoke`; its source uses only stdlib/public
stuntgres, and the directory is removed before review. Selected gates do not
replace whole-project quickcheck or post-merge finalcheck.

### Transaction time

The transaction-time family uses the same completed public body for all five
providers: direct/held metadata and raw codecs, transaction/statement ownership,
peer/autocommit/CHAIN contexts, demanded wall variation, TimeZone and fixed
timeofday layout, bounded defaults/consumers, complete invalid-call diagnostics,
symbolic replay and native controls. Non-ISO DateStyle witnesses use TEXT-only
timeofday and explicit timestamp casts; raw pq temporal codecs are checked in ISO.
Shared prepared/parameterized tests allow now <= statement time; native one-command
equality and fixed microsecond/formatter outcomes have package-owned units.
The full legacy TestTimestampSQLTransactionChainSmoke union remains in
TestReferenceTimestampSQLTransactionChainClock; the superseded smoke file is removed.

Each selective invocation has its own 900-second deadline:
```sh
make check RUN='^TestReference(DatetimeTxTimes|TimestampSQLTransactionChainClock)'
make check REFERENCE=1 RUN='^TestReference(DatetimeTxTimes|TimestampSQLTransactionChainClock)'
make check RUN='^TestReferenceDatetimeTxTimesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeTxTimesFeatureSmoke$'
make check RUN='^TestTransactionTime'
```

### Timezone and calendar functions

The timezone/calendar function family independently covers all nine timezone
and four date_add/date_subtract signatures, operator counterparts, inference,
strict demand, metadata, DST/calendar stages, released displacement extrema,
stored definitions and native control atomicity. Complete original timezone
smoke assertions now live in `TestReferenceDatetimeZoneMigratedTimezoneSmoke`;
existing `TestReferenceTimezone*` fixtures remain intact. Selected live
cancellation uses a finite row-dependent date_add INSERT SELECT, gated by the
actual PostgreSQL query/PID and the native provider/worker synchronization
pattern. Each direct/prepared subcase cleans its fixtures even on failure.

```sh
make check RUN='^TestReference(DatetimeZone|Timezone)'
make check REFERENCE=1 RUN='^TestReference(DatetimeZone|Timezone)'
make check RUN='^TestReferenceDatetimeZoneFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeZoneFeatureSmoke$'
make check RUN='^TestDatetimeZone'
make check RUN='^TestDatetimeZone'
make check RUN='^TestDatetimeZone'
```

Both complete reference modes must run before review/integration. Each selected check
has a900-second deadline. Provider units include warmed EST/MSK/IANA allocation
witnesses; they supplement, not replace, public independent SQL expectations.
The discovery backend.PG filter and focused logging-only oracle probes are
temporary and must be removed before native checks; neither remains in the
completed family. The root runs verification and feature smoke after edits, then
the final post-merge finalcheck under one1800-second deadline.

### Interval justification

The interval-justification family covers exact30-day/24-hour boundaries,
mixed signs and component extrema, complete diagnostics and phases, metadata,
held inferred parameters/recovery, four styles, existing consumers, statement
atomicity and native Dump/Restore with actual emitted SQL replay. Precanceled
ordinary context errors are a native-only API boundary, not a race-prone fast pq
request assertion. Run the same complete definitions in both modes:

```sh
make check RUN='^TestReferenceDatetimeJustify'
make check REFERENCE=1 RUN='^TestReferenceDatetimeJustify'
make check RUN='^TestReferenceDatetimeJustifyFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeJustifyFeatureSmoke$'
```

Each invocation has a900-second deadline. Supplemental units use
` RUN='^TestDatetimeJustify'`,
` RUN='^TestDatetimeJustify'` and
` RUN='^TestDatetimeJustify'`; neither these nor selected
smoke replace ordinary quickcheck or the post-merge1800-second finalcheck.

### Period overlap reference coverage

The shared `TestReferenceDatetimeOverlaps*` family independently covers all13
signatures, half-open/instant boundaries, reversed endpoints, the16-mask NULL
matrix, local clock durations, DATE preferred selection and DST, actual held
parameters, full diagnostics/metadata, query/stored consumers and native controls.
`ParameterDiagnostics` pins bound/imported constant wrapper errors;
`InliningDiagnostics` pins a constant duration body against table-column endpoints.
`InlineSafetyDiagnostics` pins ten/eleven surviving NULLIF comparisons,
weighted function-backed/IO coercion thresholds, duplicated scalar subqueries,
actual volatile duplicated versus single-use arguments and STABLE zoned literal
arithmetic. NULLIF contributes its equality operation; CASE/COALESCE structural
control remains zero overhead. Every reached body failure has direct/prepared
full diagnostics and independent recovery assertions.
No PostgreSQL-only discovery filters remain in the completed definitions.
Computed BOOL projections strictly assert unknown nullability in both backends.
Stored view/generated columns independently assert native `(nullable=true,known=true)`
versus lib/pq's unknown pair. `StoredDeparse` keeps its main SQL/deparse body
shared; an explicit PostgreSQL `pg_attribute` capability phase pins OID16 and
typmod-1, while a separate native empty/direct/prepared public driver phase pins
BOOL name/scan/length/decimal/nullability metadata. Native exports no public
OID/typmod hook and this family does not implement adjacent catalog relations.

```sh
make check RUN='^TestReferenceDatetimeOverlaps'
make check REFERENCE=1 RUN='^TestReferenceDatetimeOverlaps'
make check RUN='^TestReferenceDatetimeOverlapsFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeOverlapsFeatureSmoke$'
```

Each invocation has a900-second deadline. Supplementary parser, sqltype and
engine units use `RUN='^TestDatetimeOverlaps'` with the matching
`./internal/parser`, `./internal/sqltype` or `./internal/engine`.
These selective checks do not replace ordinary quickcheck or the single
1800-second post-merge finalcheck. The feature smoke asserts actual period
results `(true,false,true,true,NULL)`, held reuse, Denver day/hour differences,
diagnostic recovery and native stored-clock-duration replay.

### Datetime extraction

The extraction family uses the same shared fixtures and public APIs in both
modes, with explicit independent values, diagnostics and metadata. Its smoke
covers all six source types and both APIs, strictness, ISO/BC, interval parts,
infinities, precision, prepared Execute-time zones and stored-definition replay:

```sh
make check RUN='^TestReferenceDatetimeExtractFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeExtractFeatureSmoke$'
make check RUN='^TestReferenceDatetimeExtract'
make check REFERENCE=1 RUN='^TestReferenceDatetimeExtract'
```

Each invocation has a900-second deadline. Both complete modes must pass before
review/integration; the pinned mode is not deferred until finalcheck.
Supplemental units live in `./internal/sqltype`, `./internal/parser` or
`./internal/engine` with `RUN='^TestDatetimeExtract'`. Regenerate grammar with
`make generate` after parser source changes; generated code is not hand-edited.
The root performs these checks and the single1800-second finalcheck.
Extraction boundary witnesses distinguish unsupported native constrained-domain
DDL/current_setting/random predicate admission from actual PostgreSQL behavior;
they do not skip either backend or introduce adjacent capabilities. Plain-domain
sources and table CHECK cover selected extraction consumers and later-row
atomicity. The wrapper Context matrix uses the supported CURRENT_DATE field;
real bound random-provider units supplement its public native syntax boundary.
Ordinary and partial B-tree extraction expression indexes are shared successes:
direct/prepared creation, filtered years, retained row count and stored WHERE.
The broad pg_index descriptor inquiry remains PostgreSQL-only. Catalog queries use
existing same-OID joins. Wrapper units supply the existing fixed transaction
clock fixture while retaining a real statement/session scope for random.
COUNT(EXTRACT(month FROM s)) covers actual aggregate-child ownership with an
independent INT8 count2. SUM(EXTRACT(month FROM s)) is a shared direct/prepared
success with exact NUMERIC3 (February2 plus January1), covered by both
`TestReferenceDatetimeExtractStoredConsumers` and
`TestReferenceDatetimeExtractFeatureSmoke`. Scoped helper subtests include
their SQL query for actionable quiet-run diagnostics.

The review lifecycle witnesses are
`TestReferenceDatetimeExtractNotNullAtomicity` (later NUMERIC/FLOAT8 extraction
NULLs into NOT NULL, complete23502 diagnostics, unchanged sentinel),
`TestReferenceDatetimeExtractTransactionAbortRecovery` (runtime22023,
aborted25P02, rollback of staged rows and successful extraction on the SAME
connection/held statement), and
`TestReferenceDatetimeExtractNativeRawRetainedZoneAfterSessionClose`.
The last is explicitly native-only public-driver ownership evidence: an
unconsumed NUMERIC/FLOAT8 hour/zone/epoch cursor captured under New York retains
its original values/metadata after source/peer UTC changes and actual physical
source close/discard; fresh peer executions independently use UTC. It does not
invent PostgreSQL raw-driver controls. All three are included by the full family
selector above; NOT NULL and transaction diagnostics were pinned independently.
The smoke itself now executes1BC century/millennium-1 and2BC decade-1 pairs.

### Datetime constructors

The datetime-constructor smoke directly asserts all five exact result types and
values, BC, TIME24:00/TIMESTAMP carry, sparse `=>`/`:=` interval defaults/NULL,
held prepared session-zone changes, full calendar/seconds/zone/signature errors,
later-row write atomicity and named stored-definition Dump/Restore with replay
of actual emitted native SQL against PostgreSQL:

```sh
make check RUN='^TestReferenceDatetimeMakeFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeMakeFeatureSmoke$'
```

Run the complete identical `RUN='^TestReferenceDatetimeMake'` family in both
modes before review, with900-second deadlines. Supplemental constructor units
use ` RUN='^TestDatetimeMake'`,
` RUN='^TestDatetimeMakeNamedArguments$'` and
` RUN='^TestDatetimeMake(Binding|Properties|ArgumentAssociation)$'`.
These selectors do not replace ordinary quickcheck or post-merge finalcheck.
Successful interval witnesses assert `INTERVAL` metadata and exact public output
codec strings directly; they do not require an `interval::text` conversion edge.
Constructor write coverage uses INSERT affected counts and INSERT/UPDATE/DELETE
RETURNING projections through the same output binder.

### Datetime delay selected proof

The untagged public datetime-delay definitions independently assert direct and
held-prepared rows/VOID versus NULL/raw values/metadata, minimum fractional and
absolute waits, interval/calendar forms, complete diagnostic fields and phases,
actual active cancellation, exact lock/savepoint/private-write cleanup, composed
stored consumers and native control replay. No public SQL witnesses live in
root/internal/smoke packages. Internal units supplement them with wide timestamp,
nonfinite/chunk/cancellation, fixed-clock DST/month-end and descriptor-generation
behavior. Ownership witnesses use existing relation LOCK/NOWAIT admission for
transaction holds and existing session advisory try/unlock for physical-session
holds; they do not require unsupported transaction advisory functions.

Root runs these selected commands from the assigned worktree, each with an
external900-second deadline:

```sh
make check RUN='^TestDatetimeSleep(Binding|Wait|Calendar|Cleanup|Stored)$'
make check RUN='^TestReferenceDatetimeSleep(Values|Timing|Calendar|Diagnostics|Cancellation|Stored|FeatureSmoke)$'
make check RUN='^TestReferenceDatetimeSleep(Values|Timing|Calendar|Diagnostics|Cancellation|Stored|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestDatetimeSleep(Wait|Cleanup)$' RACE=1
```

`fixture.SleepActiveCancel` preserves the common bounded worker lifecycle, requiring
real PostgreSQL `PgSleep` admission or a native active wait stack before cancel.
The admitted callback exercises unrelated peer work while the owner still waits;
workers join before rollback/Close. The original binary cancellation caller
contract remains unchanged through the shared private helper.

Separate public source `local://datetime-sleep-feature-smoke.go` remains an OMP
session artifact. Root materializes `.datetime-sleep-feature-smoke/main.go`, runs
`timeout 60s go run ./.datetime-sleep-feature-smoke` from the assigned worktree,
records actual argv/exit/output/observations and removes only that throwaway
materialization before review. Successful exit0 prints exactly
`datetime sleep smoke passed` after all assertions.
Do not commit a smoke test, temporary executable directory or session source.
Whole-project Docker-free verification remains `make quickcheck`; post-merge
proof is `make finalcheck` with its external1800-second deadline. Neither
finalcheck nor parity alone replaces both selected public modes.
