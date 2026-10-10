# Adding and maintaining tests

## Mandatory policy

Agents MUST read and follow this guide before adding, modifying or migrating tests.
This is the authoritative test-placement and implementation workflow.

- All tests exercising public APIs by issuing SQL and checking responses MUST live
  in `reference/` as **dual-mode reference tests**. This includes direct/prepared
  execution, rows, metadata, diagnostics, cancellation and SQL witnesses around
  native controls. Implementing these tests in root, `smoke/`, `internal/` or any
  other directory is forbidden.
- When working in an area containing offending tests, immediately migrate them
  to dual-mode tests in `reference/`. Preserve the complete behavioral assertion
  set and public execution routes, merge with existing reference coverage where
  appropriate, and remove the superseded tests. Do not leave native-only copies.
  A migration subagent MAY handle this independent slice to keep the current
  implementation context focused; give it the original assertions and this guide.
- Internal unit tests that exercise package functions rather than public SQL APIs
  may remain beside their package. They supplement, never replace, reference
  coverage for PostgreSQL functionality.
- Every new PostgreSQL functionality implemented in stuntgres MUST add or modify
  dual-mode reference coverage. Every discovered compatibility bug MUST have a
  failing reference regression before its fix.
- The actual SQL/API exercise and assertions MUST share one body across modes.
  Never maintain near-duplicate PostgreSQL and quick-mode implementations.
  Mode selection changes backend availability, not the scenario.
- Use only public stuntgres APIs in `reference/`; do not import internal packages
  or root-private test helpers. Name tests `TestReference<Feature><Behavior>` and
  use untagged `*_test.go` files so ordinary `make quickcheck` discovers them.
- Assert behavior, boundaries, errors, atomicity and lifecycle transitions, not
  source text, wiring counts, mock echoes or parity alone. Keep functional files
  at most 750 lines; shared fixture code is exempt. Keep output quiet by default.
- Every added, modified or migrated dual-mode reference test MUST be selectively
  run in both real pinned PostgreSQL mode and Docker-free quick mode, using the
  same completed test definitions and a `RUN` selector covering all affected tests.
  Repeat both selective runs after changes to those tests or their implementation.
  Fullcheck does not replace this pre-review requirement.
- Concurrency tests MUST identify a deadlock victim from its error outcome, not
  worker completion order: a ready channel select can choose either result.
  Victim cleanup can also wake a successful survivor before the victim reports
  its result. Join both workers under a bounded deadline and retain the exact
  victim, survivor and side-effect assertions.
  `TestUniqueClaimDeadlock` collects both outcomes under one five-second deadline,
  requires exactly one error, and assigns victim/survivor assertions by that error;
  its worker cleanup cancels and joins on assertion-failure paths as well.
- Catalog discovery fixtures MUST use scenario-specific object names when
  asserting a complete database-wide candidate bag. The shared PostgreSQL
  fixture resets `public`, not every nonpublic schema; unrelated same-name
  objects can legitimately remain outside the scenario's search path.

Native Reset/Dump/Restore have no PostgreSQL API equivalent. Keep their witnesses
in the same dual-mode reference scenario, with explicit native-only control
phases and independently specified PostgreSQL SQL outcomes where applicable.
Do not invent a PostgreSQL control API or duplicate the main SQL test body.

## Required implementation order

1. **Write the shared reference scenario and run real PostgreSQL first.** Use
   the pinned PostgreSQL 18.0 fixture, not a local server or newer release. Start
   with independently reasoned expectations; use observed PostgreSQL results to
   resolve unknown values, metadata and diagnostics.
2. **Record reference data from that run.** Put concrete expected rows, column
   names, database type names, affected counts and relevant diagnostics in the
   test. Review them as a contract; never derive expected values from stuntgres
   output, copy native failures into goldens, or assert only backend equality.
3. **Run quick mode in the second iteration.** Confirm the recorded contract
   exposes missing/incorrect stuntgres behavior. Observe the failing regression,
   implement the smallest fix, and rerun until it passes.
4. **Finish by selectively running both modes.** Run the same completed definitions
   in quick and real PostgreSQL modes, confirming independent expectations and
   consistency for every added, changed or migrated scenario. Then run ordinary
   `make quickcheck`; final `make fullcheck` runs after integration under the
   catalog-part workflow. Selected tests do not replace full verification.

### Commands and oracle-first discovery

Run from repository root with Go >=1.26 and GNU `timeout`. Real mode also needs
working Docker and image/module access.

```sh
# First: real PostgreSQL discovery/validation.
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$' VERBOSE=1
# Second: Docker-free validation of the recorded contract.
make quickcheck PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$'
# Before review: both modes, using exactly the same completed test.
make quickcheck PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$'
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$'
# Ordinary full Docker-free verification.
make quickcheck
# After integration: all final gates under one 30-minute deadline.
make fullcheck
```

`make quickcheck REFERENCE=1 PACKAGES=./reference` enables `-reference=true` and the `reference` build tag; it runs
both native and PostgreSQL backends, not PostgreSQL alone. The ordinary runner
visits native first. During the initial oracle-discovery iteration, use the
shared `p.backends()` loop and temporarily select only `backend.pg` (for example,
`if !backend.pg { continue }`) so an unimplemented native feature cannot prevent
PostgreSQL observations. Remove that discovery filter before quick validation
and final verification. Never commit a filter that bypasses either backend.

There is no automatic golden-file recorder or PostgreSQL-only make mode.
Use failure diagnostics or temporary verbose-only observation logging to inspect
unknown PostgreSQL results, then encode those results as explicit expectations
and remove discovery scaffolding. `p.step` validates expectations; it does not
learn or update them. Do not weaken assertions to accommodate native failures.
Docker/image failures in an explicitly enabled real run MUST fail visibly, not
be skipped or mocked. Fix slow/hanging tests rather than increasing deadlines.

The serial/identity inquiry family is selected with
`RUN='^TestReferenceSerialIdentity(Ownership|Lifecycle|Names|Nulls|Diagnostics|Consumers|Controls|FeatureSmoke|SystemColumns)$'`.
Its shared public scenarios independently assert AUTO/INTERNAL/manual ownership,
ownership versus defaults, qualified-name parsing and literal column names,
strict NULL and TEXT metadata, complete diagnostic fields and binding/execution
phases, symbolic consumers, retained results, and ownership-preserving native
Dump/Restore with actual emitted-SQL replay on PostgreSQL. Native control phases
have no PostgreSQL API equivalent; the SQL witnesses are shared. The cohesive
feature smoke is `RUN='^TestReferenceSerialIdentityFeatureSmoke$'`. Run both
selectors in quick and pinned modes using the commands above.
System-column coverage independently asserts all six existing table system names
return TEXT NULL, while absent system names on views retain complete42703
diagnostics and held recovery. The feature smoke includes both boundaries.

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
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeTrunc' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeTrunc' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeTruncFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeTruncFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^TestDatetimeTrunc' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestDatetimeTrunc' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestDatetimeTrunc' VERBOSE=0
```
Smoke expects distinct NY fold hours05/06UTC, spring day05UTC, Sydney day13UTC,
held NULL/error recovery, unchanged late-write sentinel and symbolic stored
default/view/generated/index replay. Full family checks additionally cover
week2006-01-01→2005-12-26, quarter2001-02-16→2001-01-01, negative interval
seconds→-1second and TIME24:00→zero-day24hour INTERVAL. These selected gates
do not replace ordinary quickcheck or post-merge fullcheck.

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
make quickcheck PACKAGES=./reference RUN='^TestReference(DatetimeTxTimes|TimestampSQLTransactionChainClock)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(DatetimeTxTimes|TimestampSQLTransactionChainClock)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeTxTimesFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeTxTimesFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES='./internal/engine ./internal/parser' RUN='^TestTransactionTime' VERBOSE=0
```


The numeric-formatting family independently asserts the five numeric
`to_char` overloads, preferred FLOAT8 SMALLINT route, `to_number`, exact/binary
rounding, C currency space, global FM, signs, Roman/ordinal/V/scientific
semantics, permissive parsing, complete diagnostics and deferred mask demand.
Metadata, consumers, atomicity, stored definitions and actual native control
replay use the shared fixture. The temporal ownership boundary now expects
numeric success; epoch/TIME/ARRAY boundaries remain unchanged. Negative
numeric setup uses signed numeric string casts, not adjacent unary arithmetic.
The repeated-parameter NUMERIC→TEXT conversion gap keeps independently
specified native 0A000 and PostgreSQL success rather than widening cast scope.
Consumer TEXT composition uses `to_char(to_number(...),'FM99')`, not an
unowned general NUMERIC→TEXT cast. Stored-definition inquiries retain their
exact SQL text and reuse deparse's own backend metadata contract; numeric
computed metadata remains unchanged. Default catalog lookup joins matching
OID columns instead of requiring an adjacent OID=REGCLASS operator. Failed
native Restore reaches an invalid Roman DEFAULT through a scalar INSERT
that omits its column, then asserts unchanged complete catalog/data, retained
cursors and held statements; it does not expand the scalar Restore importer.
The UUID-default regression covers rebased closed-tree admission through both
numeric providers: non-NULL UUID generators make `num_nulls` return0, so
`to_char` and `to_number(format(...))` independently return TEXT/NUMERIC0.
Direct/prepared DEFAULTs, reused held inserts, new DEFAULT VALUES after actual
Dump/Restore and emitted SQL replay retain symbolic execution. The numeric
feature smoke reuses the same scenario body. Package admission units preserve
unrelated volatility and row/parameter/stage rejection.

Run all changed definitions in both modes under separate 900-second deadlines:
```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(NumericFormatting|DatetimeFormattingSignaturesOwnerBoundary)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(NumericFormatting|DatetimeFormattingSignaturesOwnerBoundary)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceNumericFormattingFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceNumericFormattingFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^TestNumericFormatting' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^Test(NumericFormatting|DatetimeFormattingBindingAuthority)' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestNumericFormatting' VERBOSE=0
```
Smoke independently expects exact BIGINT9007199254740993, PR `<485>`,
Roman `CDLXXXV`, scientific ` 4.86e-04`, and V input
`1234.560000000000000000`, plus C/SMALLINT precision, NULL/error recovery,
late-write atomicity and stored default/view replay. Package units supplement
these public contracts with exhaustive canonical Roman1–3999, V factor-first
underflow, immutable plan/diagnostic ownership, Retag and cancellation.
The smoke also demands numeric/UUID defaults before and after native replacement;
these defaults must return0 rather than fail the closed UUID admission walker.

Binary conversion uses independent pinned18.0 goldens for all128 directed pairs,
all42 identities, format/padding boundaries, complete diagnostics, held statements,
volatility and late-write atomicity. Native lifecycle phases retain readers and
metadata across replacement and failed/canceled imports, and replay actual emitted
SQL on PostgreSQL. Select the whole affected family and smoke in both modes,
each invocation under900 seconds:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceBinaryConversion' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBinaryConversion' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceBinaryConversionFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBinaryConversionFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^TestBinary(Conversion|Encoding|Format)' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestBinaryConversion' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestBinaryConversion' VERBOSE=0
```

The shared hex/escape cores also serve BYTEA input; run existing
`^TestReferenceBytea` regressions selectively in both modes before review.

Stored-view metadata uses the established native declared nullable contract
`true,true`; pq reports unknown `false,false`. Computed conversion expressions
continue to require unknown nullability. Actual emitted-SQL replay uses the pq
contract explicitly, not metadata captured from the native backend.
Replay objects have bounded deferred cleanup before the next backend body,
including failure exits; the shared scenario resets schemas per test, not backend.

`TestReferenceBinaryConversionActiveCancellation` separately exercises admitted
aggregate encode reads and ordered staged convert writes on a32MiB LATIN1 source.
Native observes the worker's actual provider stack before canceling its context;
PostgreSQL observes the actual query active on the held backend PID and uses
server cancellation, preserving the same session. Workers have bounded joins on
every exit. Assertions require no successful cursor/result, unchanged sentinel,
ordinary native context cancellation or the complete PostgreSQL57014 diagnostic,
then independently expected read sum4 and write count2/UTF8 bytes through the
same pre-held read/write statements. No pg_sleep substitute or production delay
is used. Existing precanceled/expired contexts remain separate coverage.

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceBinaryConversionActiveCancellation$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBinaryConversionActiveCancellation$' VERBOSE=0
```

The no-bind direct repeated-parameter witness asserts an explicit inherited
transport boundary (PG42P02 Position16 versus native42P08 Position19/Detail
`bytea versus name`); genuine Prepare remains shared42P08. See DIALECT's binary
conversion note. Codec diagnostics and all independent pair/identity goldens
remain unchanged.


The integer-base/TEXT-MD5 family asserts independent 32/64-bit two's-complement
vectors, RFC/UTF8 digest constants, unknown-TEXT versus known-BYTEA selection,
held parameter recovery and retained TEXT metadata, complete diagnostics,
consumer/write atomicity, symbolic stored widths and MD5 through native controls
with actual emitted SQL replay, and public cancellation. Run the complete
affected family in both modes before review, each under the 900-second deadline:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(StringNumericBase|BinaryHash)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(StringNumericBase|BinaryHash)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceStringNumericBaseFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceStringNumericBaseFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^Test(StringNumericBase|BinaryHash)' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^Test(StringNumericBase|BinaryHash)' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestStringNumericBase' VERBOSE=0
```

The smoke expects `fffffb2e` versus `fffffffffffffb2e` for INTEGER/BIGINT -1234,
`0` for zero, RFCabc/empty MD5, typed NULL, exact ambiguity/missing/input
diagnostics, no partial publication, and successful held-call recovery. Typed
nil `[]byte` inferred as TEXT intentionally retains native empty-input MD5 versus
pq NULL; do not erase this inherited adapter difference or use Go kinds for
signature selection. These selectors do not replace ordinary quickcheck or
post-merge fullcheck.

Size parsing/formatting coverage uses the untagged `TestReferenceSize` family:
independent exact binary/fractional/signed-limit vectors, every promotion
boundary, numeric scale/specials, full diagnostics and stages, actual held
statements and recovery, NULL-only/empty raw metadata, expression consumers and
write/transaction atomicity. Stored overloads use compact/pretty semantic deparse
replay rather than incidental formatting goldens. Native controls preserve
retained rows/metadata and failed replacement atomicity; pinned PostgreSQL
consumes the actual native-emitted Dump SQL. Every changed definition must run
in both modes before review, each within the 900-second deadline:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceSize' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSize' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceSizeFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSizeFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^Test(Size|Math|Numeric)' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestSize' VERBOSE=0
```

The smoke expects 1048576 INT8, `1024 bytes`, `-11 kB`, `10.5000 bytes`,
beyond-BIGINT `10240 PB`, typed TEXT NULL metadata, exact malformed-unit and
overflow records, held recovery, no failed-write prefix, and faithful stored
overloads/control replay. The shared MathPlan and numeric codec seams also
require existing math/numeric regression coverage; selected checks do not
replace ordinary quickcheck or post-merge fullcheck.

Size consumer witnesses retain the explicit native internal-CHAR coercion
boundary alongside independent PostgreSQL results; shared COUNT and SUM
arguments, INSERT affected counts/state reads and UPDATE RETURNING prove the
supported composition paths. Deparse catalog-query metadata independently checks
native known-nullable TEXT versus PostgreSQL unknown nullability, without changing
size-result metadata. Retained descriptors are captured before cursor exhaustion
and remain asserted after replacement/consumption.

DELETE USING coverage lives in the untagged `TestReferenceDeleteUsing` family:
physical occurrence/count/output deduplication, actual joined producers, alias/self
scopes, diagnostics, held parameters/source metadata, privileges, atomicity,
contention/isolation/cancellation and committed-state/native retained-output controls.
`MigratedCore`, `MigratedExpressions`, `MigratedReturning`,
`MigratedReturningControls`, `MigratedSequenceBoundary` and
`MigratedSequenceComposition` preserve the superseded public smoke assertion/route
union. Native no-USING qualified-cast and eager decisive-FALSE sequence demand
boundaries are explicit, not adjacent compatibility fixes. Held-dependency
invalidation asserts shared missing-name/no-draw behavior with independent state
expectations and restores the fixture search path on every exit.
Native ACL mutation/queue authority units supplement explicit PostgreSQL ACL phases.
Fast pre-cancelled DELETE calls are a native adapter boundary: lib/pq may complete
them before cancellation is reported. Shared PostgreSQL cancellation evidence comes
from `TestReferenceDeleteUsingWaitTimeout` at actual admitted target/source waits,
with complete diagnostics, no partial effects and explicit transaction recovery.
Sequence USING rejection requires the Execute stage, including a genuine held
Prepare followed by target/source/sequence no-effect assertions before execution.
Index-source analysis diagnostics remain separate; neither policy changes ordinary
SELECT/UPDATE source semantics. Internal admission/contender workers register
cancellation and a five-second, exactly-once outcome/exit join before queue
assertions; failure cleanup closes any retained result before session teardown.

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceDeleteUsing|^TestReferenceDeleteCoreExcludedForms' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDeleteUsing|^TestReferenceDeleteCoreExcludedForms' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDeleteUsingSmoke' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDeleteUsingSmoke' VERBOSE=0
```

Each selective invocation has a900-second deadline. Real oracle evidence precedes
native implementation; final verification uses the same completed bodies in both
modes. Internal `TestDeleteUsing` parser/engine tests exercise positioned traversal,
captured identity/authority, successful-only suppression and failure/cleanup seams.

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
make quickcheck PACKAGES=./reference RUN='^TestReference(DatetimeZone|Timezone)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(DatetimeZone|Timezone)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeZoneFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeZoneFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^TestDatetimeZone' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestDatetimeZone' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestDatetimeZone' VERBOSE=0
```

Both complete reference modes must run before review/integration. Each quickcheck
has a900-second deadline. Provider units include warmed EST/MSK/IANA allocation
witnesses; they supplement, not replace, public independent SQL expectations.
The discovery backend.pg filter and focused logging-only oracle probes are
temporary and must be removed before native checks; neither remains in the
completed family. The root runs verification and feature smoke after edits, then
the final post-merge fullcheck under one1800-second deadline.

The interval-justification family covers exact30-day/24-hour boundaries,
mixed signs and component extrema, complete diagnostics and phases, metadata,
held inferred parameters/recovery, four styles, existing consumers, statement
atomicity and native Dump/Restore with actual emitted SQL replay. Precanceled
ordinary context errors are a native-only API boundary, not a race-prone fast pq
request assertion. Run the same complete definitions in both modes:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeJustify' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeJustify' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeJustifyFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeJustifyFeatureSmoke$' VERBOSE=0
```

Each invocation has a900-second deadline. Supplemental units use
`PACKAGES=./internal/sqltype RUN='^TestDatetimeJustify'`,
`PACKAGES=./internal/parser RUN='^TestDatetimeJustify'` and
`PACKAGES=./internal/engine RUN='^TestDatetimeJustify'`; neither these nor selected
smoke replace ordinary quickcheck or the post-merge1800-second fullcheck.

### Shared-comment catalog coverage

The untagged `TestReferencePGShDescription` family independently asserts the
three-column relation2396 contract, shared ROLE comment identities/contents,
local catalog separation, ordinary readability, full diagnostics, transactions,
held freshness, detached rows/metadata and stored-view/composition behavior.
Role IDs come from a separately owned helper relation's `pg_class.relowner`;
schema IDs come from that relation's `relnamespace`, not an adjacent catalog.
Native bootstrap has no seeded shared comments, while PostgreSQL can have
legitimate global rows: scenarios filter exact object/class pairs, never assert
whole-cluster row-count parity. Native-only Reset/Dump/Restore phases preserve
shared roles outside local replay and feed actual emitted local SQL to PostgreSQL.
Pure `TestPGShDescription` units cover descriptor/PUBLIC ACL, uint32 maximum and
uint64 non-aliasing, detached captures, inquiry agreement, saved-scope nested
opening and entry/in-loop cancellation without private public-SQL duplicates.

Run the same completed definitions in both modes, each within900 seconds:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferencePGShDescription' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGShDescription' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePGShDescriptionFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGShDescriptionFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestPGShDescription' VERBOSE=0
```

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
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeOverlaps' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeOverlaps' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeOverlapsFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeOverlapsFeatureSmoke$' VERBOSE=0
```

Each invocation has a900-second deadline. Supplementary parser, sqltype and
engine units use `RUN='^TestDatetimeOverlaps'` with the matching
`PACKAGES=./internal/parser`, `./internal/sqltype` or `./internal/engine`.
These selective checks do not replace ordinary quickcheck or the single
1800-second post-merge fullcheck. The feature smoke asserts actual period
results `(true,false,true,true,NULL)`, held reuse, Denver day/hour differences,
diagnostic recovery and native stored-clock-duration replay.

### Sequence definition catalog discovery

The shared `TestReferencePGSequence` family covers the eight-column
`pg_catalog.pg_sequence` contract, exact definition options rather than generator
state, catalog identities, transactional visibility, repeatable-read peer
ALTER/CREATE/DROP, first-Prepare versus first-Execute timing, privileges and
other-session temporary definitions, full diagnostics, cancellation and actual
native Dump replay. `TestReferencePGSequenceFeatureSmoke` selects the integrated
definition lifecycle and control replay witness.

The privilege fixture keeps PostgreSQL's four false `has_schema_privilege` /
`has_sequence_privilege` assertions. Native mode does not implement those
predicates: a qualified `nextval` must report private-schema denial, while
numeric-regclass `nextval`, `currval` and `setval` must independently report
sequence denial with exact existing diagnostics before the same metadata read.
Denied `nextval` excludes USAGE/UPDATE and denied `currval` excludes USAGE/SELECT.
Failed native Restore appends a supported `CREATE SEQUENCE ... CACHE 0` to the
actual dump, expects option-binding SQLSTATE 22023, and preserves the complete
original definition bag; arbitrary ALTER is outside Restore's accepted input.

The oracle discovery observed retained RR definitions and row bags. Prepare
inside BEGIN retained start15/cache9 after peer ALTER; a statement prepared
before BEGIN captured start21/cache4 at its first execution. Discovery filters
are removed: both commands run the shared completed scenario. Private-schema
fixtures use default absent PUBLIC privileges with the independent false
predicates or denied-operation witnesses above, without native REVOKE support.

```sh
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGSequence' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePGSequence' VERBOSE=0
```

After implementation, include all migrated definition probes in both modes:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(PGSequence|SequenceCacheBoundsRow|SequenceHugeCacheArithmetic|SequenceCreateRestart|AlterTypeIdentityTargets|AlterTypeIdentityFailuresAndPrephase)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(PGSequence|SequenceCacheBoundsRow|SequenceHugeCacheArithmetic|SequenceCreateRestart|AlterTypeIdentityTargets|AlterTypeIdentityFailuresAndPrephase)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePGSequenceFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGSequenceFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestPGSequence' VERBOSE=0
```

Each quickcheck invocation has a 900-second deadline. The final fullcheck runs
after merge under its single 1800-second deadline; neither gate replaces the
selective dual-mode runs.

The extraction family uses the same shared fixtures and public APIs in both
modes, with explicit independent values, diagnostics and metadata. Its smoke
covers all six source types and both APIs, strictness, ISO/BC, interval parts,
infinities, precision, prepared Execute-time zones and stored-definition replay:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeExtractFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeExtractFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeExtract' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeExtract' VERBOSE=0
```

Each invocation has a900-second deadline. Both complete modes must pass before
review/integration; the pinned mode is not deferred until final fullcheck.
Supplemental units use `PACKAGES=./internal/sqltype`, `./internal/parser` or
`./internal/engine` with `RUN='^TestDatetimeExtract'`. Regenerate grammar with
`make generate` after parser source changes; generated code is not hand-edited.
The root performs these checks and the single1800-second final fullcheck.
Extraction boundary witnesses distinguish unsupported native constrained-domain
DDL/current_setting/random predicate admission from actual PostgreSQL behavior;
they do not skip either backend or introduce adjacent capabilities. Plain-domain
sources and table CHECK cover selected extraction consumers and later-row
atomicity. The wrapper Context matrix uses the supported CURRENT_DATE field;
real bound random-provider units supplement its public native syntax boundary.
Ordinary extraction expression indexes remain shared successes; partial-index
witnesses separately assert native parser rejection/no effects and real
PostgreSQL partial descriptors/filtered rows. Catalog definition queries use
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

### Regtype completed definitions

The `^TestReferenceRegtype` shared-body family encodes pinned PostgreSQL 18.0
diagnostics, metadata, raw transport, warning timing and held literal identity.
Every released/initialized name and array is exercised through regtype SQL
without requiring a native pg_type projection. The complete identity facts and
fixed-versus-allocated provenance are described in
[the inventory handoff](design/regtype-pg18-oracle.md).

The completed input and authorization scenarios preserve generic qualified
varchar modifier witnesses and use a superuser-owned denied table in an
accessible reader-owned schema. `TestReferenceRegtypeAliasModifierSyntax`
preserves rejected string/parenthesized alias modifiers across all six routes,
including held runtime input. `TestReferenceRegtypeHeldLiterals` and
`TestReferenceRegtypeWarnings` cover authority-sensitive identity capture and
literal-versus-parameter warning timing. No discovery filters, observation
logging or deliberate discovery failures remain.

`TestReferenceRegtypeOutputAuthority` and its held/atomic/native-control companions
exercise selected VARCHAR IO, TEXT/VARCHAR assignment, domain sources, stored
defaults and admitted ALTER-column conversions with actual Dump replay and
retained string metadata. The unrelated-role
family requires unchanged held OIDs and no repeated warnings after unrelated
CREATE/DROP ROLE, while its qualified-authority control requires literal/fresh
Prepare 42501 Position8, frame 42501 with portal Context, and owner-role recovery.
Run the completed review-remedy witnesses in both modes before review, each with
a 900-second deadline:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceRegtype(OutputAuthority|UnrelatedRoles|ArrayDisplacement)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRegtype(OutputAuthority|UnrelatedRoles|ArrayDisplacement)' VERBOSE=0
```

Validate the same completed definitions in both modes before review; each
quickcheck invocation has a 900-second deadline:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceRegtype' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRegtype' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceRegtypeFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRegtypeFeatureSmoke$' VERBOSE=0
```

The feature smoke independently asserts name/OID conversion, builtin and user
row identities, qualification/shadowing, held input recovery, metadata,
persistent scalar/domain/array values and native control atomicity. Native
controls must replay the actual emitted native stream on PostgreSQL; never
substitute a manually reconstructed stream or an invented PostgreSQL
Dump/Reset/Restore API. The unchanged cohesive fixture's `checkRaw` hook supplies
independent REGTYPE/array transport checks through the small regtype helper.
These selectors do not replace ordinary quickcheck or the post-merge fullcheck
under its single 1800-second deadline. Completed definitions are not passing
verification until the root records successful runs.

Regtype control witnesses retain a supported TEMP SEQUENCE across Reset/Restore
to fence physical-session ownership and allocator collisions. Persistent
domains supply the user-defined TypeClassID remapping witnesses. Temporary
domain creation remains an adjacent native 0A000 boundary, even in an already
initialized namespace; the path scenario asserts that boundary natively and
uses PostgreSQL-only temporary-domain setup for additional oracle context.

The removed `smoke/TestRoleSmoke` assertion union is preserved by
`TestReferenceRoleCreation` (aliases/login/counts/duplicate/drop),
`TestReferenceRoleAuthorization` (direct/held identities, NOLOGIN switches,
table ACL/no mutation and owner dependencies),
`TestReferenceRoleAuthenticatedSessions` (self/default/reset/peer isolation),
`TestReferenceRoleTrustedConnectorIsolationAndReplacement` and
`TestReferenceRoleTrustedConnectorAdmissionAndOpaqueIsolation`
(authentication, reconnect and Driver/opaque-key isolation), and
`TestReferenceRoleMigratedSmokeControls` (changed-session pool reuse, option
boundary, reader-authorized Dump/Reset/Restore, exact owner directive,
failed Restore/sentinel preservation, legacy stream and role cleanup).
`TestReferenceControlTransactionsHostAuthorization` additionally preserves
reader identity/settings and denied table access across host replacement.
Run the completed remedy/migration definitions in both modes:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(Regtype|Role|ControlTransactionsHostAuthorization)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(Regtype|Role|ControlTransactionsHostAuthorization)' VERBOSE=0
```

Each invocation has a900-second deadline. Existing Array/Domain/Enum,
SearchPathStoredViewRuntime, StringCoreReviewRestore and
TableCoreAuthorizationAndOwnershipCleanup reference definitions also exercise
the corrected catalog allocator/ownership seam without changed expectations.

The datetime-constructor smoke directly asserts all five exact result types and
values, BC, TIME24:00/TIMESTAMP carry, sparse `=>`/`:=` interval defaults/NULL,
held prepared session-zone changes, full calendar/seconds/zone/signature errors,
later-row write atomicity and named stored-definition Dump/Restore with replay
of actual emitted native SQL against PostgreSQL:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeMakeFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeMakeFeatureSmoke$' VERBOSE=0
```

Run the complete identical `RUN='^TestReferenceDatetimeMake'` family in both
modes before review, with900-second deadlines. Supplemental constructor units
use `PACKAGES=./internal/sqltype RUN='^TestDatetimeMake'`,
`PACKAGES=./internal/parser RUN='^TestDatetimeMakeNamedArguments$'` and
`PACKAGES=./internal/engine RUN='^TestDatetimeMake(Binding|Properties|ArgumentAssociation)$'`.
These selectors do not replace ordinary quickcheck or post-merge fullcheck.
Successful interval witnesses assert `INTERVAL` metadata and exact public output
codec strings directly; they do not require an `interval::text` conversion edge.
Constructor write coverage uses INSERT affected counts and existing UPDATE/DELETE
RETURNING projections, not unsupported INSERT RETURNING syntax.

The bitstring family covers independently pinned BIT/VARBIT values, raw scanner
and modifier diagnostics, direct/held parameters, assignment atomicity, complete
metadata and retained ownership, plus native Dump/Reset/Restore with PostgreSQL
replay of the actual emitted SQL. The expanded feature smoke combines the compact
scenario with stored-format replay/default demand, compound/simple-parenthesized
modifiers, differing-width repeated slots and held parameterized cursor snapshots.
Run each invocation under the quickcheck900-second deadline, quietly:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceBitstring' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBitstring' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$' VERBOSE=0
```

Both modes are required before review/integration. Final fullcheck remains one
1800-second gate after merge. Discovery-only deliberate observation failures
and PG-only filters must be removed before the native failing proof and gates.

The string-encoding smoke executes all five functions with independently expected
rows/types, Unicode16 assigned/unassigned codepoints, exact conversion and escape
diagnostics, held valid/NULL/error/recovery frames and stored NAME/INT4 overloads.
Native phases exercise retained snapshots, Dump/Reset/Restore, failed Restore
atomicity and real PostgreSQL replay of actual emitted native SQL:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceStringEncodingFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceStringEncodingFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceStringEncoding' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceStringEncoding' VERBOSE=0
```

Each invocation has a900-second deadline. Smoke does not replace the complete
family, ordinary quickcheck or post-merge fullcheck. The offline Unicode generator
also emits assignment ranges from the existing vendored Unicode16 data:

```sh
go run ./internal/sqltype/unicode16/generate -source ./internal/sqltype/unicode16/data -output ./internal/sqltype/unicode16/tables_generated.go -case-output ./internal/sqltype/unicode16/case_tables_generated.go -assigned-output ./internal/sqltype/unicode16/assigned_tables_generated.go
```

Concurrent index scenarios use `^TestReferenceConcurrentIndex` for the complete
writer/snapshot/lifecycle/diagnostic/control family, and
`^TestReferenceConcurrentIndexFeatureSmoke$` for the feature smoke.
`^TestReferenceConcurrentIndex(TypeReconstruction|KeyLockEligibility)$` selects
the added independent TYPE/private-prefix and row-key-lock consumer contracts
for the root's real-first observation pass.
`^TestReferenceConcurrentIndexHash$` selects inherited hash-method lifecycle,
first/later writer admission, actual flags and equality results, unsupported
hash UNIQUE full diagnostics, and emitted Dump/Restore/replay coverage. It is
also included in the concurrent feature smoke. Preserve and run the integrated
`^TestReferenceHashIndex` family alongside it; the hash merge cancellation
calibration migrates to the actual descriptor/readiness authority without
changing its expected results.
`^TestReferenceConcurrentIndexHashInvalidQuery$` exercises a canceled ready-invalid
hash build and a subsequent committed grandfathered row. Its independent INT4
equality/heap results and REINDEX recovery close query-consumer eligibility.
The supplemental `TestConcurrentIndexHashInvalidQueryCapture` uses actual
incomplete buckets and matching storage identity to prove invalid capture is
rejected, heap filtering returns the row, and healthy reconstruction is selected.
Both regressions preserve pre-ready admission rather than repairing the fixture.
Run each selector with both `make quickcheck PACKAGES=./reference RUN='...' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='...' VERBOSE=0`.
Affected ordinary-route regression selectors additionally include
`^TestReferenceBtreeIndex(OrdinaryShareBuild|ExcludedDefinitionsDiagnostics)$`
and `^TestReferenceRelationDeadlock`.
PostgreSQL queue/progress observations use actual lock/PID identities. Native
ordinary pending-Share/later-writer queue ordering is proved by internal lock
units: no public native queue catalog or worker-start timing is substituted.
The discovery-only pg-only filter has been removed: every final shared backend
loop discovers native and PostgreSQL scenarios according to the standard mode.
Native U1–U7 use `make quickcheck PACKAGES=./internal/engine RUN='^TestConcurrentIndex' VERBOSE=0`;
parser ordinary/concurrent heads use the same invocation with `PACKAGES=./internal/parser`.
Each quickcheck invocation has one 900-second deadline. Root owns parser
generation, formatting and verification after all implementation edits land;
agents do not run mid-flight checks. Final post-merge fullcheck uses one
1800-second deadline. These commands are required inputs, not reported passes.

The current-tip ordinary UNIQUE/NND integration retains its independent shared
`^TestReferenceUniqueIndex` family in both modes. The added
`^TestReferenceConcurrentIndexNullsNotDistinct$` definition covers first-build
NULL duplicates, real old-snapshot admission, Ready-before-Valid NULL enforcement,
atomic INSERT/UPDATE, actual emitted Dump/native Restore/unchanged PostgreSQL
replay and default/explicit DISTINCT controls. Run this exact selector real-first,
then natively, each under `timeout 900s make quickcheck`; it is also in feature
smoke. Public ordinary provider exclusions remain admission policy; isolated
Restore reconstructs emitted computed definitions using existing authenticated
destination authority, not a UNIQUE parser option.

The index-clause feature smoke exercises mixed keys, an existing scalar expression
key with explicit owned collation/class and INCLUDE, prepared uppercase projection,
payload/derived-key maintenance, the total 32/33 boundary and native controls with
PostgreSQL replay of the actual emitted native SQL. Compact `pg_get_indexdef`
assertions cover default/provider spelling, the INCLUDE ordinal boundary and
post-TYPE state without changing canonical Dump/replay authority:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceIndexClausesFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceIndexClausesFeatureSmoke$' VERBOSE=0
```

This smoke does not replace the complete identical dual-mode family selection
documented in [the index-clause design](design/PG18-INDEX-CLAUSES.md).

The index-property smoke checks the eleven independently expected BOOL values
for B-tree AM/index capabilities, mixed DESC/NULLS ordering and INCLUDE roles,
plus prepared true/false/NULL and explicit native Reset/Restore/foreign restore:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceIndexPropertiesFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceIndexPropertiesFeatureSmoke$' VERBOSE=0
```

Run both with a900-second deadline. The full `^TestReferenceIndexProperties`
selector covers every new definition; see [the inquiry design](design/PG18-FUNC-INFO-INDEX-PROPERTIES.md)
for the shared-coercion consumer risk selectors. Smoke does not replace those
identical dual-mode selective runs, ordinary quickcheck or post-merge fullcheck.
The dependency catalog family independently specifies exact represented edge bags,
all seven result descriptors, owner-specific diagnostics, supported private
sequence changes, retained cursors and native controls with actual SQL replay.
Run the identical complete family in both modes, each with a900-second deadline:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferencePGDepend' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGDepend' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePGDependFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGDependFeatureSmoke$' VERBOSE=0
```
Changed existing default definitions must also run in both modes; the family
prefix does not select them. They preserve genuine direct and prepared CREATE,
default INSERT, data/provider sentinels and existing excluded sequence-expression
controls without separate duplicated PostgreSQL-only success bodies:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(SequenceLatestIntegrationDefaultBoundary|StringCoreRegclassDefaultSourceConsumerRoutes|StringCoreRegclassDefaultBoundary)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(SequenceLatestIntegrationDefaultBoundary|StringCoreRegclassDefaultSourceConsumerRoutes|StringCoreRegclassDefaultBoundary)$' VERBOSE=0
```

Each selective invocation has a900-second deadline. Source-route migration
definitions are additional named selections, not replacements for this family
or these changed existing definitions.


Supplemental collector/walker units use
`make quickcheck PACKAGES=./internal/engine RUN='^TestPGDepend' VERBOSE=0`.
These do not replace the dual-mode runs, ordinary quickcheck or post-merge
fullcheck. Public capability boundaries remain explicit: no REGCLASS column
storage, set-operation query rollout, transactional CREATE TABLE/ALTER DEFAULT
or native TEMP-table creation is implied by metadata reading.


The prepared-transaction family independently asserts state/diagnostics,
ownership and five-column XID metadata, physical-origin-close survival,
SESSION/LOCAL and TEMP restrictions, held resources, coupled existing-index/
ALTER/data/sequence publication, SSI finalization and native replacement guards:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferencePreparedTransaction' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePreparedTransaction' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePreparedTransactionFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePreparedTransactionFeatureSmoke$' VERBOSE=0
```

Bound each invocation to900 seconds. Smoke expects private7 invisible to both
origin/peer, one correctly typed stable prepared row, survival after actual
physical origin exit, peer commit publishing7, and rollback9 preserving7.
Register bounded GID-specific ROLLBACK PREPARED cleanup before each prepare-capable
operation, ahead of schema/pool/role cleanup; never blanket-clean unrelated GIDs.
sql.Conn.Close only returns a borrower. Dispose the isolated pool/raw driver
connection to witness physical origin end. All worker cancellation/join paths
remain bounded. The pinned fixture enables max_prepared_transactions100 at
startup; native has no matching capacity/configuration surface.

Prepared deferrable coverage distinguishes real admission from goroutine start:
PostgreSQL's genuine SafeSnapshot observer asserts admitted predecessor0 across
PREPARE/origin close and either final outcome. Native has no public readiness
catalog/notice; package-function tests admit a real candidate through the existing
tracker barrier and assert retained original roots/frontier and predecessor0.
The shared both-backend synchronous statement-timeout companion proves a prepared
writer remains unresolved, then verifies exact fresh commit1/rollback0 after
actual completion. No scheduling sleeps or0-or1 tolerance substitute for admission.

Private `TestPreparedPopulatedReplaceCatalogGuards`,
`TestPreparedPopulatedCompetingCompletionAndRetry`,
`TestPreparedCompetingGIDDisposesWholeLoser`,
`TestPreparedLateCancellationAndWaiterObservePublication` and
`TestPreparedValidationOmitsInstallationRoots` use real admitted AST statements
and populated surviving UPDATE plus insert/deletion/catalogue/index/generator/
SSI/ticket state. They cover same-owner and foreign-owner replacement refusal,
actual competing claims, outside-metadata expression barriers, cancellation and
independent final counter veto retaining the exact UPDATE/base/version, committed
heads, original ticket, other roots and all Engine counters; whole losing-GID/
settings disposal; successful retry exactly once; and publication visible to a
real resource waiter before late cancellation. The validation-only structural
regression checks genuine live UPDATE predecessors and rejects wrong base
versions and already-ended predecessors in both phases. Validation-only work
produces no replacement heads/slot arrays; materializing work produces a separate
head and the exact update-slot manifest without installing it during validation.
These package tests issue no public SQL and add no production hooks.
Every concurrent boundary scenario registers cleanup before launching workers:
arrival/outcome waits have deadlines, cleanup releases admission/expression
barriers once, cancels outstanding operation/request contexts and joins every
launched worker through bounded waits, including assertion-failure paths.
Admitted AST fixtures specify parser-established provider defaults explicitly
(for example, CREATE INDEX uses `Method: "btree"`); an empty method is not an
engine default. The existing changed-predecessor publication regression keeps
its original canonical `mergeIndexRoot` preparation boundary and all unchanged-
state, real-peer retry, final-veto and exact row/index/SSI outcome assertions.
`TestIndexClausesReplacement` and `TestIndexClausesValidationOnlyReplacement`
also preserve the canonical nonunique replacement contract for reduced valid
parent/index catalogues: ordering/provider/shape checks require no namespace
descriptor. Namespace binding remains required when constructing UNIQUE
diagnostics. Invalid key definitions still fail both validation-only and
materializing phases.

```sh
make quickcheck PACKAGES=./internal/engine RUN='^TestPrepared' VERBOSE=0
```

Held-resource timeout Context includes genuine PostgreSQL physical tuple
coordinates only on PostgreSQL; native independently asserts its absent Context.


The set-operation feature smoke exercises all nine operator/modifier bags and
Exec counts, NULL multiplicities, precedence/parentheses, VALUES, local/global
bounds, pairwise unknown/REAL typing, SAME held parameters, derived/scalar/EXISTS
and correlated consumers, dedup-before-INSERT assignment, width/UTF8/ORDER/lock
diagnostics, stored views, foreign Restore and retained results. It executes the
actual native Dump stream on PostgreSQL; it never substitutes reconstructed SQL.
Native controls explicitly cover invalid trailing and EOF-canceled set-view
restoration, two foreign restores, destination-owned enum/domain/array grouping
after source Reset, and retained rows/schema across destination Reset/Restore.

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceQuerySetOpsFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuerySetOpsFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceQuerySetOps(Consumers|ViewsControls|SourceDependencies|Lifecycle|DemandAdmission|FeatureSmoke)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuerySetOps(Consumers|ViewsControls|SourceDependencies|Lifecycle|DemandAdmission|FeatureSmoke)$' VERBOSE=0
```

Each invocation has a900-second deadline. The complete family selector is
`RUN='^TestReferenceQuerySetOps'` in both modes; smoke does not replace it or
ordinary quickcheck/post-merge fullcheck. Lifecycle checks row-dependent late
source/assignment failures, zero-demand runtime suppression with all-arm analysis
and argument admission, ordinary native pre-cancellation, genuine observed
PostgreSQL server admission/cancellation, and SAME owner/preheld set recovery.
PostgreSQL cancellation uses the existing admitted pg_sleep helper, not a new
native function or a timing-only assertion that a fast set query was interrupted.
Pinned PostgreSQL18.0 observations ground the additional consumer rows, exact
late source/assignment diagnostics and all-arm zero-demand analysis positions.
Successful discovery does not replace completed dual-mode acceptance runs.

Stored set-view lock-boundary regression smoke covers all four modes on catalog
and sequence sources, reader-owned security_invoker/nested catalog views, and
inline-derived rejection. PostgreSQL fixture-only GRANT controls additionally
exercise ordinary SELECT-only base readers, local stored marks and revoked SELECT
under LIMIT0; native private ACL units exercise the same authority boundary
without introducing public SQL GRANT support. Run each command within900 seconds:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceQuerySetOpsStoredLockBoundaries$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuerySetOpsStoredLockBoundaries$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestQuerySetStored(InheritedMarks|LockAuthority|ReferenceKinds|RelationModes)$' VERBOSE=0
```

The private units retain local wait/strength/diagnostic recipes, ordinary-view
inheritance, all-arm admission, sequence reference-kind checks and actual acquired
relation modes. They do not duplicate public adapter scenarios. The stored-set
barrier changes inherited marks only, never ordinary SELECT/dependency traversal.
The hash-index feature smoke includes duplicate/NULL/range/order results, genuine
held probes with fresh parameters, post-Prepare mutation, method capabilities and
full diagnostics. Its controls contract exercises actual native Dump/Restore,
self/foreign replacement, retained TEMP/eager rows, failed late replay and real
PostgreSQL consumption of the unchanged native SQL. Public results alone cannot
prove indexing, so also run the package-function smoke that asserts the captured
real root, duplicate bucket candidates and consumption by the original filter:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceHashIndexFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceHashIndexFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestHashIndexLookupFeatureSmoke$' VERBOSE=0
```

Each quickcheck has a900-second deadline. Full changed-definition selectors must
run identically in both reference modes before review/integration:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(HashIndex|Unique|IndexClauses|BtreeIndex|ExpressionIndex|IndexMaintenance|IndexProperties|DeparseRelationsIndexForms|AlterTypeCompatiblePlainIndexBoundary)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(HashIndex|Unique|IndexClauses|BtreeIndex|ExpressionIndex|IndexMaintenance|IndexProperties|DeparseRelationsIndexForms|AlterTypeCompatiblePlainIndexBoundary)' VERBOSE=0
```

The `TestReferencePGIndexes` acceptance family pins the five-column view shape,
compact definitions, held execution, lifecycle, supported transactional overlays,
repeatable-read peer CREATE boundary, independent disclosure policy and full
diagnostics. Controls replay actual native Dump SQL on PostgreSQL; the existing
ready-invalid concurrent hash witness also checks descriptor enumeration.
Run the complete affected selector in both modes (900 seconds per invocation):

```sh
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^(TestReferencePGIndexes.*|TestReferenceIndexMaintenanceMetadataOracle|TestReferenceDropViewIndexWrongKind|TestReferenceConcurrentIndexHashInvalidQuery|TestReferenceConcurrentIndexFeatureSmoke)$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^(TestReferencePGIndexes.*|TestReferenceIndexMaintenanceMetadataOracle|TestReferenceDropViewIndexWrongKind|TestReferenceConcurrentIndexHashInvalidQuery|TestReferenceConcurrentIndexFeatureSmoke)$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePGIndexesFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGIndexesFeatureSmoke$' VERBOSE=0
```

Initial pinned oracle discovery passed before production. All scenario-local
discovery filters are removed; both commands now execute the same shared bodies,
including native behavioral-red before implementation. A filtered native run
is not verification. PostgreSQL exposes denied underlying objects here;
native acceptance intentionally requires schema USAGE and parent table SELECT
or SELECT on any live column. No global fixture behavior is changed.
Private `TestPGIndexes` units exercise real SetObjectACL/AuthorizationView
captures: table versus live non-indexed column grants, PUBLIC, ordinary owner,
superuser, schema denial, dropped stable attributes, revocation, retained old
captures and original invoker dispatch. Structural metadata units remain
ACL-neutral. Run alongside the shared reference gate:

```sh
make quickcheck PACKAGES=./internal/engine RUN='^(TestPGIndexes.*|TestIndexMaintenanceMetadata.*)$' VERBOSE=0
```

The shared referenceHeldStatement retains its three-field layout and adds actual
plain DB/Tx Prepare/Exec/Query consumers alongside existing Context consumers.
Its matched query uses the genuinely held statement; unmatched queries use the
underlying plain DB/Tx, never a Context stand-in. This central fixture change also
requires supplemental identical all-reference risk selection (each900 seconds):

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference' VERBOSE=0
```


PG-only relam/vector/type-provider catalog controls are genuine boundary coverage,
not native compatibility passes or skipped implementation. Native reduced-catalog
metadata is asserted independently through projectIndexMetadata package functions;
do not add unowned SQL catalogs to make a discovery query run. See
[hash design](design/PG18-INDEX-HASH.md). Generate retained parser productions
before selective checks; generation/formatting/verification belong to the root.

## How the dual-mode fixture works

`newReferenceScenario(t)` creates a fresh native engine, a middleware-wrapped
native pool and an unwrapped pool for native controls. In quick mode PostgreSQL
is absent. In real mode it also opens the pinned PostgreSQL pool. The fixture
owns a 30-second context and pool cleanup; real fixtures reset `public` before
and after each case. All real fixtures share one bounded-lifecycle container:
**do not use `t.Parallel()`**.

Non-public schemas are not reset by the fixture. Register
`schemaReferenceCleanup(t, "schema_name")` before constructing the scenario
when a case creates or moves objects into another schema; otherwise a later
case can fail with `42P06` even when it passes in isolation.
Dropping a moved view does not remove its destination schema. Prepared utility
and native replay cases must register that schema too, even when they drop every
view they created. Registering before the scenario lets fixture handles close
before schema teardown.

Queries of `pg_class` are database-wide across namespaces, not limited to the fixture's `public` reset.
Use case-specific relation names or constrain the intended namespaces when
asserting a complete candidate bag; common names such as `t` can match unrelated
objects outside the case even when its own schema cleanup is correct.
Preserve exact multiplicities and visibility assertions rather than ignoring
extra identities. The visibility source-route scenario keeps the complete
unfiltered two-peer bag and creates an unrelated `public.t` to exercise
isolation from the old generic basename.

`p.step(t, step)` executes one description on native and, when enabled,
PostgreSQL. Each backend must match the supplied expectations independently;
complete observed records are then compared for parity. SELECT checks include
column names, database type names, complete rows and owned middleware-recorded
rows/closure. Raw codec representations are checked before normalization.

Use `p.step` for straightforward streams. Use `p.backends()` and one shared
callback for transactions, pinned sessions, held statements, richer metadata,
concurrency or initial oracle discovery. Keep SQL session state on a borrowed
`*sql.Conn` or `*sql.Tx`, not an arbitrary pooled connection. Close rows before
statements, then return connections before fixture/schema cleanup. Register
cleanup immediately and cancel/join goroutines on all exits; use deterministic
synchronization, not sleeps.

The whole-root cutover is complete: root retains13 focused nonSQL units and its
startup benchmark, with no public SQL test bodies or obsolete SQL helpers.
The final driver/session/catalog ledger accounts for372 original sources and131
helper dispositions, with651 shared untagged definitions,39 genuine PostgreSQL-only
and five PostgreSQL-required differential companions.36 genuine-only and all five
differential definitions remain tagged; three genuine-only callers are untagged.
Use [family commands](DEVELOPMENT.md#driver-session-and-catalog-whole-root-cutover)
for exploration and the catalog's identical exact651-name RUN for native,
PostgreSQL and race acceptance. Select companions separately in real mode.

Native eager retained cursors and detached Columns slices are separate contracts
from pq streaming cursors and aliased Columns slices. Shared metadata callbacks
run before iteration, including empty results; raw cells are asserted before
normalization. Native precanceled/expired routes retain ordinary context errors.
For pq user cancellation use observed server admission and joined completion,
then check the SAME physical owner and preheld consumers. Do not assert a
race-prone fast precanceled pq command must return nil outputs, or cancel its
Go context during a notice callback and invent stable transport health.

Settings timeout witnesses live in `TestReferenceSettingsPublicTimeoutStages`
and `TestReferenceSettingsPublicTimeoutRollbackCleanup`. Once a physical owner
is armed with the genuine 1ms statement timer, even `SHOW` or `RESET` can
legitimately time out under scheduling pressure. The implicit-timeout scenario
therefore disposes of its armed owner without further SQL; transaction rollback
restores timeout 0 before checking SAME-owner and held-statement recovery.
Both prepared-execute routes prepare only the current candidate at timeout0,
then arm1ms before executing that genuine Conn-held handle. The rollback route
begins its transaction after preparation and closes the handle after rollback,
including fatal-path cleanup. After an untimed atomic23502 fallback, complete
candidate assertions and statement closure, then physically discard the still-armed
implicit owner without further owner SQL. The next reached candidate acquires a
fresh timeout0 owner. Rollback restores entry timeout0 on the same owner before
statement closure and recovery assertions.
All four bounded fallback sizes and the shared30s fixture deadline remain;
unused million-row fallback handles are not eagerly parsed/bound.
`TestReferenceSettingsSQL` checks tiny-timeout encoding on the arming command
and then disposes of that isolated owner. Do not increase the timer, retry an
expired-owner reset, or suppress its diagnostic to make fixture cleanup pass.
Generate and prepare only the reached bounded workload, before arming its owner.
Each candidate owns its held statement and physical connection through assertions
and closure; an untimed fallback disposes the armed implicit owner before the next
candidate opens a fresh timeout0 owner. Retain all fallback sizes and stage/backend
assertions without eager million-row preparation or a larger fixture deadline.
The superseded `TestSettingsSmoke` is removed; its typed registry, numeric,
metadata, notice/reentry, parser capture, access and connection-lifecycle
obligations remain in the existing shared `TestReferenceSettings*` consumers.
The superseded `TestNullCountSmoke` is removed. Its literal/table counts, raw
INT4 metadata and argument-count sums, nested calls, held parameter reuse and
empty-result metadata, Exec counts and complete diagnostic/nil-output contracts
remain in the shared `TestReferenceNullCount*` families. Former no-FROM rejection
cases now assert shared success; `TestReferenceNullCountInsertConsumerRoutes`
retains the original INSERT Query/Prepare routes and exact resulting state.
Malformed suffixes retain whole-input/no-prefix-effects rejection coverage,
while actual unsupported array/function-provider forms remain boundaries.


Original source methods are obligations, not interchangeable API labels.
For proven DB/Tx plain calls, set referenceStep.nonContext=true on the existing
runner: actual Exec/Query/Prepare and returned Stmt.Exec/Query retain complete
count/error-stage/nil-output/cursor assertions. Metadata and specialized Scan
hooks inspect the actual source cursor before the truthful return preceding
Context-only recording. Keep a fresh recorded Context companion; never fabricate
its recorder key for a plain call. *sql.Conn has no plain methods: original
Conn.QueryContext and physical-owner adapters remain actual Conn routes.
Explicit QueryRow/Begin/native raw calls and SAME held objects stay explicit.
referencePrepared prepares anew; it does not prove quiet Prepare or held reuse.
Consumers such as deleteReferenceConsume do not invoke step metadata/Scan
callbacks, so preserve those original checks at the callsite. Cleanup follows
rows, statements, rollback, borrowed connections, then fixture/schema/role reset;
worker joins and registered failure exits are part of the assertion union.



### Common helper reference

These helpers are package-local and directly available to `reference_test` files.
No additional helper imports or fixture setup are needed.

| Helper | Contract and use |
| --- | --- |
| `newReferenceScenario(t) *referencePair` | Required entry point for new dual-mode tests; honors `-reference` and uses a fresh native engine. Do not use legacy `newReferencePair(t)`, which always starts Docker. |
| `p.step(t, s)` | Execute and independently assert the same step on available backends, then compare observations. |
| `p.backends()` | Returns backend descriptors with `name`, `db` (`*sql.DB`), `recorder`, and `pg` (true only for PostgreSQL). Loop once over these and share the callback. |
| `p.ctx`, `p.native`, `p.nativeConnector` | Bounded scenario context; unwrapped native control pool; fresh native connector for public connector controls. `p.memory` and `p.postgres` are the wrapped pools; `p.postgres` is nil in quick mode. |
| `referenceExec(query, count, args...) referenceStep` | Exec success with exact `RowsAffected` and unsupported `LastInsertId`; DDL generally expects zero. Arguments are bound normally. Error steps require nil published results. |
| `referenceSelect(query, columns, types, rows, args...) referenceStep` | Query success with exact column names, `DatabaseTypeName` strings and normalized `[][]any` values. Supply metadata even for zero rows. |
| `referencePrepared(s) referenceStep` | Return the same step with Prepare followed by statement execution. This prepares anew per step; it does not prove held-statement reuse. |
| `referenceError(query, state, args...) referenceStep` | Exec failure with exact SQLSTATE, asserted through `SQLState() string`. Use structured diagnostics when testing message/field fidelity or failure stage. |
| `referenceErrorDiagnostic(query, want, args...) referenceStep` | Exec failure with a complete `referenceDiagnostic` expectation, including exact empty fields. |
| `referenceImplicitError(query, want, args...) referenceStep` | As above, filling both severity fields with `ERROR`. |
| `referencePosition(n) string` | Decimal diagnostic character position; zero becomes the absent-field empty string. PostgreSQL positions are character-based, not byte offsets. |
| `controlsReferenceDiagnostic(code, message) referenceDiagnostic` | Build a diagnostic with both severities `ERROR`, code and message; set any additional expected fields explicitly. |
| `referenceRunStep(t, ctx, executor, recorder, s) referenceObserved` | Run standard independent assertions on a chosen backend/session. Context steps accept DB/Conn/Tx; proven `s.nonContext=true` requires actual DB/Tx plain methods and cannot use Conn or a fake forwarding adapter. Actual cursor callbacks remain checked before returning without Context-only recording. Does not compare backends for you. |
| `referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}` | Executor wrapper for an already prepared `*sql.Stmt`, usable with `referenceRunStep`; leave `s.prepared` false, use the matching query, and close the held statement yourself. Other queries route through the embedded executor. |
| `referenceState(t, err, want) string` | Check SQLSTATE through the interface; empty `want` requires success. Useful for custom public API calls. |
| `referenceExtractDiagnostic(t, err) referenceDiagnostic` | Extract structured public native/pq diagnostics; validates diagnostic shape and excludes native claims of PostgreSQL C source locations. Useful while discovering oracle data. |
| `referenceAssertDiagnostic(t, err, &want) referenceDiagnostic` | Assert the complete structured diagnostic for custom calls. Does not accept a nil expectation. |
| `referenceNormalize(value) any` | Converts `[]byte` to string and `time.Time` to UTC without monotonic/location identity; leaves other values unchanged. Standard steps apply this automatically. |
| `noticeReferencePool(t, p, postgres, &records)` | Return a notice-enabled pool, pinned connection and recorder; append complete notice diagnostics to `records`. Select the backend using `backend.pg`; fixture registers cleanup. |
| `referenceSessionCancel(t, parent, conn, prepared, queryMode...)` | Observe real PostgreSQL pg_sleep admission, cancel through the server, join the operation and assert full57014. Optional true queryMode selects Query with nil error cursor; otherwise Exec. The caller asserts SAME-owner continuation; this is not a native precancel substitute. |
| `roleReferenceCleanup(t, names)` | Register cleanup for real PostgreSQL roles created by the test **before constructing the scenario**, so pool closure and schema reset happen before role removal. Public-schema resets do not remove cluster roles. |

### Step fields and expected data

- `referenceStep.query`, `args`, `prepared`, `selectRows`, `affected`, `state`,
  `columns`, `types` and `rows` are populated by the constructors above.
- Set `s.bag = true` for unordered results: order is ignored but duplicate
  occurrences must match. Otherwise add SQL `ORDER BY` when ordering is part of
  the expected result; never depend on traversal order.
- Set `s.checkMetadata = func(t *testing.T, rows *sql.Rows) { ... }` for complete
  raw descriptor contracts on the actual result, before iteration even when
  empty. Retain independent backend ScanType/Nullable/Length/DecimalSize flags.
- Set `s.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) { ... }` to
  rescan the current row into specialized destinations. It must not issue SQL.
  Use a shared backend callback for metadata contracts beyond database type names.
- For Query errors, start with `referenceErrorDiagnostic`, then set
  `s.selectRows = true`. Set `s.errorStage` to `"prepare"` or `"execute"` when
  stage matters; this field is checked only with a structured diagnostic.
- `s.postgresDiagnostic` permits an independently specified PostgreSQL diagnostic
  for intentional differences. Both full records are asserted first; only
  Position/Context are normalized for subsequent parity. This is not permission
  to suppress unrelated differences.
- `referenceDiagnostic` fields are `Severity`, `SeverityUnlocalized`, `Code`,
  `Message`, `Detail`, `Hint`, `Position`, `InternalPosition`, `InternalQuery`,
  `Context`, `Schema`, `Table`, `Column`, `DataType`, `Constraint`. Unspecified
  fields must actually be empty; source File/Line/Routine are not reference data.
- Integer cells normally use `int64`, floating cells `float64`, Boolean cells
  `bool`, NULL `nil`; normalized byte payloads use strings. Do not confuse
  `ColumnType.ScanType` with the actual raw scanned value. Temporal/OID and other
  codec differences can require independently captured backend expectations;
  branch on `backend.pg` for those expectations, not to duplicate the scenario.

## Self-contained example

Place this untagged file in `reference/feature_answer_test.go`. The literal
expectations illustrate the final recorded form; when adding real functionality,
validate its own expectations against the pinned PostgreSQL run first.

```go
package reference_test

import "testing"

func TestReferenceFeatureAnswer(t *testing.T) {
    p := newReferenceScenario(t)
    for _, backend := range p.backends() {
        t.Run(backend.name, func(t *testing.T) {
            // During oracle discovery only, temporarily select backend.pg
            // before this callback. The completed test runs every backend.
            conn, err := backend.db.Conn(p.ctx)
            if err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() {
                if err := conn.Close(); err != nil {
                    t.Error(err)
                }
            })
            run := func(s referenceStep) {
                referenceRunStep(t, p.ctx, conn, backend.recorder, s)
            }

            run(referenceExec("CREATE TABLE answer (v INTEGER)", 0))
            run(referenceExec("INSERT INTO answer VALUES (41), (NULL)", 2))
            for _, prepared := range []bool{false, true} {
                s := referenceSelect(
                    "SELECT v + $1::integer AS result FROM answer ORDER BY v NULLS LAST",
                    []string{"result"}, []string{"INT4"},
                    [][]any{{int64(42)}, {nil}}, int64(1),
                )
                if prepared {
                    s = referencePrepared(s)
                }
                run(s)
            }
            run(referenceError("SELECT absent FROM answer", "42703"))
        })
    }
}
```

The shared callback creates independent state on each backend. A pinned
connection owns the session, and both direct and prepared queries check exact
values, NULL propagation and metadata through the same step description. The
SQLSTATE assertion is a separate failure witness. Standard steps also check
owned middleware rows and closure; the fixture closes its pools after the
borrowed connection is returned.

Run `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFeatureAnswer$' VERBOSE=1` first, then
`make quickcheck PACKAGES=./reference RUN='^TestReferenceFeatureAnswer$'`. After any
test or implementation changes, rerun both selectively before review, then run
ordinary quickcheck and the post-integration fullcheck.

## Allocation checks

For package-function byte-allocation budgets, use calibrated
`testing.Benchmark` measurements and normalize `AllocedBytesPerOp` to one
provider evaluation when a benchmark iteration evaluates a batch.
Short process-wide `runtime.MemStats.TotalAlloc` samples can charge unrelated
runtime allocations to the provider, especially under the race detector.
Keep independently chosen payload-size and allocation-count bounds unchanged.

## Optional last-resort implementation materials

This guide is sufficient for ordinary dual-mode tests. Do **not** read other
feature tests to discover the basic convention. Only when a required advanced
fixture behavior is not covered above, optionally consult:

- `reference/fixture_test.go`: step runner, diagnostic, notice and control helpers.
- `reference/fixture_lifecycle_test.go`: mode selection, backend descriptors and
  container lifecycle.
- `Makefile`: non-standard command details.

These are last-resort implementation references, not prerequisites for each test.

## Subquery regression and instrumentation coverage

`reference/query_subquery_*_test.go` contains the shared direct/prepared,
held-handle, mutation, metadata, admission, locking and stored-view scenarios.
Both backends execute the same callback without a discovery-only backend filter:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceQuerySubquery'
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuerySubquery'
```

The integration handoff additionally supplies the exact selector for migrated
consumer/no-FROM families; those families are not replaced by the new prefix.
Each selective invocation uses a900-second deadline, in both modes before review.

Internal `TestQuerySubquery*` cases instrument actual bound plans, typed
comparison providers, lexical frame lookup, admission manifests and borrowed
stage cleanup. Sentinels prove ignored EXISTS targets versus required ORDER
keys/derived projections and normal target demand. They are not duplicate
public SQL feature fixtures and do not use fake database APIs. Parser
`TestQuerySubqueryAST` / `TestQuerySubquerySourceWalk` check structural syntax,
positions, source occurrence order and child-specific rowmark ownership.
Run those private units with
`make quickcheck PACKAGES='./internal/parser ./internal/engine' RUN='^TestQuery(Subquery|DerivedSubquerySyntax)'`.


## Cast metadata catalog discovery and smoke

The shared `TestReferencePGCast` family asserts the complete independently
specified65-row supported boundary, exact six-column shape and raw codecs,
fixed type/function identities, generated row identity invariants, direct/
prepared/held consumers, full diagnostics, omissions versus automatic coercion,
PUBLIC SELECT and native retained-row/Reset/Restore controls. PostgreSQL reads
are restricted to the same candidate pairs; additional PostgreSQL catalog rows
are legitimate. Domain identity via pg_typeof is a PostgreSQL-only phase;
native domain creation/conversion plus its unchanged exact complete bag excludes
domain rows without a new type identity API.

The genuine feature smoke combines empty shape, representative eight-row
metadata, held valid/missing/NULL/error/recovery, signed OID conversion, BOOL
text, JSON I/O, VARCHAR sizing/atomicity, UTF8 diagnostic and actual native user
Dump replay against PostgreSQL. Discovery-only filters have been removed.
Run the same completed definitions in both modes before review/integration:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferencePGCast' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGCast' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePGCastFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGCastFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^TestCastCatalog' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestPGCast' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReference(OID|CastExplicit|Character|JSON|JSONB|PGSequence|PGDepend|PGDescription|QueryOrderCatalogChar)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(OID|CastExplicit|Character|JSON|JSONB|PGSequence|PGDepend|PGDescription|QueryOrderCatalogChar)' VERBOSE=0
```

Every quickcheck invocation has a900-second deadline. Private units exercise
owner planning/application in all claimed and excluded contexts and projection
ownership/cancellation/generation retention, without issuing SQL. Selective
runs and smoke do not replace ordinary quickcheck or the post-merge fullcheck
under its single1800-second deadline.

## Numeric SUM reference contract

The untagged `TestReferenceNumericSum*` family covers all six numeric overloads,
typed NULL and nullable/promoted metadata, exact integral and decimal boundaries,
same-precision ordered floating transitions, signed zero and specials, full
diagnostics, held parameter recovery, modifiers and consumers, eager write
atomicity, authorization, cancellation and stored view control round trips.
Native control phases additionally restore into an independent Driver and replay
the actual emitted SQL on PostgreSQL; they do not invent PostgreSQL controls.
Column GRANT remains an explicit native SQL capability boundary.

Run the identical completed definitions in both modes, each under a900-second
deadline. The broad migration/risk selector covers every changed behavioral
definition plus retained RETURNING restrictions and shared AVG-state regressions;
the separate SSI selector covers its comment-only maintenance. Avoid interior
`$` anchors in `RUN`: make consumes them even inside shell single quotes.

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(NumericSum|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects|UpdateReturning|GeneratedStoredDefinitionDiagnostics|ExpressionIndexDiagnostics|NumericAvg|IntervalAvgDiagnostics|DatetimeExtractStoredConsumers|DatetimeExtractFeatureSmoke)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(NumericSum|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects|UpdateReturning|GeneratedStoredDefinitionDiagnostics|ExpressionIndexDiagnostics|NumericAvg|IntervalAvgDiagnostics|DatetimeExtractStoredConsumers|DatetimeExtractFeatureSmoke)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceNumericSumSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceNumericSumSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceSerializableWriteSkew$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSerializableWriteSkew$' VERBOSE=0
```

Numeric SUM followed the oracle-first order above: root recorded the pinned
PostgreSQL contract before authorizing production after native behavioral red.
Discovery filters are removed from the completed definitions; never reintroduce
them or adapt independently observed expectations to native output. FLOAT4 pq
ScanType is float32 while native exports float64. A later-group overflow
opening witness uses ORDER BY aggregate result, then grouping key, to force a
blocking sort before any PostgreSQL cursor rows can be published.

The `TestReferenceNumericSumReview*` regressions preserve complete direct/prepared
Exec/Query diagnostics and Unicode positions. INSERT VALUES resolves children,
FILTER and signatures before rejecting a valid numeric SUM; a valid first VALUES
row remains unpublished when a later SUM expression fails. Domain signatures
distinguish visible unqualified names from hidden qualified names, while valid
numeric-domain input returns exact NUMERIC4.656 with promoted metadata. Missing
signatures with multiple in-call ORDER BY keys use the misplaced-ORDER-BY Hint;
ambiguity, child/FILTER precedence and successfully resolved ordered calls retain
their own contracts. Root validated these new cases on pinned PostgreSQL before
removing their discovery filter and observing native behavioral red:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceNumericSumReview' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceNumericSumReview' VERBOSE=0
```

Both completed-mode runs require separate900-second deadlines after remedies;
neither the oracle/red checkpoint nor prior family passes establishes closure.

SUM child analysis uses the empty VALUES source namespace, not the destination
table's columns. The destination remains an inaccessible diagnostic candidate:
`sum(i)` reports42703 with the independently observed destination-column Detail,
not a successful source read. FILTER type errors beneath CAST and nested scalar
wrappers retain their own complete records instead of becoming target-assignment
errors. An unrelated BOOLEAN-to-INTEGER assignment control retains its ordinary
column-type message, rewrite/cast Hint and operand position. These additions
followed a separate pinned oracle/native-red round; unrelated scalar admission
and assignment diagnostics keep their existing boundaries.

Private units supplement the public contract with shared exact-state/AVG
regressions, zero-allocation integral transitions, same-precision/signed-zero
transitions, old/new-generation remapping, actual provider cancellation checks,
late/prepublication eager cancellation and exact column-ACL manifests:

```sh
make quickcheck PACKAGES=./internal/sqltype RUN='^Test(NumericSum|NumericAvg)' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^Test(Sum|NumericSum|NumericAvg|Avg|UpdateReturningMalformedSumShape)' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^Test(Sum|NumericSum|UpdateReturningAggregate)' VERBOSE=0
make quickcheck VERBOSE=0
```

Focused smoke expects direct/prepared six outputs4, promoted nullable metadata,
typed empty/all-NULL results, exact INT8 max-pair NUMERIC, decimal4.656/0.0000,
ordered REAL0, negative zero, held parameter recovery, full diagnostics/no
partial effects, global INSERT affected1 versus empty keyed0, and SUM view
control replay with retained metadata. Root runs selective checks under separate
900-second deadlines after generation/formatting, and fullcheck only after merge
under one1800-second deadline.

## Numeric arithmetic oracle-first contract

The `TestReferenceNumericArithmetic` family exercises exact decimal `+`, unary
`+`, `-`, unary `-`, `*`, `/`, `%` and integer/fractional `^`, with independent
finite strings, published PostgreSQL 18 special matrices, pinned high-precision
power strings, full diagnostics, direct/held inputs, computed NUMERIC metadata,
statement/TX atomicity, precancellation and symbolic native control replay.
`TestReferenceArithmeticBoundaries` migrates the former numeric exclusions to
shared direct/prepared successes. These test definitions do not themselves
claim native support.

The permanent expectations include observed PostgreSQL18.0 precision/scale and
complete binding/input diagnostics. The general-power guard success asserts the
full 2609-byte result through SHA256, plus exact scale/weight/prefix/suffix.
Unknown results were resolved in oracle-first discovery; no backend filters,
observation failures or numeric tolerances remain. Deparsed DEFAULT/view
expressions are replayed to assert left64/right512 semantically rather than
pinning incidental SQL whitespace. Both backends expose the actual unnamed
portal parameter Context for invalid numeric binds.
Zero-argument native Query retains its existing42P08 parameter-typing record;
pq's simple-query protocol reports42P02 instead. Supplied-argument direct and
Prepare routes independently require42P08, not protocol emulation.

Root runs each invocation under a separate 900-second deadline:

```sh
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(NumericArithmetic|ArithmeticBoundaries)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReference(NumericArithmetic|ArithmeticBoundaries)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceNumericArithmeticFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceNumericArithmeticFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReference(NumericAvg|MathScalar|FloatingArithmetic)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(NumericAvg|MathScalar|FloatingArithmetic)' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^Test(NumericArithmetic|NumericPower|NumericAvg|MathScalar)' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestExpressionNumericArithmetic' VERBOSE=0
```

The composed feature smoke demands exact0.3, large integer9223372036854775809,
product2.76000, division2.5000000000000000, remainder−1.00, typed left64/right512
with NUMERIC metadata, published fractional18.379173679952560, fresh held input
scales/NULLs, domain/zero/format-overflow diagnostics and unchanged destinations
after late failures. Its native phase additionally checks deterministic
Dump/Reset/Restore, preserved cursors and symbolic DEFAULT/view/CHECK grouping.

